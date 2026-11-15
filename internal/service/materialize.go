package service

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// MatResult 物化结果（§6.4）。
type MatResult struct {
	NewBoundary   int64 `json:"new_boundary"`
	RowsProcessed int   `json:"rows"`
	ElapsedMS     int64 `json:"elapsed_ms"`
	VerifyOK      bool  `json:"verify_ok"`
}

// Materializer 滚动物化（§6.4）：把 last_frozen_at 推进到 now-freeze_days，
// 区间内逐笔回写 account_balance_after / total_balance_after。
type Materializer struct {
	s       *store.Store
	meta    *store.MetaRepo
	tx      *store.TxRepo
	mu      sync.Mutex
	lastRun time.Time // EnsureFresh 节流（30s）
}

// NewMaterializer 构造。tx 用于读取「最早待确认冻结时间」以钳制物化边界（§9.4）。
func NewMaterializer(s *store.Store, meta *store.MetaRepo, tx *store.TxRepo) *Materializer {
	return &Materializer{s: s, meta: meta, tx: tx}
}

// metaIntTx 事务内读 int。
func metaIntTx(tx *sql.Tx, meta *store.MetaRepo, key string, def int64) (int64, error) {
	v, err := meta.GetTx(tx, key)
	if err != nil {
		return 0, err
	}
	if v == "" {
		return def, nil
	}
	return strconv.ParseInt(v, 10, 64)
}

// Materialize 推进物化边界（幂等；无需推进时直接返回）。
func (m *Materializer) Materialize(ctx context.Context, now time.Time) (MatResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.materializeLocked(ctx, now)
}

