package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/service"
	"ledger/internal/store"
)

// testEnv 测试环境（临时目录 + 完整服务栈）。
type testEnv struct {
	dir    string
	s      *store.Store
	txn    *service.TxnService
	bal    *service.BalanceService
	mat    *service.Materializer
	txr    *store.TxRepo
	mast   *store.MasterRepo
	meta   *store.MetaRepo
	adjust *service.AdjustService
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	// Windows 下 SQLite 文件锁释放可能有延迟，自管目录 + 重试删除（t.TempDir 的清理失败会判测试失败）
	dir, err := os.MkdirTemp("", "ledger-test-")
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
	// v1.2：Seed 只种一个「总账户」，但 v1.1 的存量用例按三个账户断言余额
	// （acc_cmb0001/acc_ali0001/acc_wx00001 + 转账双边），这里补建以保持既有用例不改语义。
	ensureLegacyTestAccounts(t, s)
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
	adjust := service.NewAdjustService(s, meta, txr)
	t.Cleanup(func() {
		s.Close()
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return &testEnv{dir: dir, s: s, txn: txn, bal: bal, mat: mat, txr: txr, mast: master, meta: meta, adjust: adjust}
}

// ensureLegacyTestAccounts 补建 v1.1 三账户（仅供存量用例使用；生产库只有「总账户」）。
func ensureLegacyTestAccounts(t *testing.T, s *store.Store) {
	t.Helper()
	now := time.Now().Unix()
	for i, a := range []struct{ id, name, vt string }{
		{"acc_cmb0001", "招商银行借记卡1", "current"},
		{"acc_ali0001", "支付宝余额宝", "yuebao"},
		{"acc_wx00001", "微信零钱", "wallet"},
	} {
		var n int64
		if err := s.G.Model(&store.Account{}).Where("id = ?", a.id).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			continue
		}
		acc := store.Account{ID: a.id, Name: a.name, AccountType: "asset", ValueType: a.vt,
			Currency: "CNY", IsCountInTotal: 1, SortOrder: (i + 2) * 10,
			CreatedAt: now, UpdatedAt: now}
		if err := s.G.Create(&acc).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// setInitial 设置三个种子账户初始余额 100000/50000/0。
func (e *testEnv) setInitial(t *testing.T) {
	t.Helper()
	for id, v := range map[string]int64{"acc_cmb0001": 100000, "acc_ali0001": 50000, "acc_wx00001": 0} {
		if err := e.s.G.Model(&store.Account{}).Where("id = ?", id).
			Update("initial_balance_cents", v).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func catRef(s string) *string { return &s }

func (e *testEnv) mustCreate(t *testing.T, in *service.CreateTxnInput) *service.TxnWriteResult {
	t.Helper()
	res, err := e.txn.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("创建交易失败: %v", err)
	}
	return res
}

func (e *testEnv) mustBalance(t *testing.T) *service.Balances {
	t.Helper()
	b, err := e.bal.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestBalanceBaseline T04 验收 1：余额基线。
func TestBalanceBaseline(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)

	b := e.mustBalance(t)
	if b.Total != 150000 {
		t.Fatalf("初始 total = %d, want 150000", b.Total)
	}

	// expense 3000（acc1）→ acc1=97000、total=147000
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001",
		CategoryID: catRef("cat_e01"),
	})
	b = e.mustBalance(t)
	if got := b.ByAccount["acc_cmb0001"]; got != 97000 {
		t.Fatalf("acc1 = %d, want 97000", got)
	}
	if b.Total != 147000 {
		t.Fatalf("total = %d, want 147000", b.Total)
	}

	// income 5000（acc2）→ acc2=55000、total=152000
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "income", AmountCents: 5000, AccountID: "acc_ali0001",
		CategoryID: catRef("cat_i01"),
	})
	b = e.mustBalance(t)
	if got := b.ByAccount["acc_ali0001"]; got != 55000 {
		t.Fatalf("acc2 = %d, want 55000", got)
	}
	if b.Total != 152000 {
		t.Fatalf("total = %d, want 152000", b.Total)
	}
}

