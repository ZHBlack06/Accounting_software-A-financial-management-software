package service_test

import (
	"context"
	"testing"

	"ledger/internal/pkg/apierr"
	"ledger/internal/service"
)

// TestSoftDeleteRestoreBalance 软删 → 总余额增加 → 恢复 → 还原。
func TestSoftDeleteRestoreBalance(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	res := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001",
		CategoryID: catRef("cat_e01"), Remark: "软删测试",
	})
	if got := e.mustBalance(t).Total; got != 147000 {
		t.Fatalf("初始 total = %d, want 147000", got)
	}

	ctx := context.Background()
	if err := e.txn.SoftDelete(ctx, res.Primary.ID, true); err != nil {
		t.Fatalf("软删失败: %v", err)
	}
	b := e.mustBalance(t)
	if b.Total != 150000 {
		t.Fatalf("软删后 total = %d, want 150000", b.Total)
	}
	if b.ByAccount["acc_cmb0001"] != 100000 {
		t.Fatalf("软删后 acc1 = %d, want 100000", b.ByAccount["acc_cmb0001"])
	}

	// 幂等：重复软删不报错
	if err := e.txn.SoftDelete(ctx, res.Primary.ID, true); err != nil {
		t.Fatalf("重复软删应幂等: %v", err)
	}

	if err := e.txn.Restore(ctx, res.Primary.ID); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	b = e.mustBalance(t)
	if b.Total != 147000 {
		t.Fatalf("恢复后 total = %d, want 147000", b.Total)
	}
	if err := e.txn.Restore(ctx, res.Primary.ID); err != nil {
		t.Fatalf("重复恢复应幂等: %v", err)
	}

	// 恢复后仍可校验一致
	vr, err := e.bal.Verify()
	if err != nil || !vr.OK {
		t.Fatalf("恢复后 verify 失败: %+v %v", vr, err)
	}
}

// TestTransferPairDeleteRestore 转账成对删除/恢复；单边删除会破坏总额（对照组）。
func TestTransferPairDeleteRestore(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	tr := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "transfer", AmountCents: 20000,
		Transfer: &service.TransferInput{FromAccountID: "acc_cmb0001", ToAccountID: "acc_ali0001"},
	})
	ctx := context.Background()

	// 成对删除：总额不变
	if err := e.txn.SoftDelete(ctx, tr.Primary.ID, true); err != nil {
		t.Fatalf("成对软删失败: %v", err)
	}
	if got := e.mustBalance(t).Total; got != 150000 {
		t.Fatalf("成对软删后 total = %d, want 150000（转账不影响总额）", got)
	}
	if err := e.txn.Restore(ctx, tr.Primary.ID); err != nil {
		t.Fatalf("成对恢复失败: %v", err)
	}
	if got := e.mustBalance(t).Total; got != 150000 {
		t.Fatalf("成对恢复后 total = %d, want 150000", got)
	}

	// 单边删除（withTransfer=false）：只有转出行失效 → 总额 +20000
	if err := e.txn.SoftDelete(ctx, tr.Primary.ID, false); err != nil {
		t.Fatalf("单边软删失败: %v", err)
	}
	b := e.mustBalance(t)
	if b.Total != 170000 {
		t.Fatalf("单边软删后 total = %d, want 170000（仅冲销转出）", b.Total)
	}
	if b.ByAccount["acc_cmb0001"] != 100000 {
		t.Fatalf("转出行失效后 acc1 = %d, want 100000", b.ByAccount["acc_cmb0001"])
	}
	// 恢复单边
	if err := e.txn.Restore(ctx, tr.Primary.ID); err != nil {
		t.Fatalf("单边恢复失败: %v", err)
	}
	if got := e.mustBalance(t).Total; got != 150000 {
		t.Fatalf("单边恢复后 total = %d, want 150000", got)
	}
}

// TestPurgeRetention 180 天保留期内禁止彻底删除。
func TestPurgeRetention(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	res := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 1000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	ctx := context.Background()

	// 未软删 → 400
	if err := e.txn.Purge(ctx, res.Primary.ID); err == nil {
		t.Fatal("未软删应禁止彻底删除")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("未软删应返回 VALIDATION_ERROR, got %v", err)
	}

	if err := e.txn.SoftDelete(ctx, res.Primary.ID, true); err != nil {
		t.Fatal(err)
	}
	err := e.txn.Purge(ctx, res.Primary.ID)
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeRetention {
		t.Fatalf("保留期内应返回 RETENTION_ERROR, got %v", err)
	}
	if ae.Details["purge_allowed"] == nil || ae.Details["deleted_at"] == nil {
		t.Fatalf("RETENTION details 不完整: %+v", ae.Details)
	}
}

// TestUpdateDynamicTxn 动态区改金额：余额与窗口快照同步更新。
func TestUpdateDynamicTxn(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	res := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	ctx := context.Background()

	amount := int64(5000)
	upd, err := e.txn.Update(ctx, res.Primary.ID, &service.UpdateTxnInput{AmountCents: &amount})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if upd.AmountCents != 5000 {
		t.Fatalf("amount = %d, want 5000", upd.AmountCents)
	}
	b := e.mustBalance(t)
	if b.ByAccount["acc_cmb0001"] != 95000 || b.Total != 145000 {
		t.Fatalf("改额后余额错误: acc1=%d total=%d", b.ByAccount["acc_cmb0001"], b.Total)
	}
	window, _, err := e.bal.WindowBalances()
	if err != nil {
		t.Fatal(err)
	}
	if wb, ok := window[res.Primary.ID]; !ok || wb.AccountBalanceAfter != 95000 {
		t.Fatalf("窗口余额未同步: %+v", wb)
	}

	// 非法金额
	bad := int64(0)
	if _, err = e.txn.Update(ctx, res.Primary.ID, &service.UpdateTxnInput{AmountCents: &bad}); err == nil {
		t.Fatal("金额 0 应报错")
	}
}

// TestUpdateTransferImmutable 转账行禁止改金额（TRANSFER_IMMUTABLE）。
func TestUpdateTransferImmutable(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	tr := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "transfer", AmountCents: 20000,
		Transfer: &service.TransferInput{FromAccountID: "acc_cmb0001", ToAccountID: "acc_ali0001"},
	})
	amount := int64(30000)
	_, err := e.txn.Update(context.Background(), tr.Primary.ID, &service.UpdateTxnInput{AmountCents: &amount})
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeTransferImmutable {
		t.Fatalf("转账改金额应返回 TRANSFER_IMMUTABLE, got %v", err)
	}
	// 备注等非金额字段允许修改
	remark := "转账备注"
	if _, err = e.txn.Update(context.Background(), tr.Primary.ID, &service.UpdateTxnInput{Remark: &remark}); err != nil {
		t.Fatalf("转账改备注应允许: %v", err)
	}
}

// TestGetMissing 查询不存在的交易 → 404 NOT_FOUND。
func TestGetMissing(t *testing.T) {
	e := newTestEnv(t)
	_, err := e.txn.Get("txn_not_exist")
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("不存在交易应返回 NOT_FOUND, got %v", err)
	}
}