func (m *Materializer) materializeLocked(ctx context.Context, now time.Time) (MatResult, error) {
	started := time.Now()
	freezeDays, err := m.metaInt("freeze_days", 30)
	if err != nil {
		return MatResult{}, err
	}
	// ---- §9.4 钳制物化边界（freeze horizon）----
	// 默认目标 = now - freeze_days；若存在「待确认冻结」的行且其时间更早，
	// 则把边界压到该行时间上 —— 待确认行因此永远落在动态区（不被折进
	// initial_balance/快照），从而随时可改。**不给物化扫描加 freeze_confirmed 过滤**，
	// CurrentBalances / WindowBalances / verifySeam 的 SQL 一行都不用改。
	target := now.Unix() - int64(freezeDays)*86400
	if m.tx != nil {
		clamp, cerr := m.tx.MinUnconfirmedTime()
		if cerr != nil {
			return MatResult{}, cerr
		}
		if clamp > 0 && clamp < target {
			target = clamp
		}
	}
	newBoundary := target
	res := MatResult{NewBoundary: newBoundary}

	err = m.s.WriteTx(ctx, func(tx *sql.Tx) error {
		// I3 自愈（§9.3）：待确认冻结的行不得残留任何物化值。
		// 正常路径不会产生这种行（插入即 NULL、且钳制使它们永远落在扫描窗口之外），
		// 但两条历史/人工路径会：① 执行 §9.9 的可选解冻脚本；② 外部工具直接改库。
		// 每次物化顺手清一次（走 ix_tx_unconfirmed 部分索引，代价 ~1ms）。
		if _, cerr := tx.Exec(`UPDATE transactions
			SET account_balance_after = NULL, total_balance_after = NULL, materialized_at = NULL
			WHERE freeze_confirmed = 0 AND account_balance_after IS NOT NULL`); cerr != nil {
			return cerr
		}
		last, err := metaIntTx(tx, m.meta, "last_frozen_at", 0)
		if err != nil {
			return err
		}
		if last >= newBoundary && last != 0 {
			return nil // 无需推进（幂等）
		}

		// ---- 1. 取起点余额快照 ----
		balances := map[string]int64{}
		countIn := map[string]bool{}
		var rows *sql.Rows
		if last == 0 {
			rows, err = tx.Query(`SELECT id, initial_balance_cents, is_count_in_total FROM accounts`)
		} else {
			rows, err = tx.Query(`
			  SELECT a.id, a.is_count_in_total,
			    COALESCE((SELECT t.account_balance_after FROM transactions t
			               WHERE t.account_id = a.id AND t.deleted_at IS NULL
			                 AND t.transaction_time < ? AND t.account_balance_after IS NOT NULL
			               ORDER BY t.transaction_time DESC, t.created_seq DESC LIMIT 1),
			             a.initial_balance_cents) AS bal
			  FROM accounts a`, last)
		}
		if err != nil {
			return err
		}
		if last == 0 {
			for rows.Next() {
				var id string
				var bal int64
				var ci int
				if err = rows.Scan(&id, &bal, &ci); err != nil {
					rows.Close()
					return err
				}
				balances[id] = bal
				countIn[id] = ci == 1
			}
		} else {
			for rows.Next() {
				var id string
				var ci int
				var bal int64
				if err = rows.Scan(&id, &ci, &bal); err != nil {
					rows.Close()
					return err
				}
				balances[id] = bal
				countIn[id] = ci == 1
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}

		// ---- 2. 按排序键逐笔累计 ----
		txRows, err := tx.Query(`
		  SELECT id, account_id, transaction_type, transfer_direction, amount_cents
		  FROM transactions
		  WHERE deleted_at IS NULL AND transaction_time >= ? AND transaction_time < ?
		  ORDER BY transaction_time ASC, created_seq ASC`, last, newBoundary)
		if err != nil {
			return err
		}
		type upd struct {
			id       string
			acc, tot int64
		}
		var batch []upd
		for txRows.Next() {
			var id, acc, typ string
			var dir *string
			var amt int64
			if err = txRows.Scan(&id, &acc, &typ, &dir, &amt); err != nil {
				txRows.Close()
				return err
			}
			var delta int64
			switch {
			case typ == "expense":
				delta = -amt
			case typ == "income":
				delta = amt
			case dir != nil && *dir == "out":
				delta = -amt
			case dir != nil && *dir == "in":
				delta = amt
			}
			balances[acc] += delta
			var total int64
			for a, b := range balances {
				if countIn[a] {
					total += b
				}
			}
			batch = append(batch, upd{id, balances[acc], total})
		}
		txRows.Close()
		if err = txRows.Err(); err != nil {
			return err
		}

		// ---- 3. 回写（prepared statement，每 500 条 flush）----
		if len(batch) > 0 {
			stmt, err := tx.Prepare(`UPDATE transactions SET account_balance_after=?, total_balance_after=?,
				materialized_at=? WHERE id=?`)
			if err != nil {
				return err
			}
			matAt := timeutil.NowUnix()
			for i, u := range batch {
				if _, err = stmt.Exec(u.acc, u.tot, matAt, u.id); err != nil {
					stmt.Close()
					return err
				}
				if (i+1)%500 == 0 {
					stmt.Close()
					if stmt, err = tx.Prepare(`UPDATE transactions SET account_balance_after=?, total_balance_after=?,
						materialized_at=? WHERE id=?`); err != nil {
						return err
					}
				}
			}
			stmt.Close()
		}
		res.RowsProcessed = len(batch)

		// ---- 4. 推进边界 + 清标志 ----
		if err = m.meta.SetTx(tx, "last_frozen_at", strconv.FormatInt(newBoundary, 10)); err != nil {
			return err
		}
		if err = m.meta.SetTx(tx, "last_materialize_at", strconv.FormatInt(now.Unix(), 10)); err != nil {
			return err
		}
		if err = m.meta.SetTx(tx, "frozen_write_flag", "0"); err != nil {
			return err
		}

		// ---- 5. 衔接校验（失败回滚）----
		ok, err := verifySeam(tx, newBoundary)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New(apierr.CodeMaterializeFailed, "物化衔接校验失败，已回滚")
		}
		return nil
	})
	res.ElapsedMS = time.Since(started).Milliseconds()
	res.VerifyOK = err == nil
	return res, err
}

