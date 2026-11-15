package service_test

import (
	"context"
	"testing"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
)

// freeze_boundary 语义速记（本文件全部用例的前提）：
//   - 物化区间是**左闭右开** `[last_frozen_at, target)`；
//   - 因此「已确认 且 time < last_frozen_at」才必须有物化值（断言 A 用 `<`，不能用 `<=`）；
//   - 恰好 `time == last_frozen_at` 的行属动态区，可以没有物化值。

// lastFrozenAtOf 读当前物化边界。
func lastFrozenAtOf(t *testing.T, e *testEnv) int64 {
	t.Helper()
	last, err := e.bal.LastFrozenAt()
	if err != nil {
		t.Fatal(err)
	}
	return last
}

// TestEdge1BoundaryEqualsMinPending ① `last_frozen_at == minUnconfirmed` → 无缺口。
func TestEdge1BoundaryEqualsMinPending(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	last := lastFrozenAtOf(t, e)
	minPending, err := e.txr.MinUnconfirmedTime()
	if err != nil {
		t.Fatal(err)
	}
	if last != minPending {
		t.Fatalf("边界应等于最早待确认行：last=%s min=%s",
			timeutil.FormatLocal(last), timeutil.FormatLocal(minPending))
	}
	// 恰好 == last 的那行是动态行：不得被算成缺口
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK || len(vr.MaterializationGaps) != 0 {
		t.Errorf("边界 == 待确认行时不应有缺口: ok=%v gaps=%d", vr.OK, len(vr.MaterializationGaps))
	}
	if vr.PendingRows != 3 {
		t.Errorf("pending_rows = %d，want 3", vr.PendingRows)
	}
	// 该行本身也确实没有物化值
	var accAf *int64
	if err = e.s.SQL.QueryRow(`SELECT account_balance_after FROM transactions
		WHERE transaction_time = ? AND freeze_confirmed = 0`, last).Scan(&accAf); err != nil {
		t.Fatal(err)
	}
	if accAf != nil {
		t.Errorf("动态区边上的待确认行不应有物化值，实得 %d", *accAf)
	}
}

