package store

import (
	"database/sql"
	"fmt"

	"ledger/internal/pkg/timeutil"
)

// TxRepo 交易表存取（热点 SQL 手写，保证口径与 §6 一致）。
type TxRepo struct {
	s     *Store
	views *ViewManager
}

// NewTxRepo 构造。
func NewTxRepo(s *Store, views *ViewManager) *TxRepo { return &TxRepo{s: s, views: views} }

// Views 返回视图管理器（统计/导入等模块复用账本规则展开）。
func (r *TxRepo) Views() *ViewManager { return r.views }

const txColumns = `id, created_seq, transaction_time, local_date, transaction_type, amount_cents,
	currency, fx_rate, account_id, transfer_direction, transfer_group_id, counter_account_id,
	category_id, sub_category_id, merchant_id, merchant_text, region_id,
	counterparty_name, counterparty_account, remark, ocr_raw_text, screenshot_id,
	is_adjustment, adjustment_type, related_transaction_id, source, external_id,
	account_balance_after, total_balance_after, materialized_at, deleted_at, created_at, updated_at,
	time_precision, freeze_confirmed, freeze_confirmed_at, import_batch_id`

// txColumnCount txColumns 的列数（含 v1.2 的 time_precision 与 §9 的冻结三列），
// INSERT 占位符必须与之相同。
const txColumnCount = 37

// insertPlaceholders INSERT 的 37 个占位符。
const insertPlaceholders = "(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"

// selectTxColumns 列表查询用列（带别名前缀 t.）。
const selectTxColumns = `t.id, t.created_seq, t.transaction_time, t.local_date, t.transaction_type,
	t.amount_cents, t.currency, t.fx_rate, t.account_id, t.transfer_direction, t.transfer_group_id,
	t.counter_account_id, t.category_id, t.sub_category_id, t.merchant_id, t.merchant_text,
	t.region_id, t.counterparty_name, t.counterparty_account, t.remark, t.ocr_raw_text,
	t.screenshot_id, t.is_adjustment, t.adjustment_type, t.related_transaction_id, t.source,
	t.external_id, t.account_balance_after, t.total_balance_after, t.materialized_at, t.deleted_at,
	t.created_at, t.updated_at, t.time_precision, t.freeze_confirmed, t.freeze_confirmed_at,
	t.import_batch_id`

// scanner 兼容 *sql.Row / *sql.Rows。
type scanner interface {
	Scan(dest ...any) error
}

