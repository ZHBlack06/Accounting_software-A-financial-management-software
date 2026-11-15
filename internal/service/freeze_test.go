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

// freezeFixture 造一批跨冻结边界的交易。
//
// 时间轴（now = 今天）：最近 3 笔（动态区）| 冻结区：4 笔已确认 + 3 笔待确认。
// 待确认里最早的是 -65 天，因此钳制后 last_frozen_at 应等于它的时间。
type freezeFixture struct {
	env          *testEnv
	account      string
	oldConfirmed []string // 早于边界、已确认（应被物化）
	pending      []string // 早于边界、待确认（不该被物化，可写）
	recent       []string // 动态区
}

func setupFreeze(t *testing.T) *freezeFixture {
	t.Helper()
	e := newTestEnv(t)
	accs, err := e.mast.ListAccounts(true)
	if err != nil {
		t.Fatal(err)
	}
	f := &freezeFixture{env: e, account: accs[0].ID}

	mk := func(daysAgo int, amount int64, confirmed *bool, remark string) string {
		t.Helper()
		ts := time.Now().AddDate(0, 0, -daysAgo).Unix()
		in := &service.CreateTxnInput{
			TransactionTime: &ts, TransactionType: "expense", AmountCents: amount,
			AccountID: f.account, CategoryID: strp("cat_e01"), Remark: remark,
			FreezeConfirmed: confirmed, AllowFrozen: true,
		}
		res := e.mustCreate(t, in)
		return res.Primary.ID
	}
	no := false
	for i, d := range []int{100, 90, 80, 70} {
		f.oldConfirmed = append(f.oldConfirmed, mk(d, int64(1000+i), nil, fmt.Sprintf("old%d", i)))
	}
	for i, d := range []int{65, 60, 55} {
		f.pending = append(f.pending, mk(d, int64(2000+i), &no, fmt.Sprintf("pending%d", i)))
	}
	for i, d := range []int{1, 2, 3} {
		f.recent = append(f.recent, mk(d, int64(3000+i), nil, fmt.Sprintf("recent%d", i)))
	}
	return f
}

// fullTableSum 独立 SQL 全表求和（不依赖余额引擎，用于交叉验证）。返回 (账户合计, 总合计)。
func fullTableSum(t *testing.T, s *store.Store) (map[string]int64, int64) {
	t.Helper()
	rows, err := s.SQL.Query(`SELECT account_id, COALESCE(SUM(CASE
		WHEN transaction_type='expense' THEN -amount_cents
		WHEN transaction_type='income'  THEN  amount_cents
		WHEN transfer_direction='out'   THEN -amount_cents
		WHEN transfer_direction='in'    THEN  amount_cents ELSE 0 END), 0)
		FROM transactions WHERE deleted_at IS NULL GROUP BY account_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	per := map[string]int64{}
	var total int64
	for rows.Next() {
		var id string
		var v int64
		if err = rows.Scan(&id, &v); err != nil {
			t.Fatal(err)
		}
		per[id] = v
		total += v
	}
	// 加上初始余额
	accRows, err := s.SQL.Query(`SELECT id, initial_balance_cents FROM accounts`)
	if err != nil {
		t.Fatal(err)
	}
	defer accRows.Close()
	for accRows.Next() {
		var id string
		var init int64
		if err = accRows.Scan(&id, &init); err != nil {
			t.Fatal(err)
		}
		per[id] += init
		total += init
	}
	return per, total
}

// TestMaterializeClampsToPendingBoundary §9.10 T11 ①③ + I1/I3。
func TestMaterializeClampsToPendingBoundary(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()

	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatalf("物化失败: %v", err)
	}
	last, err := e.bal.LastFrozenAt()
	if err != nil {
		t.Fatal(err)
	}
	minPending, err := e.txr.MinUnconfirmedTime()
	if err != nil {
		t.Fatal(err)
	}
	if minPending == 0 {
		t.Fatal("应有待确认行")
	}
	// ① 边界被钳制到最早待确认行的时刻
	if last != minPending {
		t.Errorf("last_frozen_at = %s，want 钳制到最早待确认行 %s",
			timeutil.FormatLocal(last), timeutil.FormatLocal(minPending))
	}
	// I1：物化区 ⊆ 冻结区
	if boundary := e.bal.FreezeBoundary(time.Now().Unix()); last > boundary {
		t.Errorf("I1 违反：last_frozen_at %s > freeze_boundary %s",
			timeutil.FormatLocal(last), timeutil.FormatLocal(boundary))
	}
	// ③ 待确认行不得有物化值，且不得早于边界（I3）
	for _, id := range f.pending {
		var accAf *int64
		var ts int64
		if err = e.s.SQL.QueryRow(`SELECT account_balance_after, transaction_time
			FROM transactions WHERE id = ?`, id).Scan(&accAf, &ts); err != nil {
			t.Fatal(err)
		}
		if accAf != nil {
			t.Errorf("I3 违反：待确认行 %s 带物化值 %d", id, *accAf)
		}
		if ts < last {
			t.Errorf("I3 违反：待确认行 %s（%s）早于边界 %s",
				id, timeutil.FormatLocal(ts), timeutil.FormatLocal(last))
		}
	}
	// 已确认的老行必须已物化（I2）
	for _, id := range f.oldConfirmed {
		var accAf *int64
		if err = e.s.SQL.QueryRow(`SELECT account_balance_after FROM transactions WHERE id = ?`,
			id).Scan(&accAf); err != nil {
			t.Fatal(err)
		}
		if accAf == nil {
			t.Errorf("I2 违反：已确认老行 %s 未物化", id)
		}
	}
}

// TestCurrentEqualsFullTableSum ②：余额引擎 == 全表独立求和（不重不漏）。
func TestCurrentEqualsFullTableSum(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	want, wantTotal := fullTableSum(t, e.s)
	got, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for acc, v := range want {
		if got.ByAccount[acc] != v {
			t.Errorf("账户 %s 余额 = %d，全表求和 = %d", acc, got.ByAccount[acc], v)
		}
	}
	if got.Total != wantTotal {
		t.Errorf("总余额 = %d，全表求和 = %d", got.Total, wantTotal)
	}

	// 反例检查：若把待确认行错误折进物化（即没钳制），余额会少掉它们
	var pendingSum int64
	if err = e.s.SQL.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM transactions
		WHERE freeze_confirmed = 0 AND deleted_at IS NULL`).Scan(&pendingSum); err != nil {
		t.Fatal(err)
	}
	if pendingSum == 0 {
		t.Fatal("待确认行金额合计不应为 0")
	}
}

