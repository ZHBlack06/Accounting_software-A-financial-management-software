package store

import (
	"fmt"
	"regexp"
	"strings"
)

// ViewManager 账户/账本 SQL 视图的生成、重建与自检（§3.6 双轨制：
// 真实视图供外部工具/审计，API 一律走参数化查询）。
type ViewManager struct {
	s *Store
}

// NewViewManager 构造。
func NewViewManager(s *Store) *ViewManager { return &ViewManager{s: s} }

var identSuffixRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func validIdentSuffix(id string) bool { return identSuffixRe.MatchString(id) }

// CreateAccountView 幂等创建 v_account_<id>。
func (v *ViewManager) CreateAccountView(accountID string) error {
	if !validIdentSuffix(accountID) {
		return fmt.Errorf("非法账户 ID: %q", accountID)
	}
	name := "v_account_" + accountID
	sql := fmt.Sprintf(`CREATE VIEW IF NOT EXISTS %s AS
		SELECT * FROM v_transactions_signed WHERE account_id = '%s'`, name, accountID)
	return v.s.G.Exec(sql).Error
}

// DropAccountView 删除账户视图。
func (v *ViewManager) DropAccountView(accountID string) error {
	if !validIdentSuffix(accountID) {
		return fmt.Errorf("非法账户 ID: %q", accountID)
	}
	return v.s.G.Exec(`DROP VIEW IF EXISTS v_account_` + accountID).Error
}

// ledgerWhereParts 展开规则为分组条件。返回每条规则的 (joiner, parts, ids)。
// parts 中 "?"/字面量二选一由 literal 决定（视图 DDL 不能带参数，需内联字面量）。
func (v *ViewManager) ledgerWhereParts(ledgerID string, literal bool) (string, []any, error) {
	var rules []LedgerRule
	if err := v.s.G.Where("ledger_id = ? AND is_active = 1", ledgerID).Find(&rules).Error; err != nil {
		return "", nil, err
	}
	ph := func(n int) string { return "(" + strings.TrimSuffix(strings.Repeat("?,", n), ",") + ")" }
	lit := func(ids []string) string {
		var b strings.Builder
		b.WriteByte('(')
		for i, id := range ids {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('\'')
			b.WriteString(id)
			b.WriteByte('\'')
		}
		b.WriteByte(')')
		return b.String()
	}
	var orParts []string
	var args []any
	for _, r := range rules {
		var targets []LedgerRuleTarget
		if err := v.s.G.Where("rule_id = ?", r.ID).Find(&targets).Error; err != nil {
			return "", nil, err
		}
		groups := map[string][]string{}
		for _, t := range targets {
			if !validIdentSuffix(t.TargetID) {
				return "", nil, fmt.Errorf("非法规则目标 ID: %q", t.TargetID)
			}
			groups[t.TargetType] = append(groups[t.TargetType], t.TargetID)
		}
		var andParts []string
		add := func(col string, ids []string) {
			if len(ids) == 0 {
				return
			}
			if literal {
				andParts = append(andParts, "t."+col+" IN "+lit(ids))
			} else {
				andParts = append(andParts, "t."+col+" IN "+ph(len(ids)))
				for _, id := range ids {
					args = append(args, id)
				}
			}
		}
		add("category_id", groups["category_l1"])
		add("sub_category_id", groups["category_l2"])
		add("account_id", groups["account"])
		add("merchant_id", groups["merchant"])
		if ids := groups["tag"]; len(ids) > 0 {
			if literal {
				andParts = append(andParts, "EXISTS (SELECT 1 FROM transaction_tags tt "+
					"WHERE tt.transaction_id = t.id AND tt.tag_id IN "+lit(ids)+")")
			} else {
				andParts = append(andParts, "EXISTS (SELECT 1 FROM transaction_tags tt "+
					"WHERE tt.transaction_id = t.id AND tt.tag_id IN "+ph(len(ids))+")")
				for _, id := range ids {
					args = append(args, id)
				}
			}
		}
		if len(andParts) > 0 {
			joiner := " OR "
			if r.MatchMode == "all" {
				joiner = " AND "
			}
			orParts = append(orParts, "("+strings.Join(andParts, joiner)+")")
		}
	}
	if len(orParts) == 0 {
		return "1=0", nil, nil // 无规则 → 空账本
	}
	return "(" + strings.Join(orParts, " OR ") + ")", args, nil
}

// BuildLedgerWhere 参数化版本（API 查询用），别名 t.*。
func (v *ViewManager) BuildLedgerWhere(ledgerID string) (string, []any, error) {
	return v.ledgerWhereParts(ledgerID, false)
}

// RebuildLedgerView DROP + CREATE v_ledger_<id>（字面量内联，幂等）。
func (v *ViewManager) RebuildLedgerView(ledgerID string) error {
	if !validIdentSuffix(ledgerID) {
		return fmt.Errorf("非法账本 ID: %q", ledgerID)
	}
	name := "v_ledger_" + ledgerID
	if err := v.s.G.Exec(`DROP VIEW IF EXISTS ` + name).Error; err != nil {
		return err
	}
	where, _, err := v.ledgerWhereParts(ledgerID, true)
	if err != nil {
		return err
	}
	sql := fmt.Sprintf(`CREATE VIEW %s AS SELECT t.* FROM v_transactions_signed t WHERE %s`, name, where)
	return v.s.G.Exec(sql).Error
}

// DropLedgerView 删除账本视图。
func (v *ViewManager) DropLedgerView(ledgerID string) error {
	if !validIdentSuffix(ledgerID) {
		return fmt.Errorf("非法账本 ID: %q", ledgerID)
	}
	return v.s.G.Exec(`DROP VIEW IF EXISTS v_ledger_` + ledgerID).Error
}

// StartReconcile 启动自检：为全部账户/账本补齐缺失视图；孤儿视图保留仅告警（由调用方记日志）。
func (v *ViewManager) StartReconcile() error {
	var accounts []Account
	if err := v.s.G.Select("id").Find(&accounts).Error; err != nil {
		return err
	}
	for _, a := range accounts {
		if err := v.CreateAccountView(a.ID); err != nil {
			return err
		}
	}
	var ledgers []Ledger
	if err := v.s.G.Select("id").Find(&ledgers).Error; err != nil {
		return err
	}
	for _, l := range ledgers {
		if err := v.RebuildLedgerView(l.ID); err != nil {
			return err
		}
	}
	return nil
}
