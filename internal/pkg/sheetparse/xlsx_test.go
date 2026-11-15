package sheetparse

import (
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"ledger/internal/pkg/apierr"
)

// buildTestWorkbook 造一个含 3 张表的小 xlsx（记帐 / 统计 / 收入）。
func buildTestWorkbook(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	// 默认 Sheet1 改名为「记帐」
	if err := f.SetSheetName("Sheet1", "记帐"); err != nil {
		t.Fatal(err)
	}
	ledger := [][]any{
		{"月份", "日期", "分类", "备注", "金额"},
		{"2018年10月", 43383, "医疗", "信诺医疗", "272.16"},
		{"2018年10月", 43403, "餐饮", "奶茶", "5"},
		{"2018年10月", 43403, "餐饮", "早饭", "1.5"},
	}
	for i, row := range ledger {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("记帐", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.NewSheet("统计"); err != nil {
		t.Fatal(err)
	}
	pivot := [][]any{
		{"求和项:金额", "分类"},
		{"月份", "餐饮", "总计"},
		{"2018年10月", "16.20", "16.20"},
	}
	for i, row := range pivot {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("统计", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.NewSheet("收入"); err != nil {
		t.Fatal(err)
	}
	income := [][]any{
		{"日期", "月份", "分类", "备注", "金额"},
		{43414, "2018月11月", "收入", "工资", "24328.31"},
	}
	for i, row := range income {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("收入", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestProbeFileXLSX 探测内存工作簿：sheet 顺序、行数、映射建议、汇总表跳过。
func TestProbeFileXLSX(t *testing.T) {
	data := buildTestWorkbook(t)
	meta, err := ProbeFile(data, "小账本.xlsx", 8)
	if err != nil {
		t.Fatalf("ProbeFile 失败: %v", err)
	}
	if meta.Kind != KindXLSX {
		t.Errorf("kind = %q，want xlsx", meta.Kind)
	}
	if len(meta.SHA1) != 40 {
		t.Errorf("sha1 长度异常: %q", meta.SHA1)
	}
	if len(meta.Sheets) != 3 {
		t.Fatalf("sheet 数 = %d，want 3", len(meta.Sheets))
	}
	names := []string{"记帐", "统计", "收入"}
	for i, want := range names {
		if meta.Sheets[i].SheetName != want {
			t.Errorf("sheet[%d] = %q，want %q", i, meta.Sheets[i].SheetName, want)
		}
	}
	ledger := meta.Sheets[0]
	if ledger.RowCount != 4 {
		t.Errorf("记帐 row_count = %d，want 4（含表头）", ledger.RowCount)
	}
	if ledger.ColCount != 5 {
		t.Errorf("记帐 col_count = %d，want 5", ledger.ColCount)
	}
	if ledger.HeaderRow != 1 {
		t.Errorf("记帐 header_row = %d，want 1", ledger.HeaderRow)
	}
	if ledger.SuggestedMapping[TargetDate] != "日期" {
		t.Errorf("记帐 date 建议 = %q", ledger.SuggestedMapping[TargetDate])
	}
	if len(ledger.SampleRows) != 3 {
		t.Errorf("记帐 sample_rows = %d，want 3", len(ledger.SampleRows))
	}
	if ledger.SuggestedSkip {
		t.Errorf("记帐不应跳过：%s", ledger.SkipReason)
	}

	pivot := meta.Sheets[1]
	if !pivot.SuggestedSkip {
		t.Errorf("统计表应建议跳过，reason=%q", pivot.SkipReason)
	}

	income := meta.Sheets[2]
	if income.SuggestedDirection != "income" {
		t.Errorf("收入表建议方向 = %q，want income", income.SuggestedDirection)
	}
}

// TestProbeFileXLSRejected .xls 明确拒绝并给中文提示。
func TestProbeFileXLSRejected(t *testing.T) {
	data := append(append([]byte{}, ole2Magic...), make([]byte, 64)...)
	_, err := ProbeFile(data, "老账本.xls", 8)
	if err == nil {
		t.Fatal(".xls 应当被拒绝")
	}
	ae, ok := err.(*apierr.Error)
	if !ok {
		t.Fatalf("错误类型 = %T，want *apierr.Error", err)
	}
	if ae.Code != apierr.CodeUnsupportedSpreadsheet {
		t.Errorf("错误码 = %q，want %q", ae.Code, apierr.CodeUnsupportedSpreadsheet)
	}
	if ae.Message == "" {
		t.Error("必须给出中文提示")
	}
}

// TestProbeFileCSV CSV 探测：分号分隔 + UTF-8 BOM。
func TestProbeFileCSV(t *testing.T) {
	content := "\ufeff日期;分类;备注;金额\n2020-01-02;餐饮;午饭;12.50\n2020-01-02;交通;地铁;4.00\n"
	meta, err := ProbeFile([]byte(content), "账目.csv", 8)
	if err != nil {
		t.Fatalf("ProbeFile(csv) 失败: %v", err)
	}
	if meta.Kind != KindCSV {
		t.Errorf("kind = %q，want csv", meta.Kind)
	}
	if len(meta.Sheets) != 1 {
		t.Fatalf("CSV 应只有一张表，实得 %d", len(meta.Sheets))
	}
	s := meta.Sheets[0]
	if s.RowCount != 3 {
		t.Errorf("row_count = %d，want 3", s.RowCount)
	}
	if s.SuggestedMapping[TargetDate] != "日期" {
		t.Errorf("date 映射 = %q，want 日期", s.SuggestedMapping[TargetDate])
	}
	if s.SuggestedMapping[TargetAmount] != "金额" {
		t.Errorf("amount 映射 = %q，want 金额", s.SuggestedMapping[TargetAmount])
	}
}

// TestOpenTableXLSX 流式读取：行号与内容正确。
func TestOpenTableXLSX(t *testing.T) {
	data := buildTestWorkbook(t)
	tbl, err := OpenTable(data, KindXLSX, 0)
	if err != nil {
		t.Fatalf("OpenTable 失败: %v", err)
	}
	defer tbl.Close()
	rows, err := readAll(tbl)
	if err != nil {
		t.Fatalf("readAll 失败: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("行数 = %d，want 4", len(rows))
	}
	if rows[0][0] != "月份" || rows[0][4] != "金额" {
		t.Errorf("表头行 = %v", rows[0])
	}
	// 日期列是序列号原文（RawCellValue）
	if rows[1][1] != "43383" {
		t.Errorf("日期原始值 = %q，want 43383", rows[1][1])
	}
}

// TestRowParserLedger 按映射解析一行：方向、金额、去重键。
func TestRowParserLedger(t *testing.T) {
	columns := []string{"月份", "日期", "分类", "备注", "金额"}
	plan := &SheetPlan{
		FileID: "fs_test0001", SheetIndex: 1,
		HeaderRow:     1,
		Mapping:       Mapping{Date: "日期", Amount: "金额", Category: "分类", Remark: "备注"},
		DirectionRule: DirectionRule{Mode: "form_default", Default: "expense"},
		TimePrecision: PrecisionDay,
	}
	p := NewRowParser(columns, plan, false)
	row := []string{"2018年10月", "43383", "医疗", "信诺医疗", "272.16"}
	pr, rerr := p.Parse(row, 2, "abc123")
	if rerr != nil {
		t.Fatalf("解析失败: %+v", rerr)
	}
	if pr.Type != "expense" || pr.AmountCents != 27216 {
		t.Errorf("type/amount = %s/%d", pr.Type, pr.AmountCents)
	}
	if pr.LocalDate != "2018-10-10" {
		t.Errorf("local_date = %s", pr.LocalDate)
	}
	if pr.HasTime {
		t.Error("序列号整数不应带时间")
	}
	if pr.TimePrecision != PrecisionDay {
		t.Errorf("time_precision = %q，want day", pr.TimePrecision)
	}
	if pr.ExternalID == "" || pr.ExternalID[:4] != "imp:" {
		t.Errorf("external_id = %q", pr.ExternalID)
	}
	if pr.RowKey != "fs_test0001:1:2" {
		t.Errorf("row_key = %q", pr.RowKey)
	}
}

// TestRowParserMonthOnly 只有年月列：按当月 1 日 + 警告。
func TestRowParserMonthOnly(t *testing.T) {
	plan := &SheetPlan{
		FileID: "fs_x", Mapping: Mapping{Month: "月份", Amount: "金额"},
		DirectionRule: DirectionRule{Mode: "form_default", Default: "income"},
		TimePrecision: PrecisionDay,
	}
	p := NewRowParser([]string{"月份", "金额"}, plan, false)
	pr, rerr := p.Parse([]string{"2018月11月", "100.00"}, 2, "s")
	if rerr != nil {
		t.Fatalf("解析失败: %+v", rerr)
	}
	if pr.LocalDate != "2018-11-01" {
		t.Errorf("local_date = %s，want 2018-11-01", pr.LocalDate)
	}
	if pr.Type != "income" {
		t.Errorf("type = %s，want income", pr.Type)
	}
	found := false
	for _, w := range pr.Warnings {
		if w != "" {
			found = true
		}
	}
	if !found {
		t.Error("应带「月份写法有误」类警告")
	}
}

// TestRowParserErrors 空日期 / 脏金额 → RowError（不静默丢行）。
func TestRowParserErrors(t *testing.T) {
	plan := &SheetPlan{
		FileID: "fs_x", Mapping: Mapping{Date: "日期", Amount: "金额"},
		DirectionRule: DirectionRule{Mode: "form_default"},
		TimePrecision: PrecisionDay,
	}
	p := NewRowParser([]string{"日期", "金额"}, plan, false)
	if _, rerr := p.Parse([]string{"", "10"}, 37, "s"); rerr == nil || rerr.Reason != "日期为空" {
		t.Errorf("空日期应报「日期为空」，实得 %+v", rerr)
	}
	if _, rerr := p.Parse([]string{"2018-10-10", "总计"}, 512, "s"); rerr == nil {
		t.Error("脏金额应报错")
	}
}

// TestRowExternalIDStable 去重指纹：同文件同表同行稳定，任一维度变化即不同。
func TestRowExternalIDStable(t *testing.T) {
	const sha = "65e3e0e14c973a9cd84d9354ffcfd13a536a200f"
	a := RowExternalID(sha, 1, 2)
	if a != RowExternalID(sha, 1, 2) {
		t.Fatal("同一 (文件,表,行) 应得到同一指纹（幂等前提）")
	}
	if !strings.HasPrefix(a, "imp:") {
		t.Errorf("指纹应带 imp: 前缀，实得 %q", a)
	}
	if len(a) != len("imp:")+20 {
		t.Errorf("指纹长度 = %d，want 24", len(a))
	}
	for _, other := range []string{
		RowExternalID(sha, 1, 3),
		RowExternalID(sha, 2, 2),
		RowExternalID("0000000000000000000000000000000000000000", 1, 2),
	} {
		if other == a {
			t.Fatalf("不同 (文件,表,行) 不应得到同一指纹: %q", other)
		}
	}
}
