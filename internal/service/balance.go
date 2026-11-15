// Package service 业务层：余额引擎、物化、交易写入。handler 不得直接写 transactions。
package service

import (
	"context"
	"strconv"

	"ledger/internal/store"
)

// Balances 当前余额聚合（§4.2）。
type Balances struct {
	ByAccount map[string]int64 `json:"by_account"` // acc_id → cents（不过滤 is_count_in_total）
	Total     int64            `json:"total"`      // 仅 is_count_in_total=1 的账户之和（含归档）
}

// BalanceService 当前余额、边界定义与全量校验（§6.3/§6.5）。
type BalanceService struct {
	s    *store.Store
	meta *store.MetaRepo
	tx   *store.TxRepo
	mat  *Materializer
}

// NewBalanceService 构造。
func NewBalanceService(s *store.Store, meta *store.MetaRepo, tx *store.TxRepo, mat *Materializer) *BalanceService {
	return &BalanceService{s: s, meta: meta, tx: tx, mat: mat}
}

// FreezeDays 当前冻结天数。
func (b *BalanceService) FreezeDays() int {
	v, err := b.meta.Get("freeze_days")
	if err != nil || v == "" {
		return 30
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 30
	}
	return n
}

// FreezeBoundary 写保护边界：now - freeze_days*86400（秒）。
func (b *BalanceService) FreezeBoundary(now int64) int64 {
	return now - int64(b.FreezeDays())*86400
}

// LastFrozenAt 已物化边界（0 = 从未物化）。
func (b *BalanceService) LastFrozenAt() (int64, error) {
	v, err := b.meta.Get("last_frozen_at")
	if err != nil {
		return 0, err
	}
	if v == "" {
		return 0, nil
	}
	return strconv.ParseInt(v, 10, 64)
}

// Current 当前余额（先按需补跑物化，再走 §6.5 查询一）。
func (b *BalanceService) Current(ctx context.Context) (*Balances, error) {
	b.mat.EnsureFresh(ctx)
	last, err := b.LastFrozenAt()
	if err != nil {
		return nil, err
	}
	rows, err := b.tx.CurrentBalances(last)
	if err != nil {
		return nil, err
	}
	out := &Balances{ByAccount: map[string]int64{}}
	for _, r := range rows {
		out.ByAccount[r.AccountID] = r.BalanceCents
		if r.IsCountInTotal == 1 {
			out.Total += r.BalanceCents
		}
	}
	return out, nil
}

// WindowBalances 动态区逐笔窗口余额（§6.5 查询二/三）。
func (b *BalanceService) WindowBalances() (map[string]store.WindowBalance, int64, error) {
	last, err := b.LastFrozenAt()
	if err != nil {
		return nil, 0, err
	}
	m, err := b.tx.WindowBalances(last)
	return m, last, err
}

// VerifyResult 全量校验结果。
type VerifyResult struct {
	OK          bool             `json:"ok"`
	CheckedRows int              `json:"checked_rows"`
	Mismatches  []VerifyMismatch `json:"mismatches"`
	// v1.2 §9.5：延迟冻结相关口径（向后兼容，只加字段）。
	// PendingRows 待确认冻结行数，纯信息，不影响 ok。
	PendingRows int `json:"pending_rows"`
	// MaterializationGaps 已确认且早于物化边界却没有物化值的行（I2 违反，影响 ok）。
	MaterializationGaps []MaterializationGap `json:"materialization_gaps"`
	// StalePending 待确认行却带着物化值（I3 违反，只可能是 bug，影响 ok）。
	StalePending int `json:"stale_pending"`
	// PendingBeforeBoundary 待确认行却早于物化边界（I3 时间侧违反，影响 ok）。
	// 这类行既不进快照也不进动态窗口 → 金额会静默消失；普通 Materialize 因「边界不后退」
	// 修不回来（只有 ForceFull 能修）。典型来源：恢复软删的待确认行、或外部改库。
	PendingBeforeBoundary int `json:"pending_before_boundary"`
}

// MaterializationGap 「余额对得上但物化缺口」明细。
type MaterializationGap struct {
	TransactionID string `json:"transaction_id"`
	Expected      string `json:"expected"`
	Actual        string `json:"actual"`
}

// verifyGapLimit 缺口明细返回条数上限（计数始终是真实值）。
const verifyGapLimit = 100

// VerifyMismatch 不一致明细。
type VerifyMismatch struct {
	TransactionID string `json:"transaction_id"`
	Field         string `json:"field"`
	Expected      int64  `json:"expected"`
	Actual        int64  `json:"actual"`
}