func scanTx(sc scanner) (*Transaction, error) {
	var t Transaction
	err := sc.Scan(
		&t.ID, &t.CreatedSeq, &t.TransactionTime, &t.LocalDate, &t.TransactionType, &t.AmountCents,
		&t.Currency, &t.FxRate, &t.AccountID, &t.TransferDirection, &t.TransferGroupID, &t.CounterAccountID,
		&t.CategoryID, &t.SubCategoryID, &t.MerchantID, &t.MerchantText, &t.RegionID,
		&t.CounterpartyName, &t.CounterpartyAccount, &t.Remark, &t.OCRRawText, &t.ScreenshotID,
		&t.IsAdjustment, &t.AdjustmentType, &t.RelatedTransactionID, &t.Source, &t.ExternalID,
		&t.AccountBalanceAfter, &t.TotalBalanceAfter, &t.MaterializedAt, &t.DeletedAt, &t.CreatedAt, &t.UpdatedAt,
		&t.TimePrecision, &t.FreezeConfirmed, &t.FreezeConfirmedAt, &t.ImportBatchID)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetByID 取单条交易；不存在返回 (nil, nil)。
func (r *TxRepo) GetByID(id string) (*Transaction, error) {
	t, err := scanTx(r.s.SQL.QueryRow(`SELECT `+txColumns+` FROM transactions WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// GetByIDTx 事务内取单条（含软删行，用于恢复/校验）。
func (r *TxRepo) GetByIDTx(tx *sql.Tx, id string) (*Transaction, error) {
	t, err := scanTx(tx.QueryRow(`SELECT `+txColumns+` FROM transactions WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// GetByTransferGroup 取转账组全部行（out/in）。
func (r *TxRepo) GetByTransferGroup(groupID string) ([]*Transaction, error) {
	rows, err := r.s.SQL.Query(`SELECT `+txColumns+` FROM transactions
		WHERE transfer_group_id=? ORDER BY created_seq ASC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Transaction
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetAdjustments 取 related_transaction_id = id 的调整项。
func (r *TxRepo) GetAdjustments(id string) ([]*Transaction, error) {
	rows, err := r.s.SQL.Query(`SELECT `+txColumns+` FROM transactions
		WHERE related_transaction_id=? AND deleted_at IS NULL ORDER BY created_seq ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Transaction
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// InsertTx 事务内插入交易行（ID/created_seq 由调用方分配）。
func (r *TxRepo) InsertTx(tx *sql.Tx, t *Transaction) error {
	now := timeutil.NowUnix()
	if t.CreatedAt == 0 {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.Currency == "" {
		t.Currency = "CNY"
	}
	if t.FxRate == 0 {
		t.FxRate = 1
	}
	if t.Source == "" {
		t.Source = "manual"
	}
	if t.TimePrecision == "" {
		t.TimePrecision = "second"
	}
	// 语义默认：只有调用方**显式**标记 FreezeSpecified 时才尊重 FreezeConfirmed=0；
	// 未显式指定的行（调整分录、直接构造的 Transaction）一律按「已冻结」写 1，
	// 与 §9.2「DEFAULT 1 覆盖所有历史行与手工录入」一致。
	// 曾经写成 `if t.FreezeConfirmed != 0` 会把 Go 零值 0 当成「待确认」，
	// 导致 adjust 生成的分录全被标成待确认（P2-7）。
	if !t.FreezeSpecified {
		t.FreezeConfirmed = 1
	}
	if t.FreezeConfirmed == 0 {
		// I3（§9.3）：待确认冻结的行永远不参与物化 —— 插入时就必须把物化值清空，
		// 否则会带着「上一世」的余额进入动态区（re-import / 复制行场景）。
		t.AccountBalanceAfter = nil
		t.TotalBalanceAfter = nil
		t.MaterializedAt = nil
	}
	_, err := tx.Exec(`INSERT INTO transactions (`+txColumns+`) VALUES `+insertPlaceholders,
		t.ID, t.CreatedSeq, t.TransactionTime, t.LocalDate, t.TransactionType, t.AmountCents,
		t.Currency, t.FxRate, t.AccountID, t.TransferDirection, t.TransferGroupID, t.CounterAccountID,
		t.CategoryID, t.SubCategoryID, t.MerchantID, t.MerchantText, t.RegionID,
		t.CounterpartyName, t.CounterpartyAccount, t.Remark, t.OCRRawText, t.ScreenshotID,
		t.IsAdjustment, t.AdjustmentType, t.RelatedTransactionID, t.Source, t.ExternalID,
		t.AccountBalanceAfter, t.TotalBalanceAfter, t.MaterializedAt, t.DeletedAt, t.CreatedAt, t.UpdatedAt,
		t.TimePrecision, t.FreezeConfirmed, t.FreezeConfirmedAt, t.ImportBatchID)
	return err
}

// UpdateMutableTx 事务内更新动态区可改字段（不含物化字段/软删字段）。
func (r *TxRepo) UpdateMutableTx(tx *sql.Tx, t *Transaction) error {
	t.UpdatedAt = timeutil.NowUnix()
	_, err := tx.Exec(`UPDATE transactions SET
		transaction_time=?, local_date=?, amount_cents=?, currency=?, fx_rate=?,
		account_id=?, category_id=?, sub_category_id=?, merchant_id=?, merchant_text=?,
		region_id=?, counterparty_name=?, counterparty_account=?, remark=?, updated_at=?
		WHERE id=?`,
		t.TransactionTime, t.LocalDate, t.AmountCents, t.Currency, t.FxRate,
		t.AccountID, t.CategoryID, t.SubCategoryID, t.MerchantID, t.MerchantText,
		t.RegionID, t.CounterpartyName, t.CounterpartyAccount, t.Remark, t.UpdatedAt, t.ID)
	return err
}

// SoftDeleteTx 软删除。
func (r *TxRepo) SoftDeleteTx(tx *sql.Tx, id string, now int64) error {
	_, err := tx.Exec(`UPDATE transactions SET deleted_at=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		now, now, id)
	return err
}

// RestoreTx 恢复软删除。
func (r *TxRepo) RestoreTx(tx *sql.Tx, id string, now int64) error {
	_, err := tx.Exec(`UPDATE transactions SET deleted_at=NULL, updated_at=? WHERE id=?`, now, id)
	return err
}

// PurgeTx 物理删除（标签/明细经 FK CASCADE 清理）。
func (r *TxRepo) PurgeTx(tx *sql.Tx, id string) error {
	if _, err := tx.Exec(`DELETE FROM transaction_tags WHERE transaction_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM transaction_items WHERE transaction_id=?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM transactions WHERE id=?`, id)
	return err
}

// ReplaceTagsTx 全量替换交易标签（触发器维护 use_count）。
func (r *TxRepo) ReplaceTagsTx(tx *sql.Tx, txID string, tagIDs []string) error {
	if _, err := tx.Exec(`DELETE FROM transaction_tags WHERE transaction_id=?`, txID); err != nil {
		return err
	}
	for _, tid := range tagIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO transaction_tags(transaction_id, tag_id) VALUES(?,?)`,
			txID, tid); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceItemsTx 全量替换交易明细（discount_cents 为生成列，不写）。
func (r *TxRepo) ReplaceItemsTx(tx *sql.Tx, txID string, items []TransactionItem) error {
	if _, err := tx.Exec(`DELETE FROM transaction_items WHERE transaction_id=?`, txID); err != nil {
		return err
	}
	now := timeutil.NowUnix()
	for _, it := range items {
		if _, err := tx.Exec(`INSERT INTO transaction_items
			(transaction_id, name, sub_category_id, original_amount_cents, paid_amount_cents,
			 discount_reason, sort_order, created_at) VALUES(?,?,?,?,?,?,?,?)`,
			txID, it.Name, it.SubCategoryID, it.OriginalAmountCents, it.PaidAmountCents,
			it.DiscountReason, it.SortOrder, now); err != nil {
			return err
		}
	}
	return nil
}

// ItemsFor 批量取明细。
func (r *TxRepo) ItemsFor(txIDs []string) (map[string][]TransactionItem, error) {
	out := map[string][]TransactionItem{}
	if len(txIDs) == 0 {
		return out, nil
	}
	rows, err := r.s.SQL.Query(`SELECT id, transaction_id, name, sub_category_id,
		original_amount_cents, paid_amount_cents, discount_cents, discount_reason, sort_order, created_at
		FROM transaction_items WHERE transaction_id IN `+placeholders(len(txIDs))+
		` ORDER BY transaction_id, sort_order, id`, toAny(txIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it TransactionItem
		if err = rows.Scan(&it.ID, &it.TransactionID, &it.Name, &it.SubCategoryID,
			&it.OriginalAmountCents, &it.PaidAmountCents, &it.DiscountCents,
			&it.DiscountReason, &it.SortOrder, &it.CreatedAt); err != nil {
			return nil, err
		}
		out[it.TransactionID] = append(out[it.TransactionID], it)
	}
	return out, rows.Err()
}

// TagIDsFor 批量取交易标签 ID。
func (r *TxRepo) TagIDsFor(txIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(txIDs) == 0 {
		return out, nil
	}
	rows, err := r.s.SQL.Query(`SELECT transaction_id, tag_id FROM transaction_tags
		WHERE transaction_id IN `+placeholders(len(txIDs)), toAny(txIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var txID, tagID string
		if err = rows.Scan(&txID, &tagID); err != nil {
			return nil, err
		}
		out[txID] = append(out[txID], tagID)
	}
	return out, rows.Err()
}

// TxSummary 全量聚合（不受分页影响）。
type TxSummary struct {
	IncomeCents        int64 `json:"income_cents"`
	ExpenseCents       int64 `json:"expense_cents"`
	NetCents           int64 `json:"net_cents"`
	AdjustmentNetCents int64 `json:"adjustment_net_cents"`
	Count              int64 `json:"count"`
}

// ListTransactions 分页列表 + total + summary（summary 为过滤条件下全量聚合）。
func (r *TxRepo) ListTransactions(q *TxQuery, categoryChildren func(string) ([]string, error)) ([]*Transaction, int64, *TxSummary, error) {
	where, args, err := BuildTxWhere(q, "t", r.views, categoryChildren)
	if err != nil {
		return nil, 0, nil, err
	}

	// summary（走 v_transactions_signed 保证口径一致；deleted 过滤在视图内，回收站查询时改用原表）
	sumTable := "v_transactions_signed t"
	sumWhere := where
	if q.IncludeDeleted {
		// 视图已过滤 deleted_at IS NULL；回收站/含删除查询用原表 + 手写符号列
		sumTable = "transactions t"
		sumWhere = where
	}
	var sum TxSummary
	var adjExpr string
	if q.IncludeDeleted {
		adjExpr = `COALESCE(SUM(CASE WHEN t.is_adjustment=1 THEN
			CASE WHEN t.transaction_type='expense' THEN -t.amount_cents
			     WHEN t.transaction_type='income' THEN t.amount_cents
			     WHEN t.transfer_direction='out' THEN -t.amount_cents
			     WHEN t.transfer_direction='in' THEN t.amount_cents ELSE 0 END ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN t.transaction_type='income' AND t.is_adjustment=0 THEN t.amount_cents ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN t.transaction_type='expense' AND t.is_adjustment=0 THEN t.amount_cents ELSE 0 END),0)`
	} else {
		adjExpr = `COALESCE(SUM(t.adjustment_signed_cents),0),
			COALESCE(SUM(t.stat_income_cents),0),
			COALESCE(SUM(t.stat_expense_cents),0)`
	}
	err = r.s.SQL.QueryRow(`SELECT `+adjExpr+`, COUNT(1) FROM `+sumTable+` WHERE `+sumWhere, args...).
		Scan(&sum.AdjustmentNetCents, &sum.IncomeCents, &sum.ExpenseCents, &sum.Count)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("summary 查询失败: %w", err)
	}
	sum.NetCents = sum.IncomeCents - sum.ExpenseCents

	// total 与列表（原表，含软删标识）
	var total int64
	if err = r.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions t WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, nil, err
	}

	page := q.Page
	if page < 1 {
		page = 1
	}
	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}
	rows, err := r.s.SQL.Query(`SELECT `+selectTxColumns+` FROM transactions t WHERE `+where+
		` ORDER BY `+OrderClause(q.Sort)+` LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, nil, err
	}
	defer rows.Close()
	var items []*Transaction
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, 0, nil, err
		}
		items = append(items, t)
	}
	return items, total, &sum, rows.Err()
}

// AccountBalanceRow 账户余额行。
type AccountBalanceRow struct {
	AccountID      string
	IsCountInTotal int
	BalanceCents   int64
}

// CurrentBalances 当前余额（物化快照 + 动态区累计，§6.5 查询一）。last 为 meta.last_frozen_at。
func (r *TxRepo) CurrentBalances(last int64) ([]AccountBalanceRow, error) {
	rows, err := r.s.SQL.Query(`
SELECT a.id, a.is_count_in_total,
  COALESCE((
    SELECT t.account_balance_after FROM transactions t
     WHERE t.account_id = a.id AND t.deleted_at IS NULL
       AND t.transaction_time < ? AND t.account_balance_after IS NOT NULL
     ORDER BY t.transaction_time DESC, t.created_seq DESC LIMIT 1
  ), a.initial_balance_cents)
  + COALESCE((
    SELECT SUM(CASE
      WHEN x.transaction_type = 'expense' THEN -x.amount_cents
      WHEN x.transaction_type = 'income'  THEN  x.amount_cents
      WHEN x.transfer_direction = 'out'   THEN -x.amount_cents
      WHEN x.transfer_direction = 'in'    THEN  x.amount_cents
      ELSE 0 END)
      FROM transactions x
     WHERE x.account_id = a.id AND x.deleted_at IS NULL AND x.transaction_time >= ?
  ), 0) AS balance_cents
FROM accounts a
ORDER BY a.sort_order, a.created_at`, last, last)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountBalanceRow
	for rows.Next() {
		var row AccountBalanceRow
		if err = rows.Scan(&row.AccountID, &row.IsCountInTotal, &row.BalanceCents); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// WindowBalance 动态区逐笔余额。
type WindowBalance struct {
	AccountBalanceAfter int64
	TotalBalanceAfter   int64
}

// WindowBalances 动态区（t >= last）全部交易的窗口余额（§6.5 查询二/三）。
// 返回 txID → (account_balance_after, total_balance_after)。
func (r *TxRepo) WindowBalances(last int64) (map[string]WindowBalance, error) {
	accRows, err := r.s.SQL.Query(`
WITH base AS (
  SELECT a.id AS account_id,
    COALESCE((SELECT t.account_balance_after FROM transactions t
              WHERE t.account_id=a.id AND t.deleted_at IS NULL
                AND t.transaction_time < ? AND t.account_balance_after IS NOT NULL
              ORDER BY t.transaction_time DESC, t.created_seq DESC LIMIT 1),
             a.initial_balance_cents) AS base_cents
  FROM accounts a
), dyn AS (
  SELECT x.id, x.account_id, x.transaction_time, x.created_seq,
    CASE WHEN x.transaction_type='expense' THEN -x.amount_cents
         WHEN x.transaction_type='income'  THEN  x.amount_cents
         WHEN x.transfer_direction='out'   THEN -x.amount_cents
         WHEN x.transfer_direction='in'    THEN  x.amount_cents
         ELSE 0 END AS delta_cents
  FROM transactions x
  WHERE x.deleted_at IS NULL AND x.transaction_time >= ?
)
SELECT d.id,
       b.base_cents + SUM(d.delta_cents) OVER (
         PARTITION BY d.account_id ORDER BY d.transaction_time, d.created_seq
         ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS account_balance_after
FROM dyn d JOIN base b ON b.account_id = d.account_id`, last, last)
	if err != nil {
		return nil, err
	}
	defer accRows.Close()
	out := map[string]WindowBalance{}
	for accRows.Next() {
		var id string
		var bal int64
		if err = accRows.Scan(&id, &bal); err != nil {
			return nil, err
		}
		out[id] = WindowBalance{AccountBalanceAfter: bal}
	}
	if err = accRows.Err(); err != nil {
		return nil, err
	}

	totRows, err := r.s.SQL.Query(`
WITH base AS (
  SELECT COALESCE(SUM(
    COALESCE((SELECT t.account_balance_after FROM transactions t
              WHERE t.account_id=a.id AND t.deleted_at IS NULL
                AND t.transaction_time < ? AND t.account_balance_after IS NOT NULL
              ORDER BY t.transaction_time DESC, t.created_seq DESC LIMIT 1),
             a.initial_balance_cents)), 0) AS base_total
  FROM accounts a WHERE a.is_count_in_total = 1
), dyn AS (
  SELECT x.id, x.transaction_time, x.created_seq,
    CASE WHEN a.is_count_in_total = 0 THEN 0
         WHEN x.transaction_type='expense' THEN -x.amount_cents
         WHEN x.transaction_type='income'  THEN  x.amount_cents
         WHEN x.transfer_direction='out'   THEN -x.amount_cents
         WHEN x.transfer_direction='in'    THEN  x.amount_cents
         ELSE 0 END AS delta_cents
  FROM transactions x JOIN accounts a ON a.id = x.account_id
  WHERE x.deleted_at IS NULL AND x.transaction_time >= ?
)
SELECT d.id, (SELECT base_total FROM base) + SUM(d.delta_cents) OVER (
         ORDER BY d.transaction_time, d.created_seq
         ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS total_balance_after
FROM dyn d`, last, last)
	if err != nil {
		return nil, err
	}
	defer totRows.Close()
	for totRows.Next() {
		var id string
		var tot int64
		if err = totRows.Scan(&id, &tot); err != nil {
			return nil, err
		}
		wb := out[id]
		wb.TotalBalanceAfter = tot
		out[id] = wb
	}
	return out, totRows.Err()
}

// FindDuplicate 组合去重（§6.8）。返回 (命中交易, 命中规则, error)；未命中返回 (nil, "", nil)。
func (r *TxRepo) FindDuplicate(cand *Transaction) (*Transaction, string, error) {
	// 规则 1：19 位 ID 相同
	if cand.ID != "" {
		t, err := scanTx(r.s.SQL.QueryRow(`SELECT `+txColumns+` FROM transactions
			WHERE id = ? AND deleted_at IS NULL`, cand.ID))
		if err == nil {
			return t, "id", nil
		}
		if err != sql.ErrNoRows {
			return nil, "", err
		}
	}
	// 规则 2：(transaction_time, amount_cents, account_id, type, merchant) 相同；转账不参与
	if cand.TransactionType != "transfer" {
		merchantID := ""
		if cand.MerchantID != nil {
			merchantID = *cand.MerchantID
		}
		t, err := scanTx(r.s.SQL.QueryRow(`SELECT `+txColumns+` FROM transactions
			WHERE deleted_at IS NULL AND transaction_time = ? AND amount_cents = ?
			  AND account_id = ? AND transaction_type = ?
			  AND IFNULL(merchant_id,'') = ? AND IFNULL(merchant_text,'') = ?
			ORDER BY created_seq ASC LIMIT 1`,
			cand.TransactionTime, cand.AmountCents, cand.AccountID, cand.TransactionType,
			merchantID, cand.MerchantText))
		if err == nil {
			return t, "time_amount_account_merchant", nil
		}
		if err != sql.ErrNoRows {
			return nil, "", err
		}
	}
	// 规则 3（导入专用）：external_id 相同
	if cand.ExternalID != "" {
		t, err := scanTx(r.s.SQL.QueryRow(`SELECT `+txColumns+` FROM transactions
			WHERE external_id = ? AND deleted_at IS NULL`, cand.ExternalID))
		if err == nil {
			return t, "external_id", nil
		}
		if err != sql.ErrNoRows {
			return nil, "", err
		}
	}
	return nil, "", nil
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// FindTxByExternalID 按 external_id 查已存在交易（导入去重用）。
func (s *Store) FindTxByExternalID(externalID string) (*Transaction, error) {
	if externalID == "" {
		return nil, nil
	}
	t, err := scanTx(s.SQL.QueryRow(`SELECT `+txColumns+` FROM transactions
		WHERE external_id = ? AND deleted_at IS NULL`, externalID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// HasAnyTransaction 判断是否存在任何交易（含软删）。
func (r *TxRepo) HasAnyTransaction() (bool, error) {
	var n int
	err := r.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions`).Scan(&n)
	return n > 0, err
}

// SetTransactionTimeForTest 仅供测试/演示脚本使用：直接改交易时间。
func (r *TxRepo) SetTransactionTimeForTest(id string, unix int64) error {
	_, err := r.s.SQL.Exec(`UPDATE transactions SET transaction_time=?, local_date=? WHERE id=?`,
		unix, timeutil.LocalDate(unix), id)
	return err
}

// MaterializeBatchUpdate 物化回写（复用 prepared statement 由调用方控制）。
func (r *TxRepo) MaterializeBatchUpdate(tx *sql.Tx, id string, accBal, totBal, matAt int64) error {
	_, err := tx.Exec(`UPDATE transactions SET account_balance_after=?, total_balance_after=?,
		materialized_at=? WHERE id=?`, accBal, totBal, matAt, id)
	return err
}

// ClearMaterializedTx 清空物化字段（ForceFull 用）。
func (r *TxRepo) ClearMaterializedTx(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE transactions SET account_balance_after=NULL, total_balance_after=NULL,
		materialized_at=NULL`)
	return err
}

// FindSoftDuplicate 软重复查询（§4.6 L2）：同一天、同金额、同账户、同商户文本、同类型的
// 最早一笔未删除交易。仅用于提示，不默认跳过。
func (r *TxRepo) FindSoftDuplicate(localDate string, amountCents int64, accountID, merchantText, typ string) (*Transaction, error) {
	t, err := scanTx(r.s.SQL.QueryRow(`SELECT `+txColumns+` FROM transactions
		WHERE deleted_at IS NULL AND local_date = ? AND amount_cents = ? AND account_id = ?
		  AND transaction_type = ? AND IFNULL(merchant_text,'') = ?
		ORDER BY created_seq ASC LIMIT 1`,
		localDate, amountCents, accountID, typ, merchantText))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// FindDuplicateTx FindDuplicate 的事务内版本（CreateBatch 必须复用，§4.8：
// 不允许在 batch 内新开连接 —— SQLite 单写者 + WriteTx 单连接约束）。
func (r *TxRepo) FindDuplicateTx(tx *sql.Tx, cand *Transaction) (*Transaction, string, error) {
	if cand.ID != "" {
		t, err := scanTx(tx.QueryRow(`SELECT `+txColumns+` FROM transactions
			WHERE id = ? AND deleted_at IS NULL`, cand.ID))
		if err == nil {
			return t, "id", nil
		}
		if err != sql.ErrNoRows {
			return nil, "", err
		}
	}
	if cand.TransactionType != "transfer" {
		merchantID := ""
		if cand.MerchantID != nil {
			merchantID = *cand.MerchantID
		}
		t, err := scanTx(tx.QueryRow(`SELECT `+txColumns+` FROM transactions
			WHERE deleted_at IS NULL AND transaction_time = ? AND amount_cents = ?
			  AND account_id = ? AND transaction_type = ?
			  AND IFNULL(merchant_id,'') = ? AND IFNULL(merchant_text,'') = ?
			ORDER BY created_seq ASC LIMIT 1`,
			cand.TransactionTime, cand.AmountCents, cand.AccountID, cand.TransactionType,
			merchantID, cand.MerchantText))
		if err == nil {
			return t, "time_amount_account_merchant", nil
		}
		if err != sql.ErrNoRows {
			return nil, "", err
		}
	}
	if cand.ExternalID != "" {
		t, err := scanTx(tx.QueryRow(`SELECT `+txColumns+` FROM transactions
			WHERE external_id = ? AND deleted_at IS NULL`, cand.ExternalID))
		if err == nil {
			return t, "external_id", nil
		}
		if err != sql.ErrNoRows {
			return nil, "", err
		}
	}
	return nil, "", nil
}

// ExistsExternalIDTx 事务内判重（只查 external_id，导入去重快路径）。
func (r *TxRepo) ExistsExternalIDTx(tx *sql.Tx, externalID string) (bool, error) {
	if externalID == "" {
		return false, nil
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM transactions WHERE external_id = ?`,
		externalID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetTimePrecisionTx 事务内设置行级时间精度（导入后修正用）。
func (r *TxRepo) SetTimePrecisionTx(tx *sql.Tx, id, precision string) error {
	_, err := tx.Exec(`UPDATE transactions SET time_precision = ? WHERE id = ?`, precision, id)
	return err
}

// MinUnconfirmedTime 最早「待确认冻结」行的 transaction_time（UTC 秒）。
// 无待确认行返回 0。走部分索引 ix_tx_unconfirmed，空闲时成本 ≈ 0（§9.4 钳制边界用）。
func (r *TxRepo) MinUnconfirmedTime() (int64, error) {
	var v sql.NullInt64
	if err := r.s.SQL.QueryRow(`SELECT MIN(transaction_time) FROM transactions
		WHERE freeze_confirmed = 0 AND deleted_at IS NULL`).Scan(&v); err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

// CountUnconfirmed 待确认行数（verify 的 pending_rows 用）。
func (r *TxRepo) CountUnconfirmed() (int64, error) {
	var n int64
	err := r.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions
		WHERE freeze_confirmed = 0 AND deleted_at IS NULL`).Scan(&n)
	return n, err
}

// ConfirmFreezeTx 事务内把待确认行置为已确认（条件由 where 片段给出，形如
// "id IN (?,?)" / "import_batch_id = ?" / "1=1"），返回受影响行数。
func (r *TxRepo) ConfirmFreezeTx(tx *sql.Tx, where string, args []any, now int64) (int64, error) {
	res, err := tx.Exec(`UPDATE transactions SET freeze_confirmed = 1, freeze_confirmed_at = ?,
		updated_at = ? WHERE freeze_confirmed = 0 AND deleted_at IS NULL AND `+where,
		append([]any{now, now}, args...)...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountExistingIDsTx 事务内统计给定 id 中真实存在的行数（确认端点算 skipped_missing）。
func (r *TxRepo) CountExistingIDsTx(tx *sql.Tx, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var n int64
	q := `SELECT COUNT(1) FROM transactions WHERE id IN ` + placeholders(len(ids))
	if err := tx.QueryRow(q, toAny(ids)...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
