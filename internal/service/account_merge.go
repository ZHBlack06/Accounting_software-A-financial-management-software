package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// AccountMergeService 账户合并（需求 R5，ARCHITECTURE-V12 §2.5 / §3.5）。
//
// 两条入口：
//  1. AutoMergeOnUpgrade：v1.2 首次启动迁移时**自动**把所有账户并入「总账户」
//     （先自动备份数据库，再合并，写追溯日志；幂等，重复启动不重复建账户）。
//  2. Merge：手动 POST /api/accounts/merge（用户以后想再合并时使用）。
type AccountMergeService struct {
	s       *store.Store
	meta    *store.MetaRepo
	master  *store.MasterRepo
	views   *store.ViewManager
	balance *BalanceService
	mat     *Materializer
	backup  *BackupService
	dataDir string
}

// NewAccountMergeService 构造。
func NewAccountMergeService(s *store.Store, meta *store.MetaRepo, master *store.MasterRepo,
	views *store.ViewManager, balance *BalanceService, mat *Materializer,
	backup *BackupService, dataDir string) *AccountMergeService {
	return &AccountMergeService{
		s: s, meta: meta, master: master, views: views, balance: balance,
		mat: mat, backup: backup, dataDir: dataDir,
	}
}

// MergeTarget 合并目标账户（id 为空时按 name 查找/新建）。
type MergeTarget struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ValueType string `json:"value_type"`
}

// MergeRequest POST /api/accounts/merge 请求体（§2.5）。
type MergeRequest struct {
	SourceIDs     []string    `json:"source_ids"`
	Target        MergeTarget `json:"target"`
	DeleteSources bool        `json:"delete_sources"`
	Confirm       bool        `json:"confirm"`
}

// AccountMergeDetail 单个源账户合并前后的余额追溯信息。
type AccountMergeDetail struct {
	AccountID           string `json:"account_id"`
	Name                string `json:"name"`
	BalanceBeforeCents  int64  `json:"balance_before_cents"`
	InitialBalanceCents int64  `json:"initial_balance_cents"`
	TransactionCount    int64  `json:"transaction_count"`
}

// MergeResult POST /api/accounts/merge 响应（§2.5）。
type MergeResult struct {
	TargetAccountID     string               `json:"target_account_id"`
	CreatedTarget       bool                 `json:"created_target"`
	MergedAccounts      int                  `json:"merged_accounts"`
	MovedTransactions   int64                `json:"moved_transactions"`
	MovedTransferLegs   int64                `json:"moved_transfer_legs"`
	InitialBalanceCents int64                `json:"initial_balance_cents"`
	Rematerialized      bool                 `json:"rematerialized"`
	RematerializeRows   int                  `json:"rematerialize_rows"`
	Warnings            []string             `json:"warnings"`
	AccountDetails      []AccountMergeDetail `json:"account_details,omitempty"`
}

