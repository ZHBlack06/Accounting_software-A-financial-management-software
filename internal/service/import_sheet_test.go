package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/sheetparse"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
	"ledger/internal/store"
)

// sheetEnv 导入服务测试环境（临时数据目录 + 完整依赖）。
type sheetEnv struct {
	dir     string
	s       *store.Store
	sheets  *service.ImportSheetService
	txn     *service.TxnService
	bal     *service.BalanceService
	mat     *service.Materializer
	txr     *store.TxRepo
	meta    *store.MetaRepo
	account string
}

func newSheetEnv(t *testing.T) *sheetEnv {
	t.Helper()
	dir, err := os.MkdirTemp("", "ledger-sheet-")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	if err = s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(); err != nil {
		t.Fatal(err)
	}
	views := store.NewViewManager(s)
	if err = views.StartReconcile(); err != nil {
		t.Fatal(err)
	}
	meta := store.NewMetaRepo(s)
	master := store.NewMasterRepo(s)
	txr := store.NewTxRepo(s, views)
	mat := service.NewMaterializer(s, meta, txr)
	bal := service.NewBalanceService(s, meta, txr, mat)
	txn := service.NewTxnService(s, meta, txr, master, bal)
	batches := store.NewImportBatchRepo(s)
	svc := service.NewImportSheetService(s, meta, master, txn, bal, batches, mat, dir)
	accs, err := master.ListAccounts(false)
	if err != nil || len(accs) == 0 {
		t.Fatalf("读取种子账户失败: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return &sheetEnv{dir: dir, s: s, sheets: svc, txn: txn, bal: bal, mat: mat, txr: txr, meta: meta, account: accs[0].ID}
}

// buildSheetWorkbook 造一个含「账目」（正常表）与「汇总」（透视表）的工作簿。
func buildSheetWorkbook(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", "账目"); err != nil {
		t.Fatal(err)
	}
	ledger := [][]any{
		{"月份", "日期", "分类", "备注", "金额"},
		{"2020年01月", "43831", "其他", "杂项", "10.00"},
		{"2020年01月", "43831", "其它", "另一个写法", "20.00"},
		{"2020年01月", "43831", "餐饮", "午饭", "30.00"},
		{"2020年01月", "43832", "餐饮", "奶茶", "5.50"},
	}
	for i, row := range ledger {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("账目", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.NewSheet("汇总"); err != nil {
		t.Fatal(err)
	}
	pivot := [][]any{
		{"求和项:金额", "分类"},
		{"月份", "餐饮", "总计"},
		{"2020年01月", "35.50", "35.50"},
	}
	for i, row := range pivot {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("汇总", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// sheetMapping 账目表列映射（日期/金额/分类/备注）。
func sheetMapping() sheetparse.Mapping {
	return sheetparse.Mapping{Date: "日期", Amount: "金额", Category: "分类", Remark: "备注"}
}

// sheetDirection 方向规则（按表默认值）。
func sheetDirection(def string) sheetparse.DirectionRule {
	return sheetparse.DirectionRule{Mode: "form_default", Default: def}
}

// ledgerPlan 账目表的解析计划。
func ledgerPlan(fileID string, sheetIndex int) service.SheetPlanInput {
	return service.SheetPlanInput{
		FileID: fileID, SheetIndex: sheetIndex, HeaderRow: 1, Skip: false,
		Mapping:       sheetMapping(),
		DirectionRule: sheetDirection("expense"),
		TimePrecision: "day",
	}
}

// TestImportSheetEndToEnd probe → preview → commit → 幂等 → 去重。
func TestImportSheetEndToEnd(t *testing.T) {
	e := newSheetEnv(t)
	data := buildSheetWorkbook(t)
	ctx := context.Background()

	probe, err := e.sheets.Probe(ctx, []service.ProbeFileInput{{FileName: "账目.xlsx", Data: data}})
	if err != nil {
		t.Fatalf("probe 失败: %v", err)
	}
	if len(probe.Files) != 1 || probe.Files[0].SheetCount != 2 {
		t.Fatalf("probe sheet 数异常: %+v", probe.Files)
	}
	fileID := probe.Files[0].FileID
	var ledgerIdx, pivotIdx int
	for _, sh := range probe.Files[0].Sheets {
		switch sh.SheetName {
		case "账目":
			ledgerIdx = sh.SheetIndex
			if sh.SuggestedSkip {
				t.Fatalf("账目表不应建议跳过: %s", sh.SkipReason)
			}
		case "汇总":
			pivotIdx = sh.SheetIndex
			if !sh.SuggestedSkip {
				t.Error("汇总表应建议跳过")
			}
		}
	}

	prev, err := e.sheets.Preview(ctx, &service.PreviewInput{
		BatchID: probe.BatchID, Defaults: service.PreviewDefaults{AccountID: e.account},
		Sheets: []service.SheetPlanInput{ledgerPlan(fileID, ledgerIdx)},
	})
	if err != nil {
		t.Fatalf("preview 失败: %v", err)
	}
	if prev.Summary.ParsedOK != 4 || prev.Summary.Error != 0 {
		t.Fatalf("preview 统计异常: %+v", prev.Summary)
	}
	// 其他 / 其它 必须是两个独立分类
	var others, qitas int
	for _, c := range prev.AutoCreate.Categories {
		switch c.Name {
		case "其他":
			others++
		case "其它":
			qitas++
		}
	}
	if others != 1 || qitas != 1 {
		t.Fatalf("其他/其它 应各出现一次，实得 其他=%d 其它=%d（分类清单 %+v）",
			others, qitas, prev.AutoCreate.Categories)
	}

	commit, err := e.sheets.Commit(ctx, &service.CommitInput{
		BatchID:                 probe.BatchID,
		Sheets:                  []service.CommitSheetRef{{FileID: fileID, SheetIndex: ledgerIdx}},
		Defaults:                service.CommitDefaults{AccountID: e.account},
		CreateMissingCategories: true, CreateMissingTags: true,
		SkipDuplicates: true, AllowFrozen: true,
	})
	if err != nil {
		t.Fatalf("commit 失败: %v", err)
	}
	if commit.Created != 4 {
		t.Fatalf("created = %d，want 4", commit.Created)
	}
	if len(commit.CreatedCategories) == 0 {
		t.Error("应自动创建分类（其他/其它 未命中种子）")
	}

	// 同日 3 行分布 04:00 / 12:00 / 20:00，第二天 1 行 → 12:00
	rows, err := e.s.SQL.Query(`SELECT transaction_time, local_date FROM transactions
		WHERE local_date = '2020-01-01' ORDER BY transaction_time ASC`)
	if err != nil {
		t.Fatal(err)
	}
	var times []string
	for rows.Next() {
		var ts int64
		var d string
		if err = rows.Scan(&ts, &d); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		times = append(times, timeutil.FormatLocal(ts))
	}
	rows.Close()
	want := []string{"2020-01-01 04:00:00", "2020-01-01 12:00:00", "2020-01-01 20:00:00"}
	if len(times) != 3 {
		t.Fatalf("同日行数 = %d，want 3（%v）", len(times), times)
	}
	for i := range want {
		if times[i] != want[i] {
			t.Errorf("同日第 %d 条 = %s，want %s", i, times[i], want[i])
		}
	}

	// 二次提交同一批次 → 409 IMPORT_BATCH_COMMITTED
	_, err = e.sheets.Commit(ctx, &service.CommitInput{
		BatchID:                 probe.BatchID,
		Sheets:                  []service.CommitSheetRef{{FileID: fileID, SheetIndex: ledgerIdx}},
		Defaults:                service.CommitDefaults{AccountID: e.account},
		CreateMissingCategories: true, AllowFrozen: true, SkipDuplicates: true,
	})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeImportBatchCommitted {
		t.Fatalf("二次提交应报 IMPORT_BATCH_COMMITTED，实得 %v", err)
	}

	// 重新上传同一文件 → 全部判重
	probe2, err := e.sheets.Probe(ctx, []service.ProbeFileInput{{FileName: "账目.xlsx", Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	fileID2 := probe2.Files[0].FileID
	prev2, err := e.sheets.Preview(ctx, &service.PreviewInput{
		BatchID: probe2.BatchID, Defaults: service.PreviewDefaults{AccountID: e.account},
		Sheets: []service.SheetPlanInput{ledgerPlan(fileID2, ledgerIdx)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prev2.Summary.DupCount != 4 {
		t.Fatalf("重复导入应全部判重，dup_count = %d", prev2.Summary.DupCount)
	}
	commit2, err := e.sheets.Commit(ctx, &service.CommitInput{
		BatchID:                 probe2.BatchID,
		Sheets:                  []service.CommitSheetRef{{FileID: fileID2, SheetIndex: ledgerIdx}},
		Defaults:                service.CommitDefaults{AccountID: e.account},
		CreateMissingCategories: true, SkipDuplicates: true, AllowFrozen: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit2.Created != 0 || commit2.SkippedDup != 4 {
		t.Fatalf("重复提交结果异常: created=%d dup=%d", commit2.Created, commit2.SkippedDup)
	}
	_ = pivotIdx
}

// TestImportSheetMissingMapping 缺金额/日期映射 → 400 IMPORT_SHEET_INVALID。
func TestImportSheetMissingMapping(t *testing.T) {
	e := newSheetEnv(t)
	data := buildSheetWorkbook(t)
	ctx := context.Background()
	probe, err := e.sheets.Probe(ctx, []service.ProbeFileInput{{FileName: "账目.xlsx", Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	in := ledgerPlan(probe.Files[0].FileID, 0)
	in.Mapping = sheetparse.Mapping{Amount: "金额"}
	_, err = e.sheets.Preview(ctx, &service.PreviewInput{
		BatchID: probe.BatchID, Defaults: service.PreviewDefaults{AccountID: e.account},
		Sheets: []service.SheetPlanInput{in},
	})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeImportSheetInvalid {
		t.Fatalf("缺日期映射应报 IMPORT_SHEET_INVALID，实得 %v", err)
	}
}

// TestImportSheetXLSRejected .xls → 422 UNSUPPORTED_SPREADSHEET。
func TestImportSheetXLSRejected(t *testing.T) {
	e := newSheetEnv(t)
	xls := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, bytes.Repeat([]byte{0}, 128)...)
	_, err := e.sheets.Probe(context.Background(),
		[]service.ProbeFileInput{{FileName: "旧账本.xls", Data: xls}})
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeUnsupportedSpreadsheet {
		t.Fatalf(".xls 应报 UNSUPPORTED_SPREADSHEET，实得 %v", err)
	}
}

// TestEnsureCategoriesKeepsOtherAndQita 「其他」与「其它」不合并（§4.5 铁律）。
func TestEnsureCategoriesKeepsOtherAndQita(t *testing.T) {
	e := newSheetEnv(t)
	ctx := context.Background()
	err := e.s.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := service.EnsureCategoriesTx(tx, []service.CategoryNeed{
			{Name: "其他", TransactionType: "expense", Level: 1},
			{Name: "其它", TransactionType: "expense", Level: 1},
			{Name: "其他", TransactionType: "expense", Level: 1}, // 同批重复 → 复用
			{Name: "其他收入", TransactionType: "income", Level: 1},
		})
		if err != nil {
			return err
		}
		// 种子已有「其他」「其他收入」，只有「其它」需要新建
		if len(res.Created) != 1 || res.Created[0].Name != "其它" {
			t.Errorf("应只新建「其它」，实得 %+v", res.Created)
		}
		if len(res.IDs) != 3 {
			t.Errorf("应解析出 3 个分类 ID，实得 %+v", res.IDs)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err = e.s.SQL.QueryRow(`SELECT COUNT(1) FROM categories WHERE name IN ('其他','其它') AND parent_id IS NULL`).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("其他/其它 应各有一条记录，实得 %d", n)
	}
}

// gbkCSV 把 UTF-8 CSV 转成 GBK 字节（同一份内容、不同编码 → 不同文件 sha1 → 不同 external_id）。
func gbkCSV(t *testing.T, utf8Body string) []byte {
	t.Helper()
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(utf8Body))
	if err != nil {
		t.Fatalf("GBK 编码失败: %v", err)
	}
	return out
}

// commitSheets 便捷提交（默认参数）。
func commitSheets(t *testing.T, svc *service.ImportSheetService, batchID, fileID string,
	sheetIndex int, accountID string, skipSoft bool) *service.CommitResult {
	t.Helper()
	res, err := svc.Commit(context.Background(), &service.CommitInput{
		BatchID: batchID,
		Sheets:  []service.CommitSheetRef{{FileID: fileID, SheetIndex: sheetIndex}},
		RowKeys: []string{}, Defaults: service.CommitDefaults{AccountID: accountID},
		CreateMissingCategories: true, CreateMissingTags: true,
		SkipDuplicates: true, SkipSoftDuplicates: skipSoft, AllowFrozen: true,
	})
	if err != nil {
		t.Fatalf("commit 失败: %v", err)
	}
	return res
}

// TestImportSoftDuplicateNotSilentlyDropped P1 回归：同内容不同编码（external_id 不同）
// 二次导入时，skip_soft_duplicates=false 必须**照常写入**，不得计成 skipped_dup。
func TestImportSoftDuplicateNotSilentlyDropped(t *testing.T) {
	e := newSheetEnv(t)
	body := "日期,分类,备注,金额\n" +
		"2020-01-01,餐饮,午饭,10.00\n" +
		"2020-01-01,交通,地铁,4.00\n" +
		"2020-01-01,餐饮,晚饭,20.00\n" +
		"2020-01-01,购物,日用品,6.50\n"
	utf8Data := []byte(body)
	gbkData := gbkCSV(t, body)
	if bytes.Equal(utf8Data, gbkData) {
		t.Fatal("前置条件失败：两份字节应不同（编码不同）")
	}

	// 第一次：UTF-8 导入
	p1, err := e.sheets.Probe(context.Background(),
		[]service.ProbeFileInput{{FileName: "a.csv", Data: utf8Data}})
	if err != nil {
		t.Fatal(err)
	}
	f1 := p1.Files[0]
	plan1 := csvPlan(f1.FileID)
	prev1, err := e.sheets.Preview(context.Background(), &service.PreviewInput{
		BatchID: p1.BatchID, Defaults: service.PreviewDefaults{AccountID: e.account},
		Sheets: []service.SheetPlanInput{plan1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prev1.Summary.ParsedOK != 4 {
		t.Fatalf("首次 preview parsed_ok = %d，want 4", prev1.Summary.ParsedOK)
	}
	r1 := commitSheets(t, e.sheets, p1.BatchID, f1.FileID, 0, e.account, false)
	if r1.Created != 4 || r1.SkippedDup != 0 {
		t.Fatalf("首次导入 created=%d dup=%d，want 4/0", r1.Created, r1.SkippedDup)
	}

	// 第二次：同一内容 GBK 编码（external_id 不同，内容完全一致）
	p2, err := e.sheets.Probe(context.Background(),
		[]service.ProbeFileInput{{FileName: "b.csv", Data: gbkData}})
	if err != nil {
		t.Fatal(err)
	}
	f2 := p2.Files[0]
	if f2.SHA1 == f1.SHA1 {
		t.Fatal("两份文件 sha1 应不同（否则 external_id 相同，测不到软重复）")
	}
	plan2 := csvPlan(f2.FileID)
	prev2, err := e.sheets.Preview(context.Background(), &service.PreviewInput{
		BatchID: p2.BatchID, Defaults: service.PreviewDefaults{AccountID: e.account},
		Sheets: []service.SheetPlanInput{plan2},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 识别阶段：external_id 不重复、软重复 4 条
	if prev2.Summary.DupCount != 0 || prev2.Summary.SoftDupCount != 4 {
		t.Fatalf("preview dup=%d soft=%d，want 0/4",
			prev2.Summary.DupCount, prev2.Summary.SoftDupCount)
	}

	// P1 核心断言：skip_soft_duplicates=false → 全部写入，绝不静默丢弃
	r2 := commitSheets(t, e.sheets, p2.BatchID, f2.FileID, 0, e.account, false)
	if r2.Created != 4 {
		t.Errorf("软重复行被静默丢弃：created=%d（want 4）dup=%d soft=%d invalid=%d",
			r2.Created, r2.SkippedDup, r2.SkippedSoftDup, r2.SkippedInvalid)
	}
	if r2.SkippedDup != 0 {
		t.Errorf("内容重复不应计入 skipped_dup，实得 %d", r2.SkippedDup)
	}
	if r2.SkippedSoftDup != 0 {
		t.Errorf("skip_soft_duplicates=false 时 skipped_soft_dup 应为 0，实得 %d", r2.SkippedSoftDup)
	}
	assertAccounted(t, r2, prev2.Summary.ParsedOK)

	// skip_soft_duplicates=true → 计进 skipped_soft_dup（不是 skipped_dup）
	// 第三次改用 UTF-8 BOM（字节不同 → sha1/external_id 不同，解析内容完全一致）
	bomData := append([]byte{0xEF, 0xBB, 0xBF}, utf8Data...)
	p3, err := e.sheets.Probe(context.Background(),
		[]service.ProbeFileInput{{FileName: "c.csv", Data: bomData}})
	if err != nil {
		t.Fatal(err)
	}
	f3 := p3.Files[0]
	prev3, err := e.sheets.Preview(context.Background(), &service.PreviewInput{
		BatchID: p3.BatchID, Defaults: service.PreviewDefaults{AccountID: e.account},
		Sheets: []service.SheetPlanInput{csvPlan(f3.FileID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	r3 := commitSheets(t, e.sheets, p3.BatchID, f3.FileID, 0, e.account, true)
	if r3.Created != 0 || r3.SkippedSoftDup != 4 || r3.SkippedDup != 0 {
		t.Errorf("skip_soft_duplicates=true 结果 created=%d dup=%d soft=%d，want 0/0/4",
			r3.Created, r3.SkippedDup, r3.SkippedSoftDup)
	}
	assertAccounted(t, r3, prev3.Summary.ParsedOK)

	// 库内应恰好 8 笔（4 + 4），一条不丢
	var n int
	if err = e.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Errorf("库内交易 = %d，want 8（首次 4 + 二次软重复 4）", n)
	}
}

// assertAccounted 计数守恒：created + skipped_* == parsed_ok（P1 验收等式）。
func assertAccounted(t *testing.T, r *service.CommitResult, parsedOK int) {
	t.Helper()
	got := r.Created + r.SkippedDup + r.SkippedSoftDup + r.SkippedInvalid
	if got != parsedOK {
		t.Errorf("计数不守恒：created(%d)+dup(%d)+soft(%d)+invalid(%d)=%d，parsed_ok=%d",
			r.Created, r.SkippedDup, r.SkippedSoftDup, r.SkippedInvalid, got, parsedOK)
	}
	if len(r.Warnings) > 0 {
		t.Errorf("不应出现计数告警: %v", r.Warnings)
	}
}

// csvPlan CSV 单表解析计划。
func csvPlan(fileID string) service.SheetPlanInput {
	return service.SheetPlanInput{
		FileID: fileID, SheetIndex: 0, HeaderRow: 1, Skip: false,
		Mapping:       sheetparse.Mapping{Date: "日期", Amount: "金额", Category: "分类", Remark: "备注"},
		DirectionRule: sheetparse.DirectionRule{Mode: "form_default", Default: "expense"},
		TimePrecision: "day",
	}
}

// TestCreateBatchHardVsSoftDuplicate CreateBatch 去重语义：
// external_id 命中 → 硬重复跳过；内容一致 → 软重复照常写入。
func TestCreateBatchHardVsSoftDuplicate(t *testing.T) {
	e := newSheetEnv(t)
	ctx := context.Background()
	ts := timeutil.NowUnix()
	base := func(extID string) *service.CreateTxnInput {
		return &service.CreateTxnInput{
			TransactionTime: &ts, TransactionType: "expense", AmountCents: 1234,
			AccountID: e.account, MerchantText: "便利店", Source: "import",
			ExternalID: extID, AllowFrozen: true,
		}
	}
	// 第 1 笔：正常写入
	r1, err := e.txn.CreateBatch(ctx, []*service.CreateTxnInput{base("imp:aaa")}, true)
	if err != nil || len(r1.Created) != 1 {
		t.Fatalf("首笔写入失败: %v %+v", err, r1)
	}
	// 第 2 笔：同 external_id（硬重复）→ 跳过
	r2, err := e.txn.CreateBatch(ctx, []*service.CreateTxnInput{base("imp:aaa")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Created) != 0 || len(r2.Skipped) != 1 || r2.Skipped[0].Reason != service.BatchSkipDuplicate {
		t.Fatalf("external_id 重复应跳过，实得 created=%d skipped=%+v", len(r2.Created), r2.Skipped)
	}
	// 第 3 笔：external_id 不同但内容一致（软重复）→ 必须写入
	r3, err := e.txn.CreateBatch(ctx, []*service.CreateTxnInput{base("imp:bbb")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(r3.Created) != 1 || len(r3.Skipped) != 0 {
		t.Fatalf("内容重复（软重复）应照常写入，实得 created=%d skipped=%+v",
			len(r3.Created), r3.Skipped)
	}
}

// importCSV 上传 + 预览 + 提交（指定 freeze_mode），返回提交结果与批次号。
func (e *sheetEnv) importCSV(t *testing.T, name, body, freezeMode string) (*service.CommitResult, string) {
	t.Helper()
	p, err := e.sheets.Probe(context.Background(),
		[]service.ProbeFileInput{{FileName: name, Data: []byte(body)}})
	if err != nil {
		t.Fatalf("probe 失败: %v", err)
	}
	f := p.Files[0]
	if _, err = e.sheets.Preview(context.Background(), &service.PreviewInput{
		BatchID: p.BatchID, Defaults: service.PreviewDefaults{AccountID: e.account},
		Sheets: []service.SheetPlanInput{csvPlan(f.FileID)},
	}); err != nil {
		t.Fatalf("preview 失败: %v", err)
	}
	in := &service.CommitInput{
		BatchID: p.BatchID,
		Sheets:  []service.CommitSheetRef{{FileID: f.FileID, SheetIndex: 0}},
		RowKeys: []string{}, Defaults: service.CommitDefaults{AccountID: e.account},
		CreateMissingCategories: true, CreateMissingTags: true,
		SkipDuplicates: true, AllowFrozen: true, FreezeMode: freezeMode,
	}
	res, err := e.sheets.Commit(context.Background(), in)
	if err != nil {
		t.Fatalf("commit 失败: %v", err)
	}
	return res, p.BatchID
}

// TestImportFreezeDeferThenConfirm §9.10 T12 ⑤ + 需求核心：
// defer 模式下历史行「待确认可写」，余额立刻正确（钳制生效），确认后余额不变。
func TestImportFreezeDeferThenConfirm(t *testing.T) {
	e := newSheetEnv(t)
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	lastBefore, _ := e.bal.LastFrozenAt()

	body := "日期,分类,备注,金额\n" +
		"2020-01-01,餐饮,午饭,10.00\n" +
		"2020-01-01,交通,地铁,4.00\n" +
		"2020-01-02,餐饮,晚饭,20.00\n" +
		"2020-01-03,购物,日用品,6.50\n"
	res, batchID := e.importCSV(t, "old.csv", body, "") // 缺省 = defer

	if res.Created != 4 {
		t.Fatalf("created = %d，want 4", res.Created)
	}
	// 全部早于边界 → 全部待确认
	var pending int
	if err := e.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions
		WHERE freeze_confirmed = 0 AND import_batch_id = ?`, batchID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 4 {
		t.Fatalf("待确认行 = %d，want 4", pending)
	}
	if res.PendingFrozen != 4 {
		t.Errorf("commit 应返回 pending_frozen=4，实得 %d", res.PendingFrozen)
	}
	// I3（§9.3）：未确认状态下待确认行不得有任何物化值
	var stale int
	if err := e.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions
		WHERE freeze_confirmed = 0 AND account_balance_after IS NOT NULL`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Errorf("I3 违反：%d 条待确认行带物化值（未确认状态下必须为 0）", stale)
	}
	vr0, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr0.OK || vr0.StalePending != 0 || vr0.PendingRows != 4 {
		t.Errorf("未确认状态 verify 异常: ok=%v stale=%d pending=%d gaps=%d",
			vr0.OK, vr0.StalePending, vr0.PendingRows, len(vr0.MaterializationGaps))
	}
	// 导入即重算：minDeferred < last_frozen_at → 必须当场重算
	if !res.Rematerialized {
		t.Errorf("defer 导入落在已物化区时必须当场重算: %+v", res)
	}
	lastAfter, _ := e.bal.LastFrozenAt()
	if lastAfter >= lastBefore {
		t.Errorf("边界应被钳制回退：%s → %s",
			timeutil.FormatLocal(lastBefore), timeutil.FormatLocal(lastAfter))
	}
	// 余额 == 全表求和（待确认行不能丢）
	wantPer, wantTotal := fullTableSum(t, e.s)
	got, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != wantTotal {
		t.Errorf("导入后总余额 %d != 全表求和 %d", got.Total, wantTotal)
	}
	if got.ByAccount[e.account] != wantPer[e.account] {
		t.Errorf("导入后账户余额 %d != 全表求和 %d",
			got.ByAccount[e.account], wantPer[e.account])
	}

	// 待确认行可写
	var txID string
	if err = e.s.SQL.QueryRow(`SELECT id FROM transactions
		WHERE freeze_confirmed = 0 AND amount_cents = 1000 LIMIT 1`).Scan(&txID); err != nil {
		t.Fatal(err)
	}
	amount := int64(999)
	if _, err = e.txn.Update(ctx, txID, &service.UpdateTxnInput{AmountCents: &amount}); err != nil {
		t.Fatalf("待确认行应可改: %v", err)
	}
	// 改完余额立刻正确（动态区求和，无需重算）
	got2, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 支出行金额 1000 → 999，总余额变化 +1（少花 1 分）
	if got2.Total != wantTotal+1000-999 {
		t.Errorf("改金额后总余额 = %d，want %d", got2.Total, wantTotal+1000-999)
	}

	// 按批次确认
	fs := service.NewFreezeService(e.s, e.meta, e.txr, e.bal, e.mat)
	cres, err := fs.Confirm(ctx, &service.FreezeConfirmInput{BatchID: batchID})
	if err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	if cres.Confirmed != 4 || !cres.Materialized || cres.PendingRemaining != 0 {
		t.Errorf("确认结果异常: %+v", cres)
	}
	// 确认后余额必须与确认前完全一致（钳制方案不重不漏的核心断言）
	got3, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got3.Total != got2.Total {
		t.Errorf("确认前后总余额不一致: %d → %d", got2.Total, got3.Total)
	}
	// 确认后同一笔重新受保护
	if _, err = e.txn.Update(ctx, txID, &service.UpdateTxnInput{AmountCents: &amount}); err == nil {
		t.Error("确认后老行应重新受写保护")
	}
}

// TestImportFreezeImmediate T12 ④：immediate 与 v1.2 现行为一致（全部已冻结）。
func TestImportFreezeImmediate(t *testing.T) {
	e := newSheetEnv(t)
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	lastBefore, _ := e.bal.LastFrozenAt()
	body := "日期,分类,备注,金额\n" +
		"2020-01-01,餐饮,午饭,10.00\n" +
		"2020-01-02,交通,地铁,4.00\n"
	res, batchID := e.importCSV(t, "imm.csv", body, "immediate")
	if res.Created != 2 {
		t.Fatalf("created = %d，want 2", res.Created)
	}
	var pending, confirmed int
	if err := e.s.SQL.QueryRow(`SELECT
		SUM(CASE WHEN freeze_confirmed = 0 THEN 1 ELSE 0 END),
		SUM(CASE WHEN freeze_confirmed = 1 THEN 1 ELSE 0 END)
		FROM transactions WHERE import_batch_id = ?`, batchID).Scan(&pending, &confirmed); err != nil {
		t.Fatal(err)
	}
	if pending != 0 || confirmed != 2 {
		t.Errorf("immediate 应全部已冻结：pending=%d confirmed=%d", pending, confirmed)
	}
	lastAfter, _ := e.bal.LastFrozenAt()
	if lastAfter != lastBefore {
		t.Errorf("immediate 不应触发钳制回退：%s → %s",
			timeutil.FormatLocal(lastBefore), timeutil.FormatLocal(lastAfter))
	}
	if res.Rematerialized {
		t.Error("immediate 无待确认行，不应重算")
	}
	// 老行照旧受保护
	var txID string
	if err := e.s.SQL.QueryRow(`SELECT id FROM transactions WHERE import_batch_id = ? LIMIT 1`,
		batchID).Scan(&txID); err != nil {
		t.Fatal(err)
	}
	amount := int64(1)
	if _, err := e.txn.Update(ctx, txID, &service.UpdateTxnInput{AmountCents: &amount}); err == nil {
		t.Error("immediate 导入的老行应受写保护（409 FROZEN）")
	}
}
