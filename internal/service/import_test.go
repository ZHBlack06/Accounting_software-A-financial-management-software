package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
	"ledger/internal/store"
)

// importSvc 构造导入服务（批次文件落在临时目录）。
func (e *testEnv) importSvc(t *testing.T) *service.ImportService {
	t.Helper()
	return service.NewImportService(e.s, store.NewMetaRepo(e.s), e.mast, e.txn, e.bal, t.TempDir())
}

// wechatCSVData 生成微信账单：BOM + 说明行 + 表头 + 4 行数据（2 近期、1 中性、1 冻结区）。
func wechatCSVData(frozenDay int) []byte {
	loc := timeutil.Local()
	day := func(offset int) string {
		return time.Now().In(loc).AddDate(0, 0, offset).Format("2006-01-02 15:04:05")
	}
	csv := "\ufeff微信支付账单明细\n" +
		"微信昵称：[测试]\n" +
		"起始时间：[2026-09-01 00:00:00] 终止时间：[2026-10-01 00:00:00]\n" +
		"导出类型：[全部]\n" +
		"交易时间,交易类型,交易对方,商品,收/支,金额(元),支付方式,当前状态,交易单号,商户单号,备注\n" +
		fmt.Sprintf("%s,商户消费,星巴克,咖啡,支出,¥33.00,零钱,支付成功,imp_t1,M001,/\n", day(-2)) +
		fmt.Sprintf("%s,商户消费,公司,报销,收入,100.00,零钱,已存入零钱,imp_t2,M002,/\n", day(-1)) +
		fmt.Sprintf("%s,零钱提现,招商银行,提现,/,1000.00,零钱,提现已到账,imp_t3,M003,/\n", day(-1)) +
		fmt.Sprintf("%s,商户消费,某餐厅,晚餐,支出,50.00,零钱,支付成功,imp_t4,M004,/\n", day(frozenDay))
	return []byte(csv)
}

// TestImportPreview 预览：格式识别、跳过行、冻结标记、去重标记。
func TestImportPreview(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	svc := e.importSvc(t)

	pv, err := svc.Preview(context.Background(), wechatCSVData(-40), "acc_cmb0001", "auto")
	if err != nil {
		t.Fatalf("Preview 失败: %v", err)
	}
	if pv.Source != "wechat" || pv.Encoding != "utf-8" {
		t.Fatalf("source/encoding = %s/%s", pv.Source, pv.Encoding)
	}
	if pv.BatchID == "" {
		t.Fatal("batch_id 不能为空")
	}
	if pv.Summary.TotalRows != 4 || pv.Summary.ParsedOK != 3 || pv.Summary.Skipped != 1 {
		t.Fatalf("summary 错误: %+v", pv.Summary)
	}
	if pv.Summary.FrozenCount != 1 {
		t.Fatalf("frozen_count = %d, want 1（40 天前落在冻结区）", pv.Summary.FrozenCount)
	}
	for _, row := range pv.Rows {
		if row.Parsed != nil && row.Parsed.ExternalID == "" {
			t.Fatalf("第 %d 行应生成幂等指纹", row.RowNo)
		}
		if row.Parsed != nil && len(row.NeedConfirm) == 0 {
			t.Fatalf("第 %d 行应提示需确认分类", row.RowNo)
		}
		if row.Parsed == nil && row.SkipReason == "" {
			t.Fatalf("第 %d 行跳过但缺少 skip_reason", row.RowNo)
		}
	}
	// 来源提示与实际不符 → 422
	_, err = svc.Preview(context.Background(), wechatCSVData(-40), "acc_cmb0001", "alipay")
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeUnsupportedCSV {
		t.Fatalf("来源不符应返回 UNSUPPORTED_CSV, got %v", err)
	}
}