// TestEdge2NoPendingAdvances ② 无待确认行 → 边界推进到 now-30d。
func TestEdge2NoPendingAdvances(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	pendingLast := lastFrozenAtOf(t, e)

	// 直接确认掉（模拟用户核对完）
	if _, err := e.s.SQL.Exec(`UPDATE transactions SET freeze_confirmed = 1,
		freeze_confirmed_at = ? WHERE freeze_confirmed = 0`, timeutil.NowUnix()); err != nil {
		t.Fatal(err)
	}
	res, err := e.mat.Materialize(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := e.bal.FreezeBoundary(time.Now().Unix())
	got := lastFrozenAtOf(t, e)
	if got != want {
		t.Errorf("无待确认行时边界应推进到 now-30d：%s vs %s（rows=%d）",
			timeutil.FormatLocal(got), timeutil.FormatLocal(want), res.RowsProcessed)
	}
	if got <= pendingLast {
		t.Errorf("边界未前进：%s → %s", timeutil.FormatLocal(pendingLast), timeutil.FormatLocal(got))
	}
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK || vr.PendingRows != 0 || len(vr.MaterializationGaps) != 0 {
		t.Errorf("推进后校验异常: ok=%v pending=%d gaps=%d",
			vr.OK, vr.PendingRows, len(vr.MaterializationGaps))
	}
}

// TestEdge3DeletedPendingNoLongerClamps ③ 待确认行软删后不再参与钳制。
func TestEdge3DeletedPendingNoLongerClamps(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	firstLast := lastFrozenAtOf(t, e)
	// 最早那笔待确认行（-65 天）软删 → 钳制点应移到下一笔（-60 天）
	if err := e.txn.SoftDelete(ctx, f.pending[0], true); err != nil {
		t.Fatalf("待确认行应可删除: %v", err)
	}
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	second := lastFrozenAtOf(t, e)
	if second <= firstLast {
		t.Errorf("软删最早待确认行后边界应前进：%s → %s",
			timeutil.FormatLocal(firstLast), timeutil.FormatLocal(second))
	}
	var want int64
	if err := e.s.SQL.QueryRow(`SELECT MIN(transaction_time) FROM transactions
		WHERE freeze_confirmed = 0 AND deleted_at IS NULL`).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if second != want {
		t.Errorf("边界应等于剩余最早待确认行：%s vs %s",
			timeutil.FormatLocal(second), timeutil.FormatLocal(want))
	}
	// 余额仍与全表求和一致（软删行已不计入）
	got, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, wantTotal := fullTableSum(t, e.s)
	if got.Total != wantTotal {
		t.Errorf("余额 = %d，全表求和 = %d", got.Total, wantTotal)
	}
}

// TestEdge4MovePendingEarlierDoesNotCross ④ 待确认行改到更早 → 边界只能停滞或后退。
func TestEdge4MovePendingEarlierDoesNotCross(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	baseLast := lastFrozenAtOf(t, e)

	target := f.pending[1] // -60 天
	earlier := time.Now().AddDate(0, 0, -200).Unix()
	if _, err := e.txn.Update(ctx, target, &service.UpdateTxnInput{TransactionTime: &earlier}); err != nil {
		t.Fatalf("待确认行应可改到更早时间: %v", err)
	}
	// 触发补偿（Update 已置 frozen_write_flag）
	if _, err := e.bal.Current(ctx); err != nil {
		t.Fatal(err)
	}
	last := lastFrozenAtOf(t, e)
	if last > baseLast {
		t.Errorf("边界不得穿越更早的待确认行：base=%s now=%s",
			timeutil.FormatLocal(baseLast), timeutil.FormatLocal(last))
	}
	if last > earlier {
		t.Errorf("边界必须 <= 该行新时间：last=%s row=%s",
			timeutil.FormatLocal(last), timeutil.FormatLocal(earlier))
	}
	// 不重不漏
	got, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	per, wantTotal := fullTableSum(t, e.s)
	if got.Total != wantTotal {
		t.Errorf("余额 = %d，全表求和 = %d", got.Total, wantTotal)
	}
	if got.ByAccount[e.mastAccountID(t)] != per[e.mastAccountID(t)] {
		t.Errorf("账户余额与全表求和不一致")
	}
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK {
		t.Errorf("改早后校验应通过: gaps=%d stale=%d", len(vr.MaterializationGaps), vr.StalePending)
	}
}

// mastAccountID 取第一个账户 ID（测试辅助）。
func (e *testEnv) mastAccountID(t *testing.T) string {
	t.Helper()
	accs, err := e.mast.ListAccounts(true)
	if err != nil {
		t.Fatal(err)
	}
	return accs[0].ID
}

// TestEdge5ConfirmAllNeedsConfirm ⑤ all=true 未 confirm → 400；confirm:true 生效。
func TestEdge5ConfirmAllNeedsConfirm(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	fs := service.NewFreezeService(e.s, e.meta, e.txr, e.bal, e.mat)
	_, err := fs.Confirm(ctx, &service.FreezeConfirmInput{All: true, Confirm: false})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("all 未二次确认应 400 VALIDATION_ERROR，实得 %v", err)
	}
	// 未被误改
	var pending int
	if err = e.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions WHERE freeze_confirmed = 0`).
		Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 3 {
		t.Errorf("400 后不应有任何确认，pending = %d", pending)
	}
	res, err := fs.Confirm(ctx, &service.FreezeConfirmInput{All: true, Confirm: true})
	if err != nil {
		t.Fatalf("all+confirm 应生效: %v", err)
	}
	if res.Confirmed != 3 || res.PendingRemaining != 0 {
		t.Errorf("all 确认结果 = %d/%d，want 3/0", res.Confirmed, res.PendingRemaining)
	}
}

// TestEdge6ConfirmedRowCannotMoveToHistory ⑥ 已确认行 PUT 到历史时间 → 409。
func TestEdge6ConfirmedRowCannotMoveToHistory(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	// 冻结区里的已确认行，改金额 → 409
	amount := int64(1)
	_, err := e.txn.Update(ctx, f.oldConfirmed[0], &service.UpdateTxnInput{AmountCents: &amount})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeFrozen {
		t.Fatalf("已确认老行改金额应 409 FROZEN，实得 %v", err)
	}
	// 动态区里的已确认行，改到历史时间 → 409（改时间分支）
	historical := time.Now().AddDate(0, 0, -100).Unix()
	_, err = e.txn.Update(ctx, f.recent[0], &service.UpdateTxnInput{TransactionTime: &historical})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeFrozen {
		t.Fatalf("已确认行改到历史时间应 409 FROZEN，实得 %v", err)
	}
}

// TestEdge8RepeatBatchConfirmIdempotent ⑧ 同批次重复确认 → confirmed=0 且不报错。
func TestEdge8RepeatBatchConfirmIdempotent(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	// 给待确认行打上同一个批次号
	for _, id := range f.pending {
		if _, err := e.s.SQL.Exec(`UPDATE transactions SET import_batch_id = 'imp_edgebatch' WHERE id = ?`,
			id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	fs := service.NewFreezeService(e.s, e.meta, e.txr, e.bal, e.mat)
	res, err := fs.Confirm(ctx, &service.FreezeConfirmInput{BatchID: "imp_edgebatch"})
	if err != nil {
		t.Fatalf("首次确认失败: %v", err)
	}
	if res.Confirmed != 3 || res.PendingRemaining != 0 {
		t.Fatalf("首次确认 = %d/%d，want 3/0", res.Confirmed, res.PendingRemaining)
	}
	res2, err := fs.Confirm(ctx, &service.FreezeConfirmInput{BatchID: "imp_edgebatch"})
	if err != nil {
		t.Fatalf("重复确认不应报错: %v", err)
	}
	if res2.Confirmed != 0 || res2.PendingRemaining != 0 {
		t.Errorf("重复确认 = %d/%d，want 0/0", res2.Confirmed, res2.PendingRemaining)
	}
	if !res2.Materialized {
		t.Error("幂等确认也应返回 materialized=true（无待改行，无需重算）")
	}
}

// TestEdge9I3SelfHeal ⑨ I3 自愈：人为给待确认行写物化值（模拟 §9.9 解冻脚本/外部改库）
// → verify 立刻报 stale_pending>0 且 ok=false；下一次 Materialize 自动清空 → 恢复 ok。
func TestEdge9I3SelfHeal(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	// 未确认状态下本应干净
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if vr.StalePending != 0 || !vr.OK {
		t.Fatalf("前置状态应干净: stale=%d ok=%v", vr.StalePending, vr.OK)
	}
	// 人为污染（等价于「对已物化行执行了解冻脚本」）
	if _, err = e.s.SQL.Exec(`UPDATE transactions
		SET account_balance_after = -114, total_balance_after = -114, materialized_at = 1
		WHERE id = ?`, f.pending[0]); err != nil {
		t.Fatal(err)
	}
	vr2, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if vr2.StalePending == 0 || vr2.OK {
		t.Fatalf("污染后应报 stale_pending>0 且 ok=false，实得 %d/%v", vr2.StalePending, vr2.OK)
	}
	// 任意一次物化即自愈（即使边界无需推进）
	if _, err = e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var left int
	if err = e.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions
		WHERE freeze_confirmed = 0 AND account_balance_after IS NOT NULL`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("物化后应清空待确认行的物化值，实得 %d 条", left)
	}
	vr3, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr3.OK || vr3.StalePending != 0 {
		t.Errorf("自愈后应恢复 ok: ok=%v stale=%d", vr3.OK, vr3.StalePending)
	}
}