// TestTransfer T04 验收 2：内部转账总额不变、成对、signed 相加为 0。
func TestTransfer(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "income", AmountCents: 5000, AccountID: "acc_ali0001", CategoryID: catRef("cat_i01"),
	})
	before := e.mustBalance(t)

	res := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "transfer", AmountCents: 20000,
		Transfer: &service.TransferInput{FromAccountID: "acc_cmb0001", ToAccountID: "acc_ali0001"},
	})
	if len(res.Linked) != 1 {
		t.Fatalf("转账应生成 1 条关联行, got %d", len(res.Linked))
	}
	if *res.Primary.TransferGroupID != *res.Linked[0].TransferGroupID {
		t.Fatal("两行 transfer_group_id 不一致")
	}
	if res.Primary.SignedCents()+res.Linked[0].SignedCents() != 0 {
		t.Fatal("转账双边 signed_cents 相加应为 0")
	}

	after := e.mustBalance(t)
	if got := after.ByAccount["acc_cmb0001"]; got != 77000 {
		t.Fatalf("acc1 = %d, want 77000", got)
	}
	if got := after.ByAccount["acc_ali0001"]; got != 75000 {
		t.Fatalf("acc2 = %d, want 75000", got)
	}
	if after.Total != before.Total {
		t.Fatalf("转账后 total = %d, 应保持 %d 不变", after.Total, before.Total)
	}
}

// setupThreeTx 造验收 3 的数据（expense/income/transfer 共 4 行）并移到 40 天前。
func setupThreeTx(t *testing.T, e *testEnv) []string {
	t.Helper()
	e.setInitial(t)
	r1 := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	r2 := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "income", AmountCents: 5000, AccountID: "acc_ali0001", CategoryID: catRef("cat_i01"),
	})
	r3 := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "transfer", AmountCents: 20000,
		Transfer: &service.TransferInput{FromAccountID: "acc_cmb0001", ToAccountID: "acc_ali0001"},
	})
	past := time.Now().AddDate(0, 0, -40).Unix()
	ids := []string{r1.Primary.ID, r2.Primary.ID, r3.Primary.ID, r3.Linked[0].ID}
	for _, id := range ids {
		if err := e.txr.SetTransactionTimeForTest(id, past); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

// TestMaterialize T04 验收 3：物化后逐笔余额正确、边界推进、校验通过。
func TestMaterialize(t *testing.T) {
	e := newTestEnv(t)
	ids := setupThreeTx(t, e)

	res, err := e.mat.Materialize(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Materialize 失败: %v", err)
	}
	if res.RowsProcessed != 4 {
		t.Fatalf("物化行数 = %d, want 4", res.RowsProcessed)
	}

	// 边界推进到 now-30d
	last, err := e.bal.LastFrozenAt()
	if err != nil {
		t.Fatal(err)
	}
	want := time.Now().Unix() - 30*86400
	if diff := last - want; diff < -5 || diff > 5 {
		t.Fatalf("last_frozen_at = %d, want ≈ %d (diff %d)", last, want, diff)
	}

	// 逐笔物化余额与手工计算一致（created_seq 顺序：expense→income→transfer out→transfer in）
	wantAccBal := map[string]int64{
		ids[0]: 97000, // acc1: 100000-3000
		ids[1]: 55000, // acc2: 50000+5000
		ids[2]: 77000, // acc1: 97000-20000
		ids[3]: 75000, // acc2: 55000+20000
	}
	for id, wantBal := range wantAccBal {
		tx, err := e.txr.GetByID(id)
		if err != nil || tx == nil {
			t.Fatalf("取交易 %s 失败: %v", id, err)
		}
		if tx.AccountBalanceAfter == nil {
			t.Fatalf("交易 %s account_balance_after 为空", id)
		}
		if *tx.AccountBalanceAfter != wantBal {
			t.Fatalf("交易 %s account_balance_after = %d, want %d", id, *tx.AccountBalanceAfter, wantBal)
		}
		if tx.TotalBalanceAfter == nil {
			t.Fatalf("交易 %s total_balance_after 为空", id)
		}
	}
	// 转出行瞬间总额下降（acc1 -20000），转入行回到 152000（双边净 0）
	outTx, _ := e.txr.GetByID(ids[2])
	if *outTx.TotalBalanceAfter != 132000 {
		t.Fatalf("转出行 total_balance_after = %d, want 132000", *outTx.TotalBalanceAfter)
	}
	inTx, _ := e.txr.GetByID(ids[3])
	if *inTx.TotalBalanceAfter != 152000 {
		t.Fatalf("转入行 total_balance_after = %d, want 152000", *inTx.TotalBalanceAfter)
	}

	// 全量校验 ok
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK {
		t.Fatalf("verify 失败: %+v", vr.Mismatches)
	}
	if vr.CheckedRows != 4 {
		t.Fatalf("checked_rows = %d, want 4", vr.CheckedRows)
	}
}

// TestDynamicSeam T04 验收 4：物化后新交易的窗口余额与账户余额一致。
func TestDynamicSeam(t *testing.T) {
	e := newTestEnv(t)
	setupThreeTx(t, e)
	if _, err := e.mat.Materialize(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}

	// 动态区插入 1 笔（今天）：acc1 76000
	res := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 1000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})

	// accounts 余额 = 物化快照 + 动态
	b := e.mustBalance(t)
	if got := b.ByAccount["acc_cmb0001"]; got != 76000 {
		t.Fatalf("acc1 = %d, want 76000（物化 77000 + 动态 -1000）", got)
	}
	if b.Total != 151000 {
		t.Fatalf("total = %d, want 151000", b.Total)
	}

	// 新交易的窗口余额 == accounts 余额（最容易出错的地方，必须断言）
	window, last, err := e.bal.WindowBalances()
	if err != nil {
		t.Fatal(err)
	}
	wb, ok := window[res.Primary.ID]
	if !ok {
		t.Fatalf("新交易 %s 不在动态区窗口中", res.Primary.ID)
	}
	if wb.AccountBalanceAfter != b.ByAccount["acc_cmb0001"] {
		t.Fatalf("窗口余额 %d != 账户余额 %d", wb.AccountBalanceAfter, b.ByAccount["acc_cmb0001"])
	}
	if wb.TotalBalanceAfter != b.Total {
		t.Fatalf("窗口总余额 %d != total %d", wb.TotalBalanceAfter, b.Total)
	}
	_ = last
}

