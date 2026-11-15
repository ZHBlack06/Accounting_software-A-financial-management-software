package service_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/service"
	"ledger/internal/store"
)

// mergeEnv 账户合并测试环境（完整服务栈 + 备份服务）。
type mergeEnv struct {
	dir    string
	s      *store.Store
	meta   *store.MetaRepo
	master *store.MasterRepo
	txn    *service.TxnService
	bal    *service.BalanceService
	merge  *service.AccountMergeService
	seq    int // 账户 ID 计数器（ViewManager 只接受 ASCII 标识符）
}

func newMergeEnv(t *testing.T) *mergeEnv {
	t.Helper()
	dir, err := os.MkdirTemp("", "ledger-merge-")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "ledger.db")
	s, err := store.Open(dbPath)
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
	backup := service.NewBackupService(s, meta, bal, mat, dir, dbPath)
	merge := service.NewAccountMergeService(s, meta, master, views, bal, mat, backup, dir)
	t.Cleanup(func() {
		s.Close()
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return &mergeEnv{dir: dir, s: s, meta: meta, master: master, txn: txn, bal: bal, merge: merge}
}

// mkAccount 建一个账户（指定初始余额）。
func (e *mergeEnv) mkAccount(t *testing.T, name string, initial int64) *store.Account {
	t.Helper()
	now := time.Now().Unix()
	e.seq++
	acc := store.Account{ID: fmt.Sprintf("acc_t%d", e.seq), Name: name, AccountType: "asset", ValueType: "wallet",
		Currency: "CNY", InitialBalanceCents: initial, IsCountInTotal: 1,
		SortOrder: 50, CreatedAt: now, UpdatedAt: now}
	if err := e.s.G.Create(&acc).Error; err != nil {
		t.Fatal(err)
	}
	return &acc
}

// mkTxn 建一笔当天交易（非冻结区）。
func (e *mergeEnv) mkTxn(t *testing.T, in *service.CreateTxnInput) {
	t.Helper()
	now := time.Now().Unix()
	if in.TransactionTime == nil {
		in.TransactionTime = &now
	}
	if _, err := e.txn.Create(context.Background(), in); err != nil {
		t.Fatalf("建交易失败: %v", err)
	}
}

// totals 返回 (账户数, 交易数, 总余额)。
func (e *mergeEnv) totals(t *testing.T) (int, int64, int64) {
	t.Helper()
	var accCount, txCount int64
	if err := e.s.SQL.QueryRow(`SELECT COUNT(1) FROM accounts`).Scan(&accCount); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions`).Scan(&txCount); err != nil {
		t.Fatal(err)
	}
	b, err := e.bal.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return int(accCount), txCount, b.Total
}

// TestMergeMergesAccountsAndKeepsTotals 合并正常路径：搬交易 + 搬转账对手 + 初始余额求和，
// 交易笔数与总余额都不变（§3.5 验收）。
func TestMergeMergesAccountsAndKeepsTotals(t *testing.T) {
	e := newMergeEnv(t)
	a := e.mkAccount(t, "甲", 1000)
	b := e.mkAccount(t, "乙", 2000)
	e.mkTxn(t, &service.CreateTxnInput{TransactionType: "expense", AmountCents: 300,
		AccountID: a.ID, CategoryID: strp("cat_e01")})
	e.mkTxn(t, &service.CreateTxnInput{TransactionType: "income", AmountCents: 500,
		AccountID: b.ID, CategoryID: strp("cat_i01")})
	e.mkTxn(t, &service.CreateTxnInput{TransactionType: "transfer", AmountCents: 200,
		Transfer: &service.TransferInput{FromAccountID: a.ID, ToAccountID: b.ID}})

	accBefore, txBefore, totalBefore := e.totals(t)
	if accBefore != 3 || txBefore != 4 {
		t.Fatalf("前置状态异常: accounts=%d tx=%d", accBefore, txBefore)
	}

	res, err := e.merge.Merge(context.Background(), &service.MergeRequest{
		SourceIDs:     []string{a.ID, b.ID},
		Target:        service.MergeTarget{Name: store.TotalAccountName},
		DeleteSources: true, Confirm: true,
	})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if res.MergedAccounts != 2 {
		t.Errorf("merged_accounts = %d，want 2", res.MergedAccounts)
	}
	// 支出 1 + 收入 1 + 转账双边 2 = 4 笔交易行
	if res.MovedTransactions != 4 {
		t.Errorf("moved_transactions = %d，want 4", res.MovedTransactions)
	}
	if res.MovedTransferLegs != 2 {
		t.Errorf("moved_transfer_legs = %d，want 2（转账双边对手账户）", res.MovedTransferLegs)
	}
	if res.InitialBalanceCents != 3000 {
		t.Errorf("initial_balance_cents = %d，want 3000", res.InitialBalanceCents)
	}
	if res.CreatedTarget {
		t.Error("目标「总账户」已存在，不应新建")
	}
	if !res.Rematerialized {
		t.Error("合并后应完成全量重算")
	}

	accAfter, txAfter, totalAfter := e.totals(t)
	if accAfter != 1 {
		t.Errorf("合并后账户数 = %d，want 1", accAfter)
	}
	if txAfter != txBefore {
		t.Errorf("交易笔数变化 %d → %d（必须不变）", txBefore, txAfter)
	}
	if totalAfter != totalBefore {
		t.Errorf("总余额变化 %d → %d（必须不变）", totalBefore, totalAfter)
	}
	if totalAfter != 3200 {
		t.Errorf("总余额 = %d，want 3200（1000+2000-300+500）", totalAfter)
	}
	// 全部交易指向总账户、转账对手也改指
	var n int
	if err = e.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions WHERE account_id <> ?`,
		store.TotalAccountID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("仍有 %d 笔交易指向其他账户", n)
	}
	if err = e.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions
		WHERE counter_account_id IS NOT NULL AND counter_account_id <> ?`,
		store.TotalAccountID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("仍有 %d 笔转账对手指向其他账户", n)
	}
	// 校验器必须通过
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK {
		t.Errorf("合并后全量校验失败: %+v", vr.Mismatches)
	}
}

// TestMergeCustomTargetName 自定义目标名 → 新建账户。
func TestMergeCustomTargetName(t *testing.T) {
	e := newMergeEnv(t)
	a := e.mkAccount(t, "甲", 500)
	res, err := e.merge.Merge(context.Background(), &service.MergeRequest{
		SourceIDs:     []string{a.ID},
		Target:        service.MergeTarget{Name: "我的钱包", ValueType: "current"},
		DeleteSources: true, Confirm: true,
	})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if !res.CreatedTarget {
		t.Error("自定义目标名应新建账户")
	}
	acc, err := e.master.GetAccount(res.TargetAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Name != "我的钱包" || acc.ValueType != "current" {
		t.Errorf("目标账户 = %s/%s，want 我的钱包/current", acc.Name, acc.ValueType)
	}
	if acc.InitialBalanceCents != 500 {
		t.Errorf("目标初始余额 = %d，want 500", acc.InitialBalanceCents)
	}
}

// TestMergeEmptyTargetNameFallsBack 空目标名 → 回落「总账户」并复用已有账户。
func TestMergeEmptyTargetNameFallsBack(t *testing.T) {
	e := newMergeEnv(t)
	a := e.mkAccount(t, "甲", 100)
	res, err := e.merge.Merge(context.Background(), &service.MergeRequest{
		SourceIDs: []string{a.ID}, Target: service.MergeTarget{},
		DeleteSources: true, Confirm: true,
	})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if res.TargetAccountID != store.TotalAccountID {
		t.Errorf("目标账户 = %s，want %s", res.TargetAccountID, store.TotalAccountID)
	}
	if res.CreatedTarget {
		t.Error("「总账户」已由 Seed 创建，不应新建")
	}
	// 二次合并同一目标 → 仍复用（created_target=false）
	b := e.mkAccount(t, "乙", 100)
	res2, err := e.merge.Merge(context.Background(), &service.MergeRequest{
		SourceIDs: []string{b.ID}, Target: service.MergeTarget{Name: store.TotalAccountName},
		DeleteSources: true, Confirm: true,
	})
	if err != nil {
		t.Fatalf("二次合并失败: %v", err)
	}
	if res2.CreatedTarget || res2.TargetAccountID != store.TotalAccountID {
		t.Errorf("二次合并应复用总账户，实得 created=%v id=%s", res2.CreatedTarget, res2.TargetAccountID)
	}
}

// TestMergeRequiresConfirm 删除源账户必须 confirm=true（P2-1）。
func TestMergeRequiresConfirm(t *testing.T) {
	e := newMergeEnv(t)
	a := e.mkAccount(t, "甲", 0)
	_, err := e.merge.Merge(context.Background(), &service.MergeRequest{
		SourceIDs: []string{a.ID}, Target: service.MergeTarget{Name: store.TotalAccountName},
		DeleteSources: true, Confirm: false,
	})
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("未确认删除应报 VALIDATION_ERROR，实得 %v", err)
	}
	// 源账户必须还在
	if _, gerr := e.master.GetAccount(a.ID); gerr != nil {
		t.Fatalf("未确认时不应删除账户: %v", gerr)
	}
	// delete_sources=false 时无需 confirm
	if _, err = e.merge.Merge(context.Background(), &service.MergeRequest{
		SourceIDs: []string{a.ID}, Target: service.MergeTarget{Name: store.TotalAccountName},
		DeleteSources: false,
	}); err != nil {
		t.Fatalf("不删除源账户时不应要求 confirm: %v", err)
	}
}

// TestMergeRejectsBadSources 目标在源里 / 源不存在 → 400。
func TestMergeRejectsBadSources(t *testing.T) {
	e := newMergeEnv(t)
	a := e.mkAccount(t, "甲", 0)
	_, err := e.merge.Merge(context.Background(), &service.MergeRequest{
		SourceIDs:     []string{a.ID, store.TotalAccountID},
		Target:        service.MergeTarget{ID: store.TotalAccountID},
		DeleteSources: true, Confirm: true,
	})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("source 含目标应报 VALIDATION_ERROR，实得 %v", err)
	}
	_, err = e.merge.Merge(context.Background(), &service.MergeRequest{
		SourceIDs:     []string{"acc_not_exist"},
		Target:        service.MergeTarget{Name: store.TotalAccountName},
		DeleteSources: true, Confirm: true,
	})
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("源账户不存在应报 VALIDATION_ERROR，实得 %v", err)
	}
	if _, has := ae.Details["missing"]; !has {
		t.Error("错误 details 应含 missing 列表")
	}
	// 空 source_ids
	if _, err = e.merge.Merge(context.Background(), &service.MergeRequest{}); err == nil {
		t.Error("空 source_ids 应报错")
	}
}

// TestAutoMergeOnUpgrade 首次启动自动迁移：备份 + 合并 + 日志 + 幂等标记。
func TestAutoMergeOnUpgrade(t *testing.T) {
	e := newMergeEnv(t)
	a := e.mkAccount(t, "招商", 1000)
	_ = e.mkAccount(t, "微信", 2000)
	e.mkTxn(t, &service.CreateTxnInput{TransactionType: "expense", AmountCents: 100,
		AccountID: a.ID, CategoryID: strp("cat_e01")})

	accBefore, txBefore, totalBefore := e.totals(t)
	if accBefore != 3 {
		t.Fatalf("前置账户数 = %d，want 3", accBefore)
	}
	if err := e.merge.AutoMergeOnUpgrade(context.Background()); err != nil {
		t.Fatalf("自动合并失败: %v", err)
	}
	accAfter, txAfter, totalAfter := e.totals(t)
	if accAfter != 1 {
		t.Errorf("自动合并后账户数 = %d，want 1", accAfter)
	}
	if txAfter != txBefore || totalAfter != totalBefore {
		t.Errorf("笔数/余额变化: %d→%d, %d→%d", txBefore, txAfter, totalBefore, totalAfter)
	}
	// 幂等标记 + 备份 + 追溯日志
	v, err := e.meta.Get("account_merge_v12_done")
	if err != nil || v != "1" {
		t.Errorf("account_merge_v12_done = %q（want 1），err=%v", v, err)
	}
	matches, _ := filepath.Glob(filepath.Join(e.dir, "backups", "*", "*.zip"))
	if len(matches) == 0 {
		t.Error("迁移前必须已生成备份 zip")
	}
	logPath := filepath.Join(e.dir, "logs", "v12-account-merge.log")
	raw, rerr := os.ReadFile(logPath)
	if rerr != nil {
		t.Fatalf("读迁移日志失败: %v", rerr)
	}
	if !contains(string(raw), "已合并") || !contains(string(raw), "合并前余额") {
		t.Errorf("迁移日志内容不完整: %s", string(raw))
	}

	// 二次调用 → 幂等（不再合并、不再建账户）
	if err = e.merge.AutoMergeOnUpgrade(context.Background()); err != nil {
		t.Fatalf("二次自动合并失败: %v", err)
	}
	acc2, tx2, total2 := e.totals(t)
	if acc2 != 1 || tx2 != txAfter || total2 != totalAfter {
		t.Errorf("二次调用改变了数据: acc %d→%d tx %d→%d", accAfter, acc2, txAfter, tx2)
	}
	// 单账户库：直接置位（覆盖 <=1 分支）
	if v, _ = e.meta.Get("account_merge_v12_done"); v != "1" {
		t.Errorf("标记 = %q", v)
	}
}

// TestEnsureGlobalSeqSelfHeal seq:global 落后时自动抬到 max(created_seq)+1（P2-5）。
func TestEnsureGlobalSeqSelfHeal(t *testing.T) {
	e := newMergeEnv(t)
	acc := e.mkAccount(t, "甲", 0)
	for i := 0; i < 3; i++ {
		e.mkTxn(t, &service.CreateTxnInput{TransactionType: "expense", AmountCents: int64(100 + i),
			AccountID: acc.ID, CategoryID: strp("cat_e01")})
	}
	var maxSeq int64
	if err := e.s.SQL.QueryRow(`SELECT MAX(created_seq) FROM transactions`).Scan(&maxSeq); err != nil {
		t.Fatal(err)
	}
	// 人为把计数器打回 1（模拟恢复旧库/外部改库）
	if err := e.meta.Set("seq:global", "1"); err != nil {
		t.Fatal(err)
	}
	fixed, from, to, err := e.s.EnsureGlobalSeq()
	if err != nil {
		t.Fatalf("自愈失败: %v", err)
	}
	if !fixed || from != 1 || to != maxSeq+1 {
		t.Errorf("自愈结果 = (%v, %d→%d)，want (true, 1→%d)", fixed, from, to, maxSeq+1)
	}
	// 自愈后新建交易必须成功且 seq 连续
	e.mkTxn(t, &service.CreateTxnInput{TransactionType: "expense", AmountCents: 999,
		AccountID: acc.ID, CategoryID: strp("cat_e01")})
	var newMax int64
	if err := e.s.SQL.QueryRow(`SELECT MAX(created_seq) FROM transactions`).Scan(&newMax); err != nil {
		t.Fatal(err)
	}
	if newMax != maxSeq+1 {
		t.Errorf("新交易 created_seq = %d，want %d", newMax, maxSeq+1)
	}
	// 已是最大值时不再修正
	fixed2, _, _, err := e.s.EnsureGlobalSeq()
	if err != nil || fixed2 {
		t.Errorf("正常情况下不应再修正: fixed=%v err=%v", fixed2, err)
	}
}

// strp 字符串指针。
func strp(s string) *string { return &s }

// contains 子串判断（避免额外 import）。
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