// Verify 全量余额校验：按排序键重算每个账户的逐笔余额与总余额，
// 与已物化的 account_balance_after / total_balance_after 逐行比对。
func (b *BalanceService) Verify() (*VerifyResult, error) {
	// 先取账户（GORM），再开游标——单连接池下不得边迭代边查询
	accs, err := store.NewMasterRepo(b.s).ListAccounts(true)
	if err != nil {
		return nil, err
	}
	type row struct {
		id    string
		acc   string
		delta int64
		accAf *int64
		totAf *int64
	}
	rows, err := b.s.SQL.Query(`SELECT t.id, t.account_id,
		CASE WHEN t.transaction_type='expense' THEN -t.amount_cents
		     WHEN t.transaction_type='income'  THEN  t.amount_cents
		     WHEN t.transfer_direction='out'   THEN -t.amount_cents
		     WHEN t.transfer_direction='in'    THEN  t.amount_cents ELSE 0 END,
		t.account_balance_after, t.total_balance_after
		FROM transactions t WHERE t.deleted_at IS NULL
		ORDER BY t.transaction_time ASC, t.created_seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bal := map[string]int64{}
	countIn := map[string]bool{}
	for _, a := range accs {
		bal[a.ID] = a.InitialBalanceCents
		countIn[a.ID] = a.IsCountInTotal == 1
	}
	res := &VerifyResult{OK: true, Mismatches: []VerifyMismatch{},
		MaterializationGaps: []MaterializationGap{}}
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.acc, &r.delta, &r.accAf, &r.totAf); err != nil {
			return nil, err
		}
		bal[r.acc] += r.delta
		total := int64(0)
		for a, v := range bal {
			if countIn[a] {
				total += v
			}
		}
		if r.accAf != nil {
			res.CheckedRows++
			if *r.accAf != bal[r.acc] {
				res.OK = false
				res.Mismatches = append(res.Mismatches, VerifyMismatch{
					TransactionID: r.id, Field: "account_balance_after", Expected: bal[r.acc], Actual: *r.accAf})
			}
			if r.totAf != nil && *r.totAf != total {
				res.OK = false
				res.Mismatches = append(res.Mismatches, VerifyMismatch{
					TransactionID: r.id, Field: "total_balance_after", Expected: total, Actual: *r.totAf})
			}
		}
	}
	loopErr := rows.Err()
	rows.Close() // 单连接池：后续查询前必须释放游标
	if loopErr != nil {
		return res, loopErr
	}

	// ---- §9.5 断言 A（I2）：已确认、早于物化边界、却没有物化值 ----
	last, err := b.LastFrozenAt()
	if err != nil {
		return nil, err
	}
	var gapCount int
	if err = b.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions
		WHERE freeze_confirmed = 1 AND deleted_at IS NULL
		  AND transaction_time < ? AND account_balance_after IS NULL`, last).Scan(&gapCount); err != nil {
		return nil, err
	}
	if gapCount > 0 {
		res.OK = false
		gapRows, gerr := b.s.SQL.Query(`SELECT id FROM transactions
			WHERE freeze_confirmed = 1 AND deleted_at IS NULL
			  AND transaction_time < ? AND account_balance_after IS NULL
			ORDER BY transaction_time, created_seq LIMIT ?`, last, verifyGapLimit)
		if gerr != nil {
			return nil, gerr
		}
		for gapRows.Next() {
			var id string
			if gerr = gapRows.Scan(&id); gerr != nil {
				gapRows.Close()
				return nil, gerr
			}
			res.MaterializationGaps = append(res.MaterializationGaps,
				MaterializationGap{TransactionID: id, Expected: "materialized", Actual: "null"})
		}
		gapRows.Close()
		if gerr = gapRows.Err(); gerr != nil {
			return nil, gerr
		}
	}

	// ---- §9.5 断言 B（I3）：待确认行不得带物化值 ----
	if err = b.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions
		WHERE freeze_confirmed = 0 AND account_balance_after IS NOT NULL`).Scan(&res.StalePending); err != nil {
		return nil, err
	}
	if res.StalePending > 0 {
		res.OK = false
	}

	// ---- pending_rows：纯信息，不影响 ok ----
	if err = b.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions
		WHERE freeze_confirmed = 0 AND deleted_at IS NULL`).Scan(&res.PendingRows); err != nil {
		return nil, err
	}

	// ---- 断言 C（I3 时间侧）：待确认行不得早于物化边界 ----
	// 这种行既不进快照（无物化值）也不进动态窗口（time < B），金额会凭空消失；
	// 仅靠断言 A/B 看不出来（B 只看 accAf 是否非空），故单独计一条。
	if err = b.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions
		WHERE freeze_confirmed = 0 AND deleted_at IS NULL AND transaction_time < ?`,
		last).Scan(&res.PendingBeforeBoundary); err != nil {
		return nil, err
	}
	if res.PendingBeforeBoundary > 0 {
		res.OK = false
	}
	return res, nil
}