// Merge 把 source_ids 并入 target 并全量重算余额（单 WriteTx，顺序不可换，§3.5）。
func (s *AccountMergeService) Merge(ctx context.Context, req *MergeRequest) (*MergeResult, error) {
	if req == nil {
		return nil, apierr.New(apierr.CodeValidation, "请求体不能为空")
	}
	sources := dedupStrings(req.SourceIDs)
	if len(sources) == 0 {
		return nil, apierr.New(apierr.CodeValidation, "source_ids 不能为空")
	}
	// 不可逆操作守卫：删除源账户必须显式确认（P2-1）。
	if req.DeleteSources && !req.Confirm {
		return nil, apierr.New(apierr.CodeValidation,
			"删除源账户需确认：delete_sources=true 时必须同时传 confirm=true").
			WithDetails(map[string]any{"delete_sources": true, "confirm": false})
	}
	if req.Target.ID != "" {
		for _, id := range sources {
			if id == req.Target.ID {
				return nil, apierr.New(apierr.CodeValidation, "source_ids 不能包含目标账户")
			}
		}
	}

	res := &MergeResult{Warnings: []string{}, AccountDetails: []AccountMergeDetail{}}
	var (
		targetID string
		moved    int64
		movedLeg int64
		initSum  int64
	)

	err := s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		// 0) 目标账户：id 为空 → 按 name 查，无则建（value_type 默认 wallet，count_in_total=1）
		tid, created, err := ensureTargetAccountTx(tx, req.Target)
		if err != nil {
			return err
		}
		targetID = tid
		res.CreatedTarget = created

		// 校验源账户存在（且不是目标）
		existing, err := accountsByIDsTx(tx, sources)
		if err != nil {
			return err
		}
		if len(existing) != len(sources) {
			var missing []string
			for _, id := range sources {
				if _, ok := existing[id]; !ok {
					missing = append(missing, id)
				}
			}
			return apierr.New(apierr.CodeValidation, "source_ids 含不存在的账户").
				WithDetails(map[string]any{"missing": missing})
		}
		if _, ok := existing[targetID]; ok {
			return apierr.New(apierr.CodeValidation, "source_ids 不能包含目标账户")
		}

		// 合并前余额追溯（搬移前采集）
		for _, id := range sources {
			acc := existing[id]
			bal, err := accountBalanceTx(tx, id)
			if err != nil {
				return err
			}
			cnt, err := accountTxCountTx(tx, id)
			if err != nil {
				return err
			}
			res.AccountDetails = append(res.AccountDetails, AccountMergeDetail{
				AccountID: id, Name: acc.Name, BalanceBeforeCents: bal,
				InitialBalanceCents: acc.InitialBalanceCents, TransactionCount: cnt,
			})
			if acc.IsArchived == 1 {
				res.Warnings = append(res.Warnings, "账户「"+acc.Name+"」已归档，已一并合并")
			}
		}
		res.AccountDetails = append(res.AccountDetails, AccountMergeDetail{
			AccountID: targetID,
			Name:      existing2Name(existing, targetID, req.Target.Name),
		})

		// 1) 搬移转账对手账户（必须先于主账户，避免约束/语义错位）
		movedLeg, err = s.master.ReassignTransferCounterTx(tx, targetID, sources)
		if err != nil {
			return err
		}
		// 2) 搬移交易主账户（含软删行，保证回收站恢复后仍可算）
		moved, err = s.master.ReassignTransactionsTx(tx, targetID, sources)
		if err != nil {
			return err
		}
		// 3) 初始余额合并：目标 = 目标已有 + 各源之和；最早 initial_balance_time 为准
		srcInit, srcTime, err := s.master.AccountBalancesTx(tx, sources)
		if err != nil {
			return err
		}
		var tgtInit, tgtTime int64
		if err = tx.QueryRow(`SELECT initial_balance_cents, initial_balance_time FROM accounts WHERE id = ?`,
			targetID).Scan(&tgtInit, &tgtTime); err != nil {
			return err
		}
		initSum = tgtInit + srcInit
		initTime := earliestNonZero(tgtTime, srcTime)
		if _, err = tx.Exec(`UPDATE accounts SET initial_balance_cents = ?, initial_balance_time = ?,
			updated_at = ? WHERE id = ?`, initSum, initTime, timeutil.NowUnix(), targetID); err != nil {
			return err
		}
		// 4) 删除源账户（此时已无引用，ON DELETE RESTRICT 不会触发）
		if req.DeleteSources {
			if _, err = s.master.DeleteAccountsTx(tx, sources); err != nil {
				return err
			}
		}
		// 5) 默认/最近使用账户指向已删账户 → 重绑到目标
		if err = rebindAccountMetaTx(tx, s.meta, targetID, sources); err != nil {
			return err
		}
		// 6) 冻结区有写入（账户维度全量重算）→ 置标志，交由 ForceFull
		return s.meta.SetTx(tx, "frozen_write_flag", "1")
	})
	if err != nil {
		return nil, err
	}

	res.TargetAccountID = targetID
	res.MergedAccounts = len(sources)
	res.MovedTransactions = moved
	res.MovedTransferLegs = movedLeg
	res.InitialBalanceCents = initSum

	// 事务外重建视图（ViewManager 走 GORM 连接，不能在 WriteTx 内调用，单连接会自锁）
	for _, id := range sources {
		if err := s.views.DropAccountView(id); err != nil {
			slog.Warn("删除源账户视图失败", "account_id", id, "err", err)
		}
	}
	if err := s.views.CreateAccountView(targetID); err != nil {
		return nil, fmt.Errorf("重建目标账户视图失败: %w", err)
	}
	// 事务外全量重算余额（同步，返回 rows）
	if mr, err := s.mat.ForceFull(ctx, time.Now()); err != nil {
		res.Warnings = append(res.Warnings, "余额重算失败："+err.Error())
	} else {
		res.Rematerialized = true
		res.RematerializeRows = mr.RowsProcessed
	}
	slog.Info("账户合并完成", "target", targetID, "accounts", res.MergedAccounts,
		"moved_transactions", res.MovedTransactions, "moved_transfer_legs", res.MovedTransferLegs,
		"initial_balance_cents", res.InitialBalanceCents)
	return res, nil
}