// TestVerifyPendingAndGaps ④⑤：Verify 新字段口径。
func TestVerifyPendingAndGaps(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	// ④ ok + pending_rows=3 + gaps=0
	if !vr.OK {
		t.Errorf("应校验通过，mismatches=%d gaps=%d stale=%d",
			len(vr.Mismatches), len(vr.MaterializationGaps), vr.StalePending)
	}
	if vr.PendingRows != 3 {
		t.Errorf("pending_rows = %d，want 3", vr.PendingRows)
	}
	if len(vr.MaterializationGaps) != 0 {
		t.Errorf("gaps 应为 0，实得 %+v", vr.MaterializationGaps)
	}
	// checked_rows 只统计已物化行（待确认行天然不计入）
	if vr.CheckedRows == 0 {
		t.Error("checked_rows 不应为 0（应有已物化行）")
	}

	// ⑤ 人为把某已确认行的物化值置空 → gaps>0 且 ok=false
	if _, err = e.s.SQL.Exec(`UPDATE transactions SET account_balance_after = NULL WHERE id = ?`,
		f.oldConfirmed[0]); err != nil {
		t.Fatal(err)
	}
	vr2, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if vr2.OK {
		t.Error("存在物化缺口时 ok 必须为 false")
	}
	if len(vr2.MaterializationGaps) == 0 {
		t.Error("应报出 materialization_gaps")
	}
	// 恢复
	if _, err = e.s.SQL.Exec(`UPDATE transactions SET account_balance_after = 1 WHERE id = ?`,
		f.oldConfirmed[0]); err != nil {
		t.Fatal(err)
	}
	// 再把某待确认行写上物化值 → stale_pending>0 且 ok=false
	if _, err = e.s.SQL.Exec(`UPDATE transactions SET account_balance_after = 1 WHERE id = ?`,
		f.pending[0]); err != nil {
		t.Fatal(err)
	}
	vr3, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if vr3.StalePending == 0 || vr3.OK {
		t.Errorf("stale_pending 应 >0 且 ok=false，实得 %d/%v", vr3.StalePending, vr3.OK)
	}
	if _, err = e.s.SQL.Exec(`UPDATE transactions SET account_balance_after = NULL WHERE id = ?`,
		f.pending[0]); err != nil {
		t.Fatal(err)
	}
}

