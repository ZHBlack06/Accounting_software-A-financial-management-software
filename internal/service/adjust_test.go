package service_test

import (
	"context"
	"testing"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/service"
)

// seedFrozenExpense 造一笔 40 天前的支出（冻结区）并物化。
func seedFrozenExpense(t *testing.T, e *testEnv, amount int64) string {
	t.Helper()
	e.setInitial(t)
	res := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: amount, AccountID: "acc_cmb0001",
		CategoryID: catRef("cat_e01"), Remark: "咖啡",
	})
	past := time.Now().AddDate(0, 0, -40).Unix()
	if err := e.txr.SetTransactionTimeForTest(res.Primary.ID, past); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mat.Materialize(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	return res.Primary.ID
}

// TestAdjust T05 验收 1：多记支出 → 冲减生成 1 笔 income 调整，账户 +9000。
func TestAdjust(t *testing.T) {
	e := newTestEnv(t)
	id := seedFrozenExpense(t, e, 10000)
	before := e.mustBalance(t)

	res, err := e.adjust.Adjust(context.Background(), id, &service.AdjustInput{
		Mode: "adjust", NewAmountCents: 1000, Remark: "冲减多记90元",
	})
	if err != nil {
		t.Fatalf("adjust 失败: %v", err)
	}
	if res.DeltaCents != -9000 {
		t.Fatalf("delta_cents = %d, want -9000", res.DeltaCents)
	}
	if len(res.Created) != 1 {
		t.Fatalf("adjust 应生成 1 笔, got %d", len(res.Created))
	}
	tx := res.Created[0]
	if tx.TransactionType != "income" || tx.AmountCents != 9000 {
		t.Fatalf("调整行错误: type=%s amount=%d", tx.TransactionType, tx.AmountCents)
	}
	if tx.IsAdjustment != 1 || tx.AdjustmentType == nil || *tx.AdjustmentType != "adjust" {
		t.Fatal("调整标记错误")
	}
	if tx.RelatedTransactionID == nil || *tx.RelatedTransactionID != id {
		t.Fatal("related_transaction_id 错误")
	}
	if tx.TransactionTime < time.Now().Unix()-60 {
		t.Fatal("调整时间应强制为 now")
	}
	after := e.mustBalance(t)
	if got := after.ByAccount["acc_cmb0001"] - before.ByAccount["acc_cmb0001"]; got != 9000 {
		t.Fatalf("账户余额变化 = %d, want +9000", got)
	}
}

// TestReverse T05 验收 2：反冲重录生成 2 笔，净 +9000。
func TestReverse(t *testing.T) {
	e := newTestEnv(t)
	id := seedFrozenExpense(t, e, 10000)
	before := e.mustBalance(t)

	res, err := e.adjust.Adjust(context.Background(), id, &service.AdjustInput{
		Mode: "reverse", NewAmountCents: 1000, Remark: "重录正确金额",
	})
	if err != nil {
		t.Fatalf("reverse 失败: %v", err)
	}
	if len(res.Created) != 2 {
		t.Fatalf("reverse 应生成 2 笔, got %d", len(res.Created))
	}
	revert, recast := res.Created[0], res.Created[1]
	if revert.TransactionType != "income" || revert.AmountCents != 10000 {
		t.Fatalf("冲销行错误: %s %d", revert.TransactionType, revert.AmountCents)
	}
	if recast.TransactionType != "expense" || recast.AmountCents != 1000 {
		t.Fatalf("重录行错误: %s %d", recast.TransactionType, recast.AmountCents)
	}
	for _, tx := range res.Created {
		if tx.IsAdjustment != 1 || tx.AdjustmentType == nil || *tx.AdjustmentType != "reverse" {
			t.Fatal("两笔都应标 adjustment_type=reverse")
		}
	}
	after := e.mustBalance(t)
	if got := after.ByAccount["acc_cmb0001"] - before.ByAccount["acc_cmb0001"]; got != 9000 {
		t.Fatalf("净影响 = %d, want +9000", got)
	}
}

// TestAdjustTransfer T05 验收 3：转账双边调整净 0，acc1 +2000 / acc2 -2000。
func TestAdjustTransfer(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	res := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "transfer", AmountCents: 10000,
		Transfer: &service.TransferInput{FromAccountID: "acc_cmb0001", ToAccountID: "acc_ali0001"},
	})
	past := time.Now().AddDate(0, 0, -40).Unix()
	if err := e.txr.SetTransactionTimeForTest(res.Primary.ID, past); err != nil {
		t.Fatal(err)
	}
	if err := e.txr.SetTransactionTimeForTest(res.Linked[0].ID, past); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mat.Materialize(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	before := e.mustBalance(t)

	out, err := e.adjust.Adjust(context.Background(), res.Primary.ID, &service.AdjustInput{
		Mode: "adjust", NewAmountCents: 8000, Remark: "转账金额记错",
	})
	if err != nil {
		t.Fatalf("转账 adjust 失败: %v", err)
	}
	if len(out.Created) != 2 {
		t.Fatalf("转账调整应生成 2 笔, got %d", len(out.Created))
	}
	var sum int64
	for _, tx := range out.Created {
		sum += tx.SignedCents()
	}
	if sum != 0 {
		t.Fatalf("转账调整 signed 合计 = %d, want 0", sum)
	}
	after := e.mustBalance(t)
	if got := after.ByAccount["acc_cmb0001"] - before.ByAccount["acc_cmb0001"]; got != 2000 {
		t.Fatalf("acc1 变化 = %d, want +2000", got)
	}
	if got := after.ByAccount["acc_ali0001"] - before.ByAccount["acc_ali0001"]; got != -2000 {
		t.Fatalf("acc2 变化 = %d, want -2000", got)
	}
}

// TestAdjustValidation T05 验收 4、5。
func TestAdjustValidation(t *testing.T) {
	e := newTestEnv(t)
	id := seedFrozenExpense(t, e, 10000)

	// remark 为空
	_, err := e.adjust.Adjust(context.Background(), id, &service.AdjustInput{
		Mode: "adjust", NewAmountCents: 1000})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("remark 为空应 400 VALIDATION_ERROR, got %v", err)
	}
	// 金额未变
	_, err = e.adjust.Adjust(context.Background(), id, &service.AdjustInput{
		Mode: "adjust", NewAmountCents: 10000, Remark: "x"})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNoEffect {
		t.Fatalf("金额未变应 400 NO_EFFECT, got %v", err)
	}
	// 生成一笔调整后再调整 → 禁止链式
	res, err := e.adjust.Adjust(context.Background(), id, &service.AdjustInput{
		Mode: "adjust", NewAmountCents: 1000, Remark: "首次调整"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.adjust.Adjust(context.Background(), res.Created[0].ID, &service.AdjustInput{
		Mode: "adjust", NewAmountCents: 500, Remark: "再调整"})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("链式调整应 400 VALIDATION_ERROR, got %v", err)
	}
}

// TestVerifyDetectsCorruption T05 验收 8：人为改错物化字段 → verify ok:false + mismatches。
func TestVerifyDetectsCorruption(t *testing.T) {
	e := newTestEnv(t)
	seedFrozenExpense(t, e, 10000)
	if vr, err := e.bal.Verify(); err != nil || !vr.OK {
		t.Fatalf("初始 verify 应通过: %+v %v", vr, err)
	}
	if _, err := e.s.SQL.Exec(`UPDATE transactions SET account_balance_after = account_balance_after + 1
		WHERE account_balance_after IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if vr.OK || len(vr.Mismatches) == 0 {
		t.Fatalf("应检测到不一致: %+v", vr)
	}
}