// AutoMergeOnUpgrade v1.2 首次启动迁移：自动备份 → 全部账户并入「总账户」→ 写追溯日志。
//
// 幂等：meta.account_merge_v12_done = "1" 后不再执行；只有一个账户时直接置位。
// 用户明确要求「账户合并要自动做」（含冻结区仍有交易的账户照常合并），
// 因此这里不阻断，但会把每个账户合并前后的余额写进 data/logs/v12-account-merge.log。
func (s *AccountMergeService) AutoMergeOnUpgrade(ctx context.Context) error {
	done, err := s.meta.Get("account_merge_v12_done")
	if err != nil {
		return err
	}
	if done == "1" {
		return nil
	}
	ids, err := s.master.AllAccountIDs()
	if err != nil {
		return err
	}
	if len(ids) <= 1 {
		if len(ids) == 1 {
			if err = setMetaIfEmptyAccount(s.meta, ids[0]); err != nil {
				return err
			}
		}
		return s.meta.Set("account_merge_v12_done", "1")
	}

	// 先备份（用户要求：迁移前必须先备份；备份失败则本次不合并，下次启动重试）
	if s.backup != nil {
		info, berr := s.backup.Create(ctx)
		if berr != nil {
			slog.Error("v1.2 账户合并迁移：备份失败，本次跳过合并（下次启动重试）", "err", berr)
			return nil
		}
		slog.Info("v1.2 账户合并迁移：已创建迁移前备份", "file", info.File, "size_bytes", info.SizeBytes)
	}

	// 目标：已有「总账户」则复用，否则由 Merge 新建
	target := MergeTarget{Name: store.TotalAccountName, ValueType: "wallet"}
	if acc, ferr := s.master.FindAccountByName(store.TotalAccountName); ferr == nil && acc != nil {
		target.ID = acc.ID
		target.ValueType = acc.ValueType
	}
	var sources []string
	for _, id := range ids {
		if id != target.ID {
			sources = append(sources, id)
		}
	}
	if len(sources) == 0 {
		return s.meta.Set("account_merge_v12_done", "1")
	}

	res, merr := s.Merge(ctx, &MergeRequest{
		SourceIDs: sources, Target: target, DeleteSources: true, Confirm: true,
	})
	if merr != nil {
		slog.Error("v1.2 账户合并迁移失败（下次启动重试）", "err", merr)
		return nil
	}
	if err = s.writeMergeJournal(res); err != nil {
		slog.Warn("写账户合并追溯日志失败", "err", err)
	}
	if err = s.meta.Set("account_merge_v12_done", "1"); err != nil {
		return err
	}
	slog.Info("v1.2 账户合并迁移完成",
		"merged_accounts", res.MergedAccounts, "moved_transactions", res.MovedTransactions,
		"target", res.TargetAccountID, "initial_balance_cents", res.InitialBalanceCents)
	return nil
}