// TestPendingRowIsWritable T10 ④：待确认的 2018/老行可 PUT、可 DELETE。
func TestPendingRowIsWritable(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	target := f.pending[0]

	// PUT 改金额（老行，早于冻结边界，本来会 409）
	newAmount := int64(4321)
	if _, err := e.txn.Update(ctx, target, &service.UpdateTxnInput{AmountCents: &newAmount}); err != nil {
		t.Fatalf("待确认行应可改金额，实得 %v", err)
	}
	// 改时间到更早的历史（允许，且会触发边界回退）
	earlier := time.Now().AddDate(0, 0, -200).Unix()
	if _, err := e.txn.Update(ctx, target, &service.UpdateTxnInput{TransactionTime: &earlier}); err != nil {
		t.Fatalf("待确认行应可改到任意历史时间，实得 %v", err)
	}
	// 改到更早后 I3 靠 frozen_write_flag 自愈：下一次查询会 ForceFull 把边界拉回
	if _, err := e.bal.Current(ctx); err != nil {
		t.Fatal(err)
	}
	var pendingTime int64
	if err := e.s.SQL.QueryRow(`SELECT transaction_time FROM transactions WHERE id = ?`,
		target).Scan(&pendingTime); err != nil {
		t.Fatal(err)
	}
	last, _ := e.bal.LastFrozenAt()
	if pendingTime < last {
		t.Errorf("I3 违反：改早后待确认行 %s 仍早于边界 %s",
			timeutil.FormatLocal(pendingTime), timeutil.FormatLocal(last))
	}
	// DELETE 也应允许
	if err := e.txn.SoftDelete(ctx, target, true); err != nil {
		t.Fatalf("待确认行应可删除，实得 %v", err)
	}
	// 已确认老行仍受保护（回归）
	newAmount2 := int64(1)
	_, err := e.txn.Update(ctx, f.oldConfirmed[0], &service.UpdateTxnInput{AmountCents: &newAmount2})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeFrozen {
		t.Fatalf("已确认老行应仍报 409 FROZEN，实得 %v", err)
	}
}

// TestConfirmFreezeFlow T12 ①②③：确认后边界前进、物化成功、幂等。
func TestConfirmFreezeFlow(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeLast, _ := e.bal.LastFrozenAt()

	fs := service.NewFreezeService(e.s, e.meta, e.txr, e.bal, e.mat)
	res, err := fs.Confirm(ctx, &service.FreezeConfirmInput{TransactionIDs: f.pending})
	if err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	if res.Confirmed != 3 {
		t.Errorf("confirmed = %d，want 3", res.Confirmed)
	}
	if !res.Materialized {
		t.Errorf("确认后应已完成物化: %+v", res)
	}
	if res.PendingRemaining != 0 {
		t.Errorf("pending_remaining = %d，want 0", res.PendingRemaining)
	}
	afterLast, _ := e.bal.LastFrozenAt()
	if afterLast <= beforeLast {
		t.Errorf("边界应前进：%s → %s",
			timeutil.FormatLocal(beforeLast), timeutil.FormatLocal(afterLast))
	}
	// 边界应推进到 now-freeze_days（30 天）
	want := e.bal.FreezeBoundary(time.Now().Unix())
	if afterLast != want {
		t.Errorf("last_frozen_at = %s，want %s", timeutil.FormatLocal(afterLast), timeutil.FormatLocal(want))
	}
	// 余额必须完全相等
	after, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Total != before.Total {
		t.Errorf("确认前后总余额不一致: %d → %d", before.Total, after.Total)
	}
	for acc, v := range before.ByAccount {
		if after.ByAccount[acc] != v {
			t.Errorf("账户 %s 余额变化: %d → %d", acc, v, after.ByAccount[acc])
		}
	}
	// 幂等：重复确认 → confirmed=0
	res2, err := fs.Confirm(ctx, &service.FreezeConfirmInput{TransactionIDs: f.pending})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Confirmed != 0 {
		t.Errorf("重复确认 confirmed = %d，want 0", res2.Confirmed)
	}
	// 确认后同一笔必须重新受保护
	newAmount := int64(5)
	if _, err = e.txn.Update(ctx, f.pending[0], &service.UpdateTxnInput{AmountCents: &newAmount}); err == nil {
		t.Error("确认后老行应重新受写保护（409 FROZEN）")
	}
	// verify 恢复到 ok + pending=0 + gaps=0
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK || vr.PendingRows != 0 || len(vr.MaterializationGaps) != 0 {
		t.Errorf("确认后校验异常: ok=%v pending=%d gaps=%d stale=%d",
			vr.OK, vr.PendingRows, len(vr.MaterializationGaps), vr.StalePending)
	}
}

