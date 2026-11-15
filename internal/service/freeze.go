package service

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// IsWriteProtected 写保护**唯一判定入口**（ARCHITECTURE-V12 §9.3）。
//
// 早于冻结边界且已确认冻结 → 只读（409 FROZEN）。
// 待确认行（freeze_confirmed=0）永远可写，不受 transaction_time 影响 —— 这正是
// 「导入的历史数据先让我核对再冻结」这条需求的核心。
//
// 任何新增的写路径都必须调用本函数，禁止再手写 `t.TransactionTime < boundary`。
func IsWriteProtected(t *store.Transaction, freezeBoundary int64) bool {
	if t == nil {
		return false
	}
	return t.FreezeConfirmed == 1 && t.TransactionTime < freezeBoundary
}

// IsFreezePending 该行是否「待确认冻结」。
func IsFreezePending(t *store.Transaction) bool {
	return t != nil && t.FreezeConfirmed == 0
}

// FreezeConfirmInput POST /api/transactions/freeze-confirm 请求体（§9.6）。
// 三选一，优先级 transaction_ids > batch_id > all。
type FreezeConfirmInput struct {
	TransactionIDs []string `json:"transaction_ids"`
	BatchID        string   `json:"batch_id"`
	All            bool     `json:"all"`
	Confirm        bool     `json:"confirm"`
}

// FreezeConfirmResult 响应（§9.6）。
type FreezeConfirmResult struct {
	Confirmed            int    `json:"confirmed"`
	SkippedMissing       int    `json:"skipped_missing"`
	PendingRemaining     int    `json:"pending_remaining"`
	Materialized         bool   `json:"materialized"`
	MaterializeRows      int    `json:"materialize_rows"`
	MaterializeElapsedMS int64  `json:"materialize_elapsed_ms"`
	NewFrozenAt          string `json:"new_frozen_at"`
	FreezeBoundary       string `json:"freeze_boundary"`
	// MaterializeError 仅在物化失败时出现（确认已持久，不回滚）。
	MaterializeError string `json:"materialize_error,omitempty"`
}

// maxConfirmIDs 单次确认的 id 上限（§9.6：超出 400，避免超大请求体）。
const maxConfirmIDs = 5000

// FreezeService 延迟冻结：确认 + 按需重算（§9.6）。
type FreezeService struct {
	s       *store.Store
	meta    *store.MetaRepo
	tx      *store.TxRepo
	balance *BalanceService
	mat     *Materializer
}

// NewFreezeService 构造。
func NewFreezeService(s *store.Store, meta *store.MetaRepo, tx *store.TxRepo,
	balance *BalanceService, mat *Materializer) *FreezeService {
	return &FreezeService{s: s, meta: meta, tx: tx, balance: balance, mat: mat}
}

// Confirm 确认冻结。两个事务故意分开（§9.6）：
//  1. tx1：只改 freeze_confirmed/at + 置 frozen_write_flag（解除钳制后必须整体重算）；
//  2. tx2：物化。失败**不回滚**确认（用户意图必须持久，物化是派生数据）。
func (f *FreezeService) Confirm(ctx context.Context, in *FreezeConfirmInput) (*FreezeConfirmResult, error) {
	if in == nil {
		return nil, apierr.New(apierr.CodeValidation, "请求体不能为空")
	}
	if len(in.TransactionIDs) > maxConfirmIDs {
		return nil, apierr.Newf(apierr.CodeValidation,
			"transaction_ids 最多 %d 个（本次 %d 个），请改用 batch_id 或分批确认",
			maxConfirmIDs, len(in.TransactionIDs))
	}
	ids := dedupStrings(in.TransactionIDs)
	batchID := strings.TrimSpace(in.BatchID)
	switch {
	case len(ids) == 0 && batchID == "" && !in.All:
		return nil, apierr.New(apierr.CodeValidation,
			"必须指定 transaction_ids / batch_id / all=true 三者之一")
	case len(ids) == 0 && batchID == "" && in.All && !in.Confirm:
		return nil, apierr.New(apierr.CodeValidation, "all=true 需同时传 confirm=true 二次确认")
	}

	now := timeutil.NowUnix()
	res := &FreezeConfirmResult{}
	var (
		where string
		args  []any
	)
	switch {
	case len(ids) > 0:
		where = `id IN ` + placeholders(len(ids))
		args = toAnyArgs(ids)
	case batchID != "":
		where = `import_batch_id = ?`
		args = []any{batchID}
	default:
		where = `1=1`
	}

	// ---- tx1：确认 + 置重算标志 ----
	err := f.s.WriteTx(ctx, func(tx *sql.Tx) error {
		if len(ids) > 0 {
			existing, cerr := f.tx.CountExistingIDsTx(tx, ids)
			if cerr != nil {
				return cerr
			}
			res.SkippedMissing = len(ids) - int(existing)
		}
		n, cerr := f.tx.ConfirmFreezeTx(tx, where, args, now)
		if cerr != nil {
			return cerr
		}
		res.Confirmed = int(n)
		if n > 0 {
			// 解除钳制后边界会前进 → 必须整表重算（EnsureFresh 见到 flag 即 ForceFull）
			if cerr = f.meta.SetTx(tx, "frozen_write_flag", "1"); cerr != nil {
				return cerr
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// ---- tx2：物化（失败不回滚确认）----
	if res.Confirmed > 0 {
		matRes, merr := f.mat.ForceFull(ctx, time.Now())
		if merr != nil {
			res.Materialized = false
			res.MaterializeError = merr.Error()
			slog.Error("确认冻结后物化失败（确认已持久，等待下次补偿）",
				"confirmed", res.Confirmed, "err", merr)
		} else {
			res.Materialized = true
			res.MaterializeRows = matRes.RowsProcessed
			res.MaterializeElapsedMS = matRes.ElapsedMS
		}
	} else {
		// 没有可确认的行 → 无需重算（幂等）
		res.Materialized = true
	}

	// ---- 收尾信息 ----
	pending, perr := f.tx.CountUnconfirmed()
	if perr != nil {
		return nil, perr
	}
	res.PendingRemaining = int(pending)
	last, lerr := f.balance.LastFrozenAt()
	if lerr != nil {
		return nil, lerr
	}
	res.NewFrozenAt = timeutil.FormatLocal(last)
	res.FreezeBoundary = timeutil.FormatLocal(f.balance.FreezeBoundary(now))
	slog.Info("确认冻结完成", "confirmed", res.Confirmed, "pending_remaining", res.PendingRemaining,
		"materialized", res.Materialized, "rows", res.MaterializeRows)
	return res, nil
}