// TestFrozenProtection T04 验收 5：冻结区改/删 → 409 FROZEN。
func TestFrozenProtection(t *testing.T) {
	e := newTestEnv(t)
	ids := setupThreeTx(t, e)
	if _, err := e.mat.Materialize(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}

	amount := int64(5000)
	err := func() error {
		_, err := e.txn.Update(context.Background(), ids[0], &service.UpdateTxnInput{AmountCents: &amount})
		return err
	}()
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeFrozen {
		t.Fatalf("PUT 冻结区交易应返回 FROZEN, got %v", err)
	}
	if ae.Details["freeze_boundary"] == nil || ae.Details["transaction_time"] == nil || ae.Details["suggest"] == nil {
		t.Fatalf("FROZEN details 不完整: %+v", ae.Details)
	}
	if err := e.txn.SoftDelete(context.Background(), ids[0], true); err == nil {
		t.Fatal("DELETE 冻结区交易应返回 FROZEN")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeFrozen {
		t.Fatalf("DELETE 冻结区应返回 FROZEN, got %v", err)
	}
}

// TestFrozenWrite T04 验收 6：allow_frozen 写入 → frozen_write_flag=1 → ForceFull → verify ok。
func TestFrozenWrite(t *testing.T) {
	e := newTestEnv(t)
	setupThreeTx(t, e)
	if _, err := e.mat.Materialize(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}

	past := time.Now().AddDate(0, 0, -40).Unix()
	res, err := e.txn.Create(context.Background(), &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 700, AccountID: "acc_wx00001",
		CategoryID: catRef("cat_e10"), TransactionTime: &past, AllowFrozen: true,
	})
	if err != nil {
		t.Fatalf("allow_frozen 创建失败: %v", err)
	}
	if !res.Warning.Frozen {
		t.Fatal("warning.frozen 应为 true")
	}
	meta := store.NewMetaRepo(e.s)
	flag, _ := meta.Get("frozen_write_flag")
	if flag != "1" {
		t.Fatalf("frozen_write_flag = %q, want 1", flag)
	}

	// ForceFull 后 verify ok 且余额含新笔
	if _, err := e.mat.ForceFull(context.Background(), time.Now()); err != nil {
		t.Fatalf("ForceFull 失败: %v", err)
	}
	vr, err := e.bal.Verify()
	if err != nil || !vr.OK {
		t.Fatalf("ForceFull 后 verify 失败: %+v %v", vr, err)
	}
	b := e.mustBalance(t)
	if got := b.ByAccount["acc_wx00001"]; got != -700 {
		t.Fatalf("acc3 = %d, want -700", got)
	}
	flag, _ = meta.Get("frozen_write_flag")
	if flag != "0" {
		t.Fatalf("物化后 frozen_write_flag = %q, want 0", flag)
	}
}

// TestDuplicate T04 验收 7：去重 → 409 DUPLICATE；allow_duplicate → 201 + warning。
func TestDuplicate(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	in := &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001",
		CategoryID: catRef("cat_e01"), MerchantText: "星巴克",
	}
	e.mustCreate(t, in)

	_, err := e.txn.Create(context.Background(), in)
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeDuplicate {
		t.Fatalf("重复创建应返回 DUPLICATE, got %v", err)
	}
	if ae.Details["matched_transaction_id"] == nil {
		t.Fatal("DUPLICATE details 应含 matched_transaction_id")
	}

	dup := *in
	dup.AllowDuplicate = true
	res, err := e.txn.Create(context.Background(), &dup)
	if err != nil {
		t.Fatalf("allow_duplicate 创建失败: %v", err)
	}
	if !res.Warning.Duplicate {
		t.Fatal("warning.duplicate 应为 true")
	}
}