// TestEdge10RestorePendingTriggersRecompute P1-2 回归：
// 待确认行软删期间边界越过它 → 恢复后必须立刻整表重算，余额不得静默少算。
func TestEdge10RestorePendingTriggersRecompute(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, wantTotal := fullTableSum(t, e.s)

	// 1) 软删最早那笔待确认行（-65 天）
	target := f.pending[0]
	if err := e.txn.SoftDelete(ctx, target, true); err != nil {
		t.Fatalf("待确认行应可删除: %v", err)
	}
	// 2) 确认剩下两笔 → 边界推进到 now-30d（越过被删行的时间点）
	fs := service.NewFreezeService(e.s, e.meta, e.txr, e.bal, e.mat)
	res, err := fs.Confirm(ctx, &service.FreezeConfirmInput{
		TransactionIDs: []string{f.pending[1], f.pending[2]},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Confirmed != 2 || res.PendingRemaining != 0 {
		t.Fatalf("确认结果 %d/%d", res.Confirmed, res.PendingRemaining)
	}
	lastBefore, _ := e.bal.LastFrozenAt()
	var rowTime int64
	if err = e.s.SQL.QueryRow(`SELECT transaction_time FROM transactions WHERE id = ?`,
		target).Scan(&rowTime); err != nil {
		t.Fatal(err)
	}
	if rowTime >= lastBefore {
		t.Fatalf("前置失败：被删行应早于边界（row=%s last=%s）",
			timeutil.FormatLocal(rowTime), timeutil.FormatLocal(lastBefore))
	}

	// 3) 恢复该行 → 必须触发 ForceFull 把边界拉回
	if err = e.txn.Restore(ctx, target); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	lastAfter, _ := e.bal.LastFrozenAt()
	if lastAfter > rowTime {
		t.Errorf("恢复后边界必须回退到该行（含）之前：row=%s last=%s",
			timeutil.FormatLocal(rowTime), timeutil.FormatLocal(lastAfter))
	}
	// 4) 余额必须与全表独立求和一致（修复前会少 2000 分组）
	got, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != wantTotal {
		t.Errorf("恢复后总余额 = %d，全表求和 = %d（差 %d）",
			got.Total, wantTotal, got.Total-wantTotal)
	}
	// 5) verify 必须通过（含断言 C）
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK || vr.PendingBeforeBoundary != 0 {
		t.Errorf("恢复后校验异常: ok=%v pending_before_boundary=%d gaps=%d",
			vr.OK, vr.PendingBeforeBoundary, len(vr.MaterializationGaps))
	}
	// 6) 该行恢复成待确认状态（可写）
	restored, err := e.txn.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	if restored.FreezeConfirmed != 0 || restored.DeletedAt != nil {
		t.Errorf("恢复后状态异常: confirmed=%d deleted=%v",
			restored.FreezeConfirmed, restored.DeletedAt)
	}
}

// TestEdge11VerifyDetectsPendingBehindBoundary 断言 C：人为造「待确认行早于边界」必须被发现。
func TestEdge11VerifyDetectsPendingBehindBoundary(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	// 人为把边界推进到 now-30d（模拟「同批其它行被确认」），留下待确认行在边界之前
	if _, err := e.s.SQL.Exec(`UPDATE transactions SET freeze_confirmed = 1 WHERE id = ?`,
		f.pending[0]); err != nil {
		t.Fatal(err)
	}
	// 其余待确认行仍在边界之后，先确认掉，使边界可以推进
	if _, err := e.s.SQL.Exec(`UPDATE transactions SET freeze_confirmed = 1 WHERE freeze_confirmed = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	last, _ := e.bal.LastFrozenAt()
	// 再把最早那行打回待确认（模拟 Restore 未重算的 bug 状态）
	if _, err := e.s.SQL.Exec(`UPDATE transactions SET freeze_confirmed = 0 WHERE id = ?`,
		f.pending[0]); err != nil {
		t.Fatal(err)
	}
	var rowTime int64
	if err := e.s.SQL.QueryRow(`SELECT transaction_time FROM transactions WHERE id = ?`,
		f.pending[0]).Scan(&rowTime); err != nil {
		t.Fatal(err)
	}
	if rowTime >= last {
		t.Skipf("该行时间未早于边界，跳过（row=%s last=%s）",
			timeutil.FormatLocal(rowTime), timeutil.FormatLocal(last))
	}
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if vr.PendingBeforeBoundary == 0 {
		t.Error("应检出「待确认行早于物化边界」")
	}
	if vr.OK {
		t.Error("存在该违反时 ok 必须为 false")
	}
}

// TestEdge12AdjustmentIsConfirmed P2-7 回归：调整分录必须是「已冻结」，不得落成待确认。
func TestEdge12AdjustmentIsConfirmed(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	now := time.Now().Unix()
	created := e.mustCreate(t, &service.CreateTxnInput{
		TransactionTime: &now, TransactionType: "expense", AmountCents: 5000,
		AccountID: e.mastAccountID(t), CategoryID: strp("cat_e01"), Source: "manual",
	})
	// adjust：5000 → 6000
	adj, err := e.adjust.Adjust(ctx, created.Primary.ID, &service.AdjustInput{
		Mode: "adjust", NewAmountCents: 6000, Remark: "补记 10 元",
	})
	if err != nil {
		t.Fatalf("调整失败: %v", err)
	}
	if len(adj.Created) == 0 {
		t.Fatal("应生成调整分录")
	}
	var fc int
	var accAf *int64
	if err = e.s.SQL.QueryRow(`SELECT freeze_confirmed, account_balance_after
		FROM transactions WHERE id = ?`, adj.Created[0].ID).Scan(&fc, &accAf); err != nil {
		t.Fatal(err)
	}
	if fc != 1 {
		t.Errorf("调整分录 freeze_confirmed = %d，want 1（不得是待确认）", fc)
	}
	if accAf != nil {
		// 调整时间 = now 在动态区，accAf 为 NULL 属正常；这里只在被误标待确认时才会异常
		t.Logf("调整分录 account_balance_after = %d（动态区行，可为空）", *accAf)
	}
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if vr.PendingRows != 0 {
		t.Errorf("调整分录不应计入待确认：pending_rows = %d", vr.PendingRows)
	}
	if vr.StalePending != 0 {
		t.Errorf("stale_pending = %d，want 0", vr.StalePending)
	}
}

// TestEdge13CreatePendingBackTriggersRecompute 写入路径审计补漏：
// 经 Create / CreateBatch 直接造出「待确认 + 时间早于物化边界」的行时，
// 必须置 frozen_write_flag 触发重算，否则该行既不进快照也不进动态窗口。
func TestEdge13CreatePendingBackTriggersRecompute(t *testing.T) {
	f := setupFreeze(t)
	e := f.env
	ctx := context.Background()
	if _, err := e.mat.Materialize(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	lastBefore, _ := e.bal.LastFrozenAt()
	_, wantTotal := fullTableSum(t, e.s)

	// 造一笔「待确认 + 200 天前」（远早于 last_frozen_at）
	old := time.Now().AddDate(0, 0, -200).Unix()
	no := false
	in := &service.CreateTxnInput{
		TransactionTime: &old, TransactionType: "expense", AmountCents: 777,
		AccountID: e.mastAccountID(t), CategoryID: strp("cat_e01"),
		FreezeConfirmed: &no, AllowFrozen: true,
	}
	if _, err := e.txn.Create(ctx, in); err != nil {
		t.Fatalf("创建待确认行失败: %v", err)
	}
	// Create 应已置 flag → 下一次查询触发 ForceFull，边界回退
	flag, err := e.meta.Get("frozen_write_flag")
	if err != nil {
		t.Fatal(err)
	}
	if flag != "1" {
		t.Errorf("Create 造出「待确认 + 早于 last」的行必须置 frozen_write_flag，实得 %q", flag)
	}
	got, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, wantAfter := fullTableSum(t, e.s)
	if got.Total != wantAfter {
		t.Errorf("余额 = %d，全表求和 = %d（差 %d）", got.Total, wantAfter, got.Total-wantAfter)
	}
	if wantAfter == wantTotal {
		t.Fatal("测试前置问题：新增行未改变合计")
	}
	lastAfter, _ := e.bal.LastFrozenAt()
	if lastAfter > old {
		t.Errorf("边界必须回退到该行之前：row=%s last=%s",
			timeutil.FormatLocal(old), timeutil.FormatLocal(lastAfter))
	}
	vr, err := e.bal.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK || vr.PendingBeforeBoundary != 0 {
		t.Errorf("校验异常: ok=%v pending_before_boundary=%d",
			vr.OK, vr.PendingBeforeBoundary)
	}
	_ = lastBefore

	// CreateBatch 同样：造一条「待确认 + 早于边界」→ 置 flag 且重算后余额正确
	older := time.Now().AddDate(0, 0, -300).Unix()
	br, err := e.txn.CreateBatch(ctx, []*service.CreateTxnInput{{
		TransactionTime: &older, TransactionType: "expense", AmountCents: 888,
		AccountID: e.mastAccountID(t), CategoryID: strp("cat_e01"),
		FreezeConfirmed: &no, AllowFrozen: true, Source: "import",
	}}, true)
	if err != nil || len(br.Created) != 1 {
		t.Fatalf("CreateBatch 失败: %v %+v", err, br)
	}
	if flag, _ = e.meta.Get("frozen_write_flag"); flag != "1" {
		t.Errorf("CreateBatch 造出同类行必须置 flag，实得 %q", flag)
	}
	if _, err = e.bal.Current(ctx); err != nil {
		t.Fatal(err)
	}
	got2, err := e.bal.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, want2 := fullTableSum(t, e.s)
	if got2.Total != want2 {
		t.Errorf("批量造行后余额 = %d，全表求和 = %d", got2.Total, want2)
	}
}
