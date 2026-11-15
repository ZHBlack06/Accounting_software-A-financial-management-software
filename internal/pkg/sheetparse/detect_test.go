package sheetparse

import "testing"

// ledgerRows 模拟真实样本「记帐」表的前几行。
func ledgerRows() [][]string {
	return [][]string{
		{"月份", "日期", "分类", "备注", "金额"},
		{"2018年10月", "43383", "医疗", "信诺医疗", "272.16"},
		{"2018年10月", "43403", "餐饮", "奶茶", "5"},
		{"2018年10月", "43403", "餐饮", "早饭", "1.5"},
		{"2018年10月", "43404", "交通", "地铁", "4"},
	}
}

// TestDetectLedgerSheet 记帐表：表头第 1 行、映射建议正确、不跳过。
func TestDetectLedgerSheet(t *testing.T) {
	d := Detect(ledgerRows(), "记帐")
	if d.HeaderRow != 1 {
		t.Errorf("header_row = %d，want 1", d.HeaderRow)
	}
	if len(d.Candidates) == 0 || d.Candidates[0] != 1 {
		t.Errorf("header_row_candidates = %v，want 含 1", d.Candidates)
	}
	if d.Mapping[TargetDate] != "日期" {
		t.Errorf("date 映射 = %q，want 日期", d.Mapping[TargetDate])
	}
	if d.Mapping[TargetAmount] != "金额" {
		t.Errorf("amount 映射 = %q，want 金额", d.Mapping[TargetAmount])
	}
	if d.Mapping[TargetCategory] != "分类" {
		t.Errorf("category 映射 = %q，want 分类", d.Mapping[TargetCategory])
	}
	if d.Mapping[TargetRemark] != "备注" {
		t.Errorf("remark 映射 = %q，want 备注", d.Mapping[TargetRemark])
	}
	if d.Skip {
		t.Errorf("记帐表不应建议跳过：%s", d.SkipReason)
	}
	if d.DateHasTime {
		t.Error("记帐表日期列无时分秒，date_has_time 应为 false")
	}
	if d.Direction != "expense" {
		t.Errorf("默认方向 = %q，want expense", d.Direction)
	}
	if d.Warning != "" {
		t.Errorf("不应有表头识别警告，实得 %q", d.Warning)
	}
}

// TestDetectPivotSheet 统计表：两行表头透视表，应建议跳过。
func TestDetectPivotSheet(t *testing.T) {
	rows := [][]string{
		{"求和项:金额", "分类"},
		{"月份", "餐饮", "宠物", "家用", "总计"},
		{"2018年10月", "16.20", "", "", "16.20"},
		{"2018年11月", "448.80", "283.00", "863.20", "1,595.00"},
	}
	d := Detect(rows, "统计")
	if !d.Skip {
		t.Fatal("统计表应建议跳过")
	}
	if d.SkipReason == "" {
		t.Error("跳过原因不能为空")
	}
	if len(d.Candidates) < 2 {
		t.Errorf("应识别出两行表头候选，实得 %v", d.Candidates)
	}
}

// TestDetectNoHeaderSheet 张理默表：无表头 + 无金额/日期列 → 跳过 + 警告。
func TestDetectNoHeaderSheet(t *testing.T) {
	rows := [][]string{
		{"11-17-19", "零花钱转存", "40", "0.25", "5%", "4月", "15", "30", "450"},
		{"11-24-19", "零花钱转存", "40", "0.21"},
		{"12-01-19", "零花钱转存", "40", "0.17"},
	}
	d := Detect(rows, "张理默")
	if d.HeaderRow != 1 {
		t.Errorf("未识别到表头时应回落第 1 行，实得 %d", d.HeaderRow)
	}
	if d.Warning == "" {
		t.Error("未识别到表头应给 warning")
	}
	if !d.Skip {
		t.Error("无日期/金额列的表应建议跳过")
	}
}

// TestDetectIncomeSheet 收入表：方向建议 income。
func TestDetectIncomeSheet(t *testing.T) {
	rows := [][]string{
		{"日期", "月份", "分类", "备注", "金额"},
		{"43414", "2018月11月", "收入", "工资", "24328.31"},
		{"43444", "2018月12月", "收入", "工资", "12543.15"},
	}
	d := Detect(rows, "收入")
	if d.Direction != "income" {
		t.Errorf("收入表默认方向 = %q，want income", d.Direction)
	}
	if d.Mapping[TargetDate] != "日期" {
		t.Errorf("date 映射 = %q，want 日期", d.Mapping[TargetDate])
	}
	if d.Skip {
		t.Errorf("收入表不应跳过：%s", d.SkipReason)
	}
}

// TestDetectOtherVsQitaNotMerged 断言「其他」与「其它」不会被归一化合并（禁止归一化的单测）。
func TestDetectOtherVsQitaNotMerged(t *testing.T) {
	if NormalizeName("其他") == NormalizeName("其它") {
		t.Fatal("「其他」与「其它」不得被视为同一名称（禁止任何归一化）")
	}
	if NormalizeName("其他") != "其他" || NormalizeName("其它") != "其它" {
		t.Fatal("名称处理只允许 TrimSpace，不得改写")
	}
}
