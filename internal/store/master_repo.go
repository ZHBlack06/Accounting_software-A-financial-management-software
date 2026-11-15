package store

import (
	"database/sql"
	"errors"
	"strings"

	"gorm.io/gorm"

	"ledger/internal/pkg/timeutil"
)

// MasterRepo 主数据（账户/分类/标签/商户/地区/账本）模型级读写。
type MasterRepo struct {
	s *Store
}

// NewMasterRepo 构造。
func NewMasterRepo(s *Store) *MasterRepo { return &MasterRepo{s: s} }

// ---------------- accounts ----------------

// ListAccounts 按 sort_order 排序列出账户。
func (r *MasterRepo) ListAccounts(includeArchived bool) ([]Account, error) {
	q := r.s.G.Order("sort_order, created_at")
	if !includeArchived {
		q = q.Where("is_archived = 0")
	}
	var out []Account
	return out, q.Find(&out).Error
}

// GetAccount 按 ID 取账户。
func (r *MasterRepo) GetAccount(id string) (*Account, error) {
	var a Account
	if err := r.s.G.Where("id = ?", id).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// CountAccountTransactions 统计账户全部交易（含软删），删除前校验用。
func (r *MasterRepo) CountAccountTransactions(accountID string) (int64, error) {
	var n int64
	err := r.s.G.Model(&Transaction{}).Where("account_id = ? OR counter_account_id = ?", accountID, accountID).
		Count(&n).Error
	return n, err
}

// CountFrozenAccountTransactions 统计账户冻结区交易（改初始余额前校验用，T03.5）。
func (r *MasterRepo) CountFrozenAccountTransactions(accountID string, freezeBoundary int64) (int64, error) {
	var n int64
	err := r.s.G.Model(&Transaction{}).
		Where("account_id = ? AND transaction_time < ? AND deleted_at IS NULL", accountID, freezeBoundary).
		Count(&n).Error
	return n, err
}

// LastAccountTransactionTime 最近交易时间（未软删）。
func (r *MasterRepo) LastAccountTransactionTime(accountID string) (*int64, error) {
	var t *int64
	err := r.s.G.Model(&Transaction{}).
		Select("MAX(transaction_time)").
		Where("account_id = ? AND deleted_at IS NULL", accountID).
		Scan(&t).Error
	return t, err
}

// ---------------- categories ----------------

// ListCategories 可选按 transaction_type 过滤（type 同时放行 'both'）。
func (r *MasterRepo) ListCategories(txType string, includeArchived bool) ([]Category, error) {
	q := r.s.G.Order("level, sort_order, created_at")
	if !includeArchived {
		q = q.Where("is_archived = 0")
	}
	if txType != "" {
		q = q.Where("transaction_type IN (?, 'both')", txType)
	}
	var out []Category
	return out, q.Find(&out).Error
}

// GetCategory 按 ID 取分类。
func (r *MasterRepo) GetCategory(id string) (*Category, error) {
	var c Category
	if err := r.s.G.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// CategoryInUse 被交易引用或存在子分类时不可删。
func (r *MasterRepo) CategoryInUse(id string) (bool, error) {
	var n int64
	if err := r.s.G.Model(&Transaction{}).
		Where("category_id = ? OR sub_category_id = ?", id, id).Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if err := r.s.G.Model(&Category{}).Where("parent_id = ?", id).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// ChildCategoryIDs 返回一级分类的全部子分类 ID（含自身与否由调用方决定）。
func (r *MasterRepo) ChildCategoryIDs(parentID string) ([]string, error) {
	var ids []string
	err := r.s.G.Model(&Category{}).Where("parent_id = ?", parentID).Pluck("id", &ids).Error
	return ids, err
}

// ---------------- tags ----------------

// ListTags 标签列表（q 模糊匹配名称）。
func (r *MasterRepo) ListTags(includeArchived bool, q string) ([]Tag, error) {
	db := r.s.G.Order("sort_order, created_at")
	if !includeArchived {
		db = db.Where("is_archived = 0")
	}
	if q != "" {
		db = db.Where("name LIKE ?", "%"+q+"%")
	}
	var out []Tag
	return out, db.Find(&out).Error
}

// GetTag 按 ID 取标签。
func (r *MasterRepo) GetTag(id string) (*Tag, error) {
	var t Tag
	if err := r.s.G.Where("id = ?", id).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// TagInUse 是否被交易引用。
func (r *MasterRepo) TagInUse(id string) (bool, error) {
	var n int64
	err := r.s.G.Model(&TransactionTag{}).Where("tag_id = ?", id).Count(&n).Error
	return n > 0, err
}

// TagsByIDs 批量取标签。
func (r *MasterRepo) TagsByIDs(ids []string) ([]Tag, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []Tag
	return out, r.s.G.Where("id IN ?", ids).Find(&out).Error
}

// ---------------- merchants ----------------

// ListMerchants 商户列表（use_count desc 优先，q 模糊匹配 name/alias）。
func (r *MasterRepo) ListMerchants(q, mtype string, limit int) ([]Merchant, error) {
	db := r.s.G.Where("is_archived = 0")
	if mtype != "" {
		db = db.Where("merchant_type = ?", mtype)
	}
	if q != "" {
		like := "%" + q + "%"
		db = db.Where("name LIKE ? OR alias LIKE ?", like, like)
	}
	db = db.Order("use_count DESC, name")
	if limit > 0 {
		db = db.Limit(limit)
	}
	var out []Merchant
	return out, db.Find(&out).Error
}

// GetMerchant 按 ID 取商户。
func (r *MasterRepo) GetMerchant(id string) (*Merchant, error) {
	var m Merchant
	if err := r.s.G.Where("id = ?", id).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// MerchantInUse 是否被交易引用。
func (r *MasterRepo) MerchantInUse(id string) (bool, error) {
	var n int64
	err := r.s.G.Model(&Transaction{}).Where("merchant_id = ?", id).Count(&n).Error
	return n > 0, err
}

// ---------------- regions ----------------

// SearchRegions 按 code / 汉字 / 拼音 / 首字母搜索。
func (r *MasterRepo) SearchRegions(q string, limit int) ([]Region, error) {
	db := r.s.G.Order("level, code")
	if q != "" {
		like := "%" + q + "%"
		prefix := strings.ToLower(q) + "%"
		db = db.Where("code = ? OR name LIKE ? OR full_name LIKE ? OR pinyin LIKE ? OR pinyin_abbr LIKE ?",
			q, like, like, like, prefix)
	}
	if limit <= 0 {
		limit = 20
	}
	var out []Region
	return out, db.Limit(limit).Find(&out).Error
}

// GetRegion 按 code 取地区。
func (r *MasterRepo) GetRegion(code string) (*Region, error) {
	var rg Region
	if err := r.s.G.Where("code = ?", code).First(&rg).Error; err != nil {
		return nil, err
	}
	return &rg, nil
}

// ---------------- ledgers ----------------

// ListLedgers 账本列表。
func (r *MasterRepo) ListLedgers(includeArchived bool) ([]Ledger, error) {
	q := r.s.G.Order("sort_order, created_at")
	if !includeArchived {
		q = q.Where("is_archived = 0")
	}
	var out []Ledger
	return out, q.Find(&out).Error
}

// GetLedger 按 ID 取账本。
func (r *MasterRepo) GetLedger(id string) (*Ledger, error) {
	var l Ledger
	if err := r.s.G.Where("id = ?", id).First(&l).Error; err != nil {
		return nil, err
	}
	return &l, nil
}

// LedgerRules 取账本全部规则（含目标）。
func (r *MasterRepo) LedgerRules(ledgerID string) ([]LedgerRule, map[string][]LedgerRuleTarget, error) {
	var rules []LedgerRule
	if err := r.s.G.Where("ledger_id = ?", ledgerID).Order("created_at").Find(&rules).Error; err != nil {
		return nil, nil, err
	}
	targets := map[string][]LedgerRuleTarget{}
	if len(rules) > 0 {
		ids := make([]string, 0, len(rules))
		for _, ru := range rules {
			ids = append(ids, ru.ID)
		}
		var ts []LedgerRuleTarget
		if err := r.s.G.Where("rule_id IN ?", ids).Find(&ts).Error; err != nil {
			return nil, nil, err
		}
		for _, t := range ts {
			targets[t.RuleID] = append(targets[t.RuleID], t)
		}
	}
	return rules, targets, nil
}

// ReplaceLedgerRules 全量替换规则（事务）。
func (r *MasterRepo) ReplaceLedgerRules(ledgerID string, rules []LedgerRule, targets map[string][]LedgerRuleTarget) error {
	return r.s.G.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("ledger_id = ?", ledgerID).Delete(&LedgerRule{}).Error; err != nil {
			return err
		}
		for i := range rules {
			if err := tx.Create(&rules[i]).Error; err != nil {
				return err
			}
			for _, t := range targets[rules[i].ID] {
				t.RuleID = rules[i].ID
				if err := tx.Create(&t).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// CountLedgerTransactions 账本内交易数（走参数化 where，与视图口径一致）。
func (r *MasterRepo) CountLedgerTransactions(where string, args []any) (int64, error) {
	var n int64
	err := r.s.G.Table("transactions AS t").Where(where, args...).
		Where("t.deleted_at IS NULL").Count(&n).Error
	return n, err
}

// ---------------- 账户合并（v1.2，需求 R5） ----------------

// ReassignTransactionsTx 事务内把源账户的交易主账户改指到目标账户（含软删行，
// 保证回收站恢复后仍可算）。返回受影响笔数。必须在已搬移 counter_account_id 之后调用。
func (r *MasterRepo) ReassignTransactionsTx(tx *sql.Tx, targetID string, sourceIDs []string) (int64, error) {
	if len(sourceIDs) == 0 {
		return 0, nil
	}
	now := timeutil.NowUnix()
	q := `UPDATE transactions SET account_id = ?, updated_at = ? WHERE account_id IN ` +
		placeholders(len(sourceIDs))
	args := append([]any{targetID, now}, toAny(sourceIDs)...)
	res, err := tx.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ReassignTransferCounterTx 事务内把转账对手账户改指到目标账户（否则转账行双边不一致）。
func (r *MasterRepo) ReassignTransferCounterTx(tx *sql.Tx, targetID string, sourceIDs []string) (int64, error) {
	if len(sourceIDs) == 0 {
		return 0, nil
	}
	q := `UPDATE transactions SET counter_account_id = ? WHERE counter_account_id IN ` +
		placeholders(len(sourceIDs))
	args := append([]any{targetID}, toAny(sourceIDs)...)
	res, err := tx.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// AccountBalancesTx 事务内取(source)账户的初始余额与最早初始余额时间。
// 用于合并前后余额追溯日志。
func (r *MasterRepo) AccountBalancesTx(tx *sql.Tx, ids []string) (int64, int64, error) {
	if len(ids) == 0 {
		return 0, 0, nil
	}
	q := `SELECT COALESCE(SUM(initial_balance_cents),0),
		COALESCE(MIN(NULLIF(initial_balance_time,0)),0) FROM accounts WHERE id IN ` +
		placeholders(len(ids))
	var sum, minTime int64
	if err := tx.QueryRow(q, toAny(ids)...).Scan(&sum, &minTime); err != nil {
		return 0, 0, err
	}
	return sum, minTime, nil
}

// TransactionCountsTx 事务内统计某批账户的交易笔数（主账户）与转账对手笔数。
func (r *MasterRepo) TransactionCountsTx(tx *sql.Tx, ids []string) (int64, int64, error) {
	if len(ids) == 0 {
		return 0, 0, nil
	}
	ph := placeholders(len(ids))
	args := toAny(ids)
	var mainCnt, counterCnt int64
	if err := tx.QueryRow(`SELECT COUNT(1) FROM transactions WHERE account_id IN `+ph,
		args...).Scan(&mainCnt); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(`SELECT COUNT(1) FROM transactions WHERE counter_account_id IN `+ph,
		args...).Scan(&counterCnt); err != nil {
		return 0, 0, err
	}
	return mainCnt, counterCnt, nil
}

// DeleteAccountsTx 事务内删除账户（此时已无引用，trg_accounts_no_delete 不会触发）。
func (r *MasterRepo) DeleteAccountsTx(tx *sql.Tx, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := tx.Exec(`DELETE FROM accounts WHERE id IN `+placeholders(len(ids)), toAny(ids)...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountAccounts 账户总数（不含/含归档都算，合并判定用）。
func (r *MasterRepo) CountAccounts() (int64, error) {
	var n int64
	err := r.s.SQL.QueryRow(`SELECT COUNT(1) FROM accounts`).Scan(&n)
	return n, err
}

// FindAccountByName 按名称查账户（合并目标账户按名称复用）。
func (r *MasterRepo) FindAccountByName(name string) (*Account, error) {
	var a Account
	err := r.s.G.Where("name = ?", name).Order("created_at").First(&a).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// AllAccountIDs 全部账户 ID（含归档），按 sort_order 升序。
func (r *MasterRepo) AllAccountIDs() ([]string, error) {
	var ids []string
	rows, qerr := r.s.SQL.Query(`SELECT id FROM accounts ORDER BY sort_order, created_at`)
	if qerr != nil {
		return nil, qerr
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