// writeMergeJournal 追加迁移日志（便于用户追溯每个账户合并前后的余额）。
func (s *AccountMergeService) writeMergeJournal(res *MergeResult) error {
	if s.dataDir == "" {
		return nil
	}
	dir := filepath.Join(s.dataDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s [v1.2 账户合并] 已合并 %d 个账户、%d 笔交易 → 总账户(%s)；转账对手行 %d 笔；合并后初始余额合计 %d 分\n",
		timeutil.FormatLocal(timeutil.NowUnix()), res.MergedAccounts, res.MovedTransactions,
		res.TargetAccountID, res.MovedTransferLegs, res.InitialBalanceCents)
	details := append([]AccountMergeDetail{}, res.AccountDetails...)
	sort.SliceStable(details, func(i, j int) bool { return details[i].AccountID < details[j].AccountID })
	for _, d := range details {
		if d.AccountID == res.TargetAccountID {
			continue
		}
		fmt.Fprintf(&b, "  - %s(%s)：合并前余额 %d 分（初始余额 %d 分，交易 %d 笔）\n",
			d.Name, d.AccountID, d.BalanceBeforeCents, d.InitialBalanceCents, d.TransactionCount)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(&b, "  ! %s\n", w)
	}
	f, err := os.OpenFile(filepath.Join(dir, "v12-account-merge.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return err
}

// ---------------- 事务内辅助 ----------------

// ensureTargetAccountTx 解析/创建目标账户，返回 (id, created, err)。
func ensureTargetAccountTx(tx *sql.Tx, t MergeTarget) (string, bool, error) {
	if t.ID != "" {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM accounts WHERE id = ?`, t.ID).Scan(&n); err != nil {
			return "", false, err
		}
		if n == 0 {
			return "", false, apierr.New(apierr.CodeValidation, "目标账户不存在")
		}
		return t.ID, false, nil
	}
	name := strings.TrimSpace(t.Name)
	if name == "" {
		name = store.TotalAccountName
	}
	var existing string
	err := tx.QueryRow(`SELECT id FROM accounts WHERE name = ? ORDER BY created_at LIMIT 1`, name).Scan(&existing)
	if err == nil {
		return existing, false, nil
	}
	if err != sql.ErrNoRows {
		return "", false, err
	}
	// 新建：优先用固定的 acc_total01，冲突则用短 ID
	id := store.TotalAccountID
	var n int
	if err = tx.QueryRow(`SELECT COUNT(1) FROM accounts WHERE id = ?`, id).Scan(&n); err != nil {
		return "", false, err
	}
	if n > 0 {
		id = ids.ShortID("acc_")
	}
	vt := t.ValueType
	if vt == "" {
		vt = "wallet"
	}
	now := timeutil.NowUnix()
	var maxSort int
	_ = tx.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM accounts`).Scan(&maxSort)
	if _, err = tx.Exec(`INSERT INTO accounts
		(id, name, alias, account_type, value_type, bank_name, card_tail, currency,
		 initial_balance_cents, initial_balance_time, is_count_in_total, sort_order,
		 is_archived, archived_at, remark, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, name, "", "asset", vt, "", "", "CNY", 0, 0, 1, maxSort+10, 0, nil, "", now, now); err != nil {
		return "", false, err
	}
	return id, true, nil
}

// accountsByIDsTx 事务内批量取账户（返回 id → 账户）。
func accountsByIDsTx(tx *sql.Tx, ids []string) (map[string]*store.Account, error) {
	out := map[string]*store.Account{}
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT id, name, value_type, initial_balance_cents, initial_balance_time,
		is_count_in_total, sort_order, is_archived FROM accounts WHERE id IN ` + placeholders(len(ids))
	rows, err := tx.Query(q, toAnyArgs(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a := &store.Account{}
		var vt string
		if err = rows.Scan(&a.ID, &a.Name, &vt, &a.InitialBalanceCents, &a.InitialBalanceTime,
			&a.IsCountInTotal, &a.SortOrder, &a.IsArchived); err != nil {
			return nil, err
		}
		a.ValueType = vt
		out[a.ID] = a
	}
	return out, rows.Err()
}

// accountBalanceTx 事务内计算账户当前余额（初始余额 + 未删除交易的带符号合计）。
func accountBalanceTx(tx *sql.Tx, accountID string) (int64, error) {
	var bal int64
	err := tx.QueryRow(`
SELECT a.initial_balance_cents + COALESCE((
  SELECT SUM(CASE WHEN t.transaction_type='expense' THEN -t.amount_cents
                  WHEN t.transaction_type='income'  THEN  t.amount_cents
                  WHEN t.transfer_direction='out'   THEN -t.amount_cents
                  WHEN t.transfer_direction='in'    THEN  t.amount_cents
                  ELSE 0 END)
  FROM transactions t WHERE t.account_id = a.id AND t.deleted_at IS NULL), 0)
FROM accounts a WHERE a.id = ?`, accountID).Scan(&bal)
	return bal, err
}

// accountTxCountTx 事务内统计账户交易笔数（主账户）。
func accountTxCountTx(tx *sql.Tx, accountID string) (int64, error) {
	var n int64
	err := tx.QueryRow(`SELECT COUNT(1) FROM transactions WHERE account_id = ?`, accountID).Scan(&n)
	return n, err
}

// rebindAccountMetaTx meta 里指向已删账户的键重绑到目标账户。
func rebindAccountMetaTx(tx *sql.Tx, meta *store.MetaRepo, targetID string, removed []string) error {
	for _, key := range []string{"default_account_id", "last_used_account_id"} {
		v, err := meta.GetTx(tx, key)
		if err != nil {
			return err
		}
		for _, id := range removed {
			if v == id {
				if err = meta.SetTx(tx, key, targetID); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// setMetaIfEmptyAccount 单账户库：默认/最近使用账户指向它。
func setMetaIfEmptyAccount(meta *store.MetaRepo, accountID string) error {
	for _, key := range []string{"default_account_id", "last_used_account_id"} {
		v, err := meta.Get(key)
		if err != nil {
			return err
		}
		if v == "" {
			if err := meta.Set(key, accountID); err != nil {
				return err
			}
		}
	}
	return nil
}

// existing2Name 目标账户名称（用于日志）。
func existing2Name(accounts map[string]*store.Account, id, fallback string) string {
	if a, ok := accounts[id]; ok {
		return a.Name
	}
	if fallback != "" {
		return fallback
	}
	return store.TotalAccountName
}

// earliestNonZero 返回两个时间戳中"最早的非零值"，都为零时返回 0。
func earliestNonZero(a, b int64) int64 {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// dedupStrings 去空去重（保持原顺序）。
func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// placeholders 返回 n 个以逗号分隔的 SQL 占位符，如 (?,?,?)。
func placeholders(n int) string {
	if n <= 0 {
		return "()"
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", n), ",") + ")"
}

// toAnyArgs []string → []any（database/sql 变参）。
func toAnyArgs(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