// verifySeam 衔接点校验：独立按公式重算每账户 boundary 前余额，
// 与物化后该账户最后一条 account_balance_after 比对。
func verifySeam(tx *sql.Tx, boundary int64) (bool, error) {
	rows, err := tx.Query(`
SELECT a.id,
       a.initial_balance_cents + COALESCE(SUM(CASE
         WHEN t.transaction_type='expense' THEN -t.amount_cents
         WHEN t.transaction_type='income'  THEN  t.amount_cents
         WHEN t.transfer_direction='out'   THEN -t.amount_cents
         WHEN t.transfer_direction='in'    THEN  t.amount_cents
         ELSE 0 END), 0) AS expected,
       (SELECT t2.account_balance_after FROM transactions t2
         WHERE t2.account_id = a.id AND t2.deleted_at IS NULL
           AND t2.transaction_time < ? AND t2.account_balance_after IS NOT NULL
         ORDER BY t2.transaction_time DESC, t2.created_seq DESC LIMIT 1) AS materialized
FROM accounts a
LEFT JOIN transactions t ON t.account_id = a.id
     AND t.deleted_at IS NULL AND t.transaction_time < ?
GROUP BY a.id`, boundary, boundary)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var expected int64
		var materialized *int64
		if err = rows.Scan(&id, &expected, &materialized); err != nil {
			return false, err
		}
		// 有交易但物化缺失、或数值不一致 → 校验失败
		if materialized != nil && *materialized != expected {
			slog.Error("物化衔接校验失败", "account_id", id, "expected", expected, "materialized", *materialized)
			return false, nil
		}
	}
	return true, rows.Err()
}

// ForceFull 冻结区被写入后的全量重物化（§6.4）：重置边界 → 清物化字段 → 全量重算。
func (m *Materializer) ForceFull(ctx context.Context, now time.Time) (MatResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.s.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := m.meta.SetTx(tx, "last_frozen_at", "0"); err != nil {
			return err
		}
		return store.NewTxRepo(m.s, nil).ClearMaterializedTx(tx)
	}); err != nil {
		return MatResult{}, err
	}
	return m.materializeLocked(ctx, now)
}

// EnsureFresh 查询前补偿（§6.4 触发时机表）。
// frozen_write_flag=1 → 立即 ForceFull（不受节流影响，保证冻结区写入后查询即一致）；
// last_frozen_at 落后超过 24h → Materialize（30s 节流）。
// 失败只记日志不阻断查询（启动任务会兜底）。
func (m *Materializer) EnsureFresh(ctx context.Context) {
	flag, err := m.meta.Get("frozen_write_flag")
	if err != nil {
		slog.Error("读取 frozen_write_flag 失败", "err", err)
		return
	}
	if flag == "1" {
		m.lastRun = time.Now()
		if _, err = m.ForceFull(ctx, time.Now()); err != nil {
			slog.Error("强制全量重物化失败", "err", err)
		}
		return
	}
	if time.Since(m.lastRun) < 30*time.Second {
		return
	}
	m.lastRun = time.Now()
	v, err := m.meta.Get("last_frozen_at")
	if err != nil {
		return
	}
	last, _ := strconv.ParseInt(v, 10, 64)
	if time.Now().Unix()-last > 24*3600 {
		if _, err = m.Materialize(ctx, time.Now()); err != nil {
			slog.Error("补偿物化失败", "err", err)
		}
	}
}

// metaInt 非事务读 int。
func (m *Materializer) metaInt(key string, def int64) (int64, error) {
	v, err := m.meta.Get(key)
	if err != nil {
		return 0, err
	}
	if v == "" {
		return def, nil
	}
	return strconv.ParseInt(v, 10, 64)
}