// TestConfirmByBatchAndAll T12 ①：batch_id / all 两种入参 + 参数校验。
func TestConfirmByBatchAndAll(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	fs := service.NewFreezeService(e.s, e.meta, e.txr, e.bal, e.mat)

	// 空入参 → 400
	if _, err := fs.Confirm(ctx, &service.FreezeConfirmInput{}); err == nil {
		t.Error("空入参应报错")
	}
	// all=true 未 confirm → 400
	_, err := fs.Confirm(ctx, &service.FreezeConfirmInput{All: true})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Errorf("all 未二次确认应报 VALIDATION_ERROR，实得 %v", err)
	}
	// 超上限 → 400
	many := make([]string, 5001)
	if _, err = fs.Confirm(ctx, &service.FreezeConfirmInput{TransactionIDs: many}); err == nil {
		t.Error("超过 5000 个 id 应报错")
	}
	// 混入不存在的 id → skipped_missing > 0，其余照常确认
	res, err := fs.Confirm(ctx, &service.FreezeConfirmInput{
		TransactionIDs: append([]string{"202001010000000000X"}, f.pending[0]),
	})
	if err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	if res.SkippedMissing != 1 || res.Confirmed != 1 {
		t.Errorf("skipped_missing=%d confirmed=%d，want 1/1", res.SkippedMissing, res.Confirmed)
	}
	// all=true + confirm → 确认剩余 2 条
	res2, err := fs.Confirm(ctx, &service.FreezeConfirmInput{All: true, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Confirmed != 2 || res2.PendingRemaining != 0 {
		t.Errorf("all 确认结果 confirmed=%d pending=%d，want 2/0", res2.Confirmed, res2.PendingRemaining)
	}
}

// TestConfirmMaterializeFailureKeepsConfirmation T12 ③：物化失败不回滚确认。
func TestConfirmMaterializeFailureKeepsConfirmation(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	fs := service.NewFreezeService(e.s, e.meta, e.txr, e.bal, e.mat)
	// 注入物化失败：freeze_days 非数字 → metaInt 解析失败 → ForceFull 返回错误
	if err := e.meta.Set("freeze_days", "abc"); err != nil {
		t.Fatal(err)
	}
	res, err := fs.Confirm(ctx, &service.FreezeConfirmInput{TransactionIDs: f.pending})
	if err != nil {
		t.Fatalf("确认本身不应失败: %v", err)
	}
	if res.Materialized {
		t.Error("注入失败后 materialized 应为 false")
	}
	if res.MaterializeError == "" {
		t.Error("应返回 materialize_error")
	}
	// 确认必须已持久
	var n int64
	if err = e.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions WHERE id IN (?,?,?) AND freeze_confirmed = 1`,
		f.pending[0], f.pending[1], f.pending[2]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("确认应已持久（3 行），实得 %d", n)
	}
	// 修复后下次查询自动补偿
	if err = e.meta.Set("freeze_days", "30"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.bal.Current(ctx); err != nil {
		t.Fatal(err)
	}
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK {
		t.Errorf("补偿后校验应通过: gaps=%d stale=%d", len(vr.MaterializationGaps), vr.StalePending)
	}
}

// TestMinUnconfirmedTime 钳制查询本身。
func TestMinUnconfirmedTime(t *testing.T) {
	f := setupFreeze(t)
	got, err := f.env.txr.MinUnconfirmedTime()
	if err != nil {
		t.Fatal(err)
	}
	var want int64
	if err = f.env.s.SQL.QueryRow(`SELECT MIN(transaction_time) FROM transactions
		WHERE freeze_confirmed = 0 AND deleted_at IS NULL`).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if got != want || got == 0 {
		t.Errorf("MinUnconfirmedTime = %d，want %d", got, want)
	}
	// 无待确认行 → 0
	if _, err = f.env.s.SQL.Exec(`UPDATE transactions SET freeze_confirmed = 1 WHERE freeze_confirmed = 0`); err != nil {
		t.Fatal(err)
	}
	if got, err = f.env.txr.MinUnconfirmedTime(); err != nil || got != 0 {
		t.Errorf("无待确认行应返回 0，实得 %d（%v）", got, err)
	}
}