// TestImportCommit 提交：创建 2 笔、余额正确；重复导入被去重。
func TestImportCommit(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	ctx := context.Background()
	svc := e.importSvc(t)

	pv, err := svc.Preview(ctx, wechatCSVData(-40), "acc_cmb0001", "auto")
	if err != nil {
		t.Fatal(err)
	}
	// 选中两个近期行 + 冻结行 → 未勾选 allow_frozen 时整体拒绝
	allRows := []int{}
	for _, r := range pv.Rows {
		if r.Parsed != nil {
			allRows = append(allRows, r.RowNo)
		}
	}
	_, err = svc.Commit(ctx, &service.ImportCommitInput{
		BatchID: pv.BatchID, RowNos: allRows,
		Defaults: service.ImportCommitDefaults{AccountID: "acc_cmb0001"},
	})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeFrozen {
		t.Fatalf("含冻结行提交应返回 FROZEN, got %v", err)
	}

	// 只提交两个近期行
	recent := []int{}
	for _, r := range pv.Rows {
		if r.Parsed != nil && !r.Frozen {
			recent = append(recent, r.RowNo)
		}
	}
	if len(recent) != 2 {
		t.Fatalf("近期行数 = %d, want 2", len(recent))
	}
	res, err := svc.Commit(ctx, &service.ImportCommitInput{
		BatchID: pv.BatchID, RowNos: recent,
		Defaults: service.ImportCommitDefaults{AccountID: "acc_cmb0001"},
	})
	if err != nil {
		t.Fatalf("Commit 失败: %v", err)
	}
	if res.Created != 2 || len(res.TransactionIDs) != 2 {
		t.Fatalf("created = %d, want 2", res.Created)
	}
	b := e.mustBalance(t)
	if b.ByAccount["acc_cmb0001"] != 106700 {
		t.Fatalf("导入后 acc1 = %d, want 106700（100000-3300+10000）", b.ByAccount["acc_cmb0001"])
	}
	// 总余额含另外两个种子账户（100000+50000+0），导入只影响 acc1
	if b.Total != 156700 {
		t.Fatalf("导入后 total = %d, want 156700（150000-3300+10000）", b.Total)
	}
	vr, err := e.bal.Verify()
	if err != nil || !vr.OK {
		t.Fatalf("导入后 verify 失败: %+v %v", vr, err)
	}

	// 重复导入同一文件 → 预览标重、提交跳过
	pv2, err := svc.Preview(ctx, wechatCSVData(-40), "acc_cmb0001", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if pv2.Summary.DupCount != 2 {
		t.Fatalf("重导 dup_count = %d, want 2", pv2.Summary.DupCount)
	}
	recent2 := []int{}
	for _, r := range pv2.Rows {
		if r.Parsed != nil && !r.Frozen {
			recent2 = append(recent2, r.RowNo)
		}
	}
	res2, err := svc.Commit(ctx, &service.ImportCommitInput{
		BatchID: pv2.BatchID, RowNos: recent2, SkipDuplicates: true,
		Defaults: service.ImportCommitDefaults{AccountID: "acc_cmb0001"},
	})
	if err != nil {
		t.Fatalf("重复提交失败: %v", err)
	}
	if res2.Created != 0 || res2.SkippedDup != 2 {
		t.Fatalf("重复提交 created/skipped_dup = %d/%d, want 0/2", res2.Created, res2.SkippedDup)
	}
	if got := e.mustBalance(t).Total; got != 156700 {
		t.Fatalf("重复提交后 total = %d, want 156700（余额不得翻倍）", got)
	}

	// 不存在的批次 → IMPORT_BATCH_EXPIRED
	_, err = svc.Commit(ctx, &service.ImportCommitInput{
		BatchID: "imp_not_exist", Defaults: service.ImportCommitDefaults{AccountID: "acc_cmb0001"},
	})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeImportBatchExpired {
		t.Fatalf("不存在批次应返回 IMPORT_BATCH_EXPIRED, got %v", err)
	}
}

// TestImportCommitValidation 提交参数校验。
func TestImportCommitValidation(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	ctx := context.Background()
	svc := e.importSvc(t)
	pv, err := svc.Preview(ctx, wechatCSVData(-40), "acc_cmb0001", "auto")
	if err != nil {
		t.Fatal(err)
	}

	// 缺 batch_id
	if _, err = svc.Commit(ctx, &service.ImportCommitInput{}); err == nil {
		t.Fatal("缺 batch_id 应报错")
	}
	// 账户不存在
	_, err = svc.Commit(ctx, &service.ImportCommitInput{
		BatchID:  pv.BatchID,
		Defaults: service.ImportCommitDefaults{AccountID: "acc_not_exist"},
	})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("账户不存在应返回 VALIDATION_ERROR, got %v", err)
	}
	// 选中行号全部无效（只有跳过行）
	_, err = svc.Commit(ctx, &service.ImportCommitInput{
		BatchID: pv.BatchID, RowNos: []int{},
		Defaults: service.ImportCommitDefaults{AccountID: "acc_cmb0001"},
	})
	// RowNos 为空表示全选（含冻结行）→ FROZEN
	if ae, ok := err.(*apierr.Error); !ok || (ae.Code != apierr.CodeFrozen && ae.Code != apierr.CodeValidation) {
		t.Fatalf("全选含冻结行应被拒绝, got %v", err)
	}
}
