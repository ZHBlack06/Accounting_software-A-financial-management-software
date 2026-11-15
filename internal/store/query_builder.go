package store

import (
	"fmt"
	"strings"

	"ledger/internal/pkg/timeutil"
)

// TxQuery 交易列表/统计的统一过滤条件（§5.4 端点 35 全部参数）。
type TxQuery struct {
	StartTime          *int64
	EndTime            *int64
	AccountIDs         []string
	CategoryID         string
	IncludeSub         bool
	SubCategoryID      string
	TagIDs             []string
	TagMatch           string // any | all
	MerchantID         string
	RegionID           string
	LedgerID           string
	Types              []string
	AmountMinCents     *int64
	AmountMaxCents     *int64
	IncludeAdjustments bool
	OnlyAdjustments    bool
	IncludeDeleted     bool
	OnlyDeleted        bool // 回收站：只看软删
	// FreezePending v1.2 §9.8：nil 不过滤；true 只看待确认冻结；false 只排除待确认。
	FreezePending *bool
	Keyword       string
	Sort               string // time_desc|time_asc|amount_desc|amount_asc|created_desc
	Page               int
	PageSize           int
}

// ParseTimeRange 解析 start_time / end_time 字符串（兼容纯日期）。
func ParseTimeRange(startStr, endStr string) (*int64, *int64, error) {
	var start, end *int64
	if startStr != "" {
		v, err := timeutil.ParseTimeOrDate(startStr, false)
		if err != nil {
			return nil, nil, fmt.Errorf("start_time 格式非法: %w", err)
		}
		start = &v
	}
	if endStr != "" {
		v, err := timeutil.ParseTimeOrDate(endStr, true)
		if err != nil {
			return nil, nil, fmt.Errorf("end_time 格式非法: %w", err)
		}
		end = &v
	}
	return start, end, nil
}

// splitCSV 拆分逗号分隔参数并去空。
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SplitCSV 导出给 handler 使用。
func SplitCSV(s string) []string { return splitCSV(s) }

func placeholders(n int) string {
	return "(" + strings.TrimSuffix(strings.Repeat("?,", n), ",") + ")"
}

// BuildTxWhere 把过滤条件编译为 (whereSQL, args)。alias 为表别名（如 "t"）。
// views 用于账本规则展开；categoryChildren 为 category_id → 子分类 ID 解析函数。
func BuildTxWhere(q *TxQuery, alias string, views *ViewManager, categoryChildren func(string) ([]string, error)) (string, []any, error) {
	a := alias
	if a != "" {
		a += "."
	}
	var where []string
	var args []any

	if q.IncludeDeleted {
		if q.OnlyDeleted {
			where = append(where, a+"deleted_at IS NOT NULL")
		}
	} else {
		where = append(where, a+"deleted_at IS NULL")
	}
	// v1.2 §9.8：待确认冻结筛选（走部分索引不生效，但列表本身有其它索引）
	if q.FreezePending != nil {
		if *q.FreezePending {
			where = append(where, a+"freeze_confirmed = 0")
		} else {
			where = append(where, a+"freeze_confirmed = 1")
		}
	}
	if q.StartTime != nil {
		where = append(where, a+"transaction_time >= ?")
		args = append(args, *q.StartTime)
	}
	if q.EndTime != nil {
		where = append(where, a+"transaction_time <= ?")
		args = append(args, *q.EndTime)
	}
	if len(q.AccountIDs) > 0 {
		where = append(where, a+"account_id IN "+placeholders(len(q.AccountIDs)))
		for _, id := range q.AccountIDs {
			args = append(args, id)
		}
	}
	if q.CategoryID != "" {
		ids := []string{q.CategoryID}
		if q.IncludeSub && categoryChildren != nil {
			children, err := categoryChildren(q.CategoryID)
			if err != nil {
				return "", nil, err
			}
			ids = append(ids, children...)
		}
		var orParts []string
		orParts = append(orParts, a+"category_id IN "+placeholders(len(ids)))
		for _, id := range ids {
			args = append(args, id)
		}
		if q.IncludeSub && len(ids) > 1 {
			orParts = append(orParts, a+"sub_category_id IN "+placeholders(len(ids)))
			for _, id := range ids {
				args = append(args, id)
			}
		}
		where = append(where, "("+strings.Join(orParts, " OR ")+")")
	}
	if q.SubCategoryID != "" {
		where = append(where, a+"sub_category_id = ?")
		args = append(args, q.SubCategoryID)
	}
	if len(q.TagIDs) > 0 {
		if q.TagMatch == "all" {
			for _, tid := range q.TagIDs {
				where = append(where, "EXISTS (SELECT 1 FROM transaction_tags tt "+
					"WHERE tt.transaction_id = "+a+"id AND tt.tag_id = ?)")
				args = append(args, tid)
			}
		} else {
			where = append(where, "EXISTS (SELECT 1 FROM transaction_tags tt "+
				"WHERE tt.transaction_id = "+a+"id AND tt.tag_id IN "+placeholders(len(q.TagIDs))+")")
			for _, tid := range q.TagIDs {
				args = append(args, tid)
			}
		}
	}
	if q.MerchantID != "" {
		where = append(where, a+"merchant_id = ?")
		args = append(args, q.MerchantID)
	}
	if q.RegionID != "" {
		where = append(where, a+"region_id = ?")
		args = append(args, q.RegionID)
	}
	if q.LedgerID != "" && views != nil {
		w, largs, err := views.BuildLedgerWhere(q.LedgerID)
		if err != nil {
			return "", nil, err
		}
		// BuildLedgerWhere 生成 t.* 别名，按需替换
		if alias != "" && alias != "t" {
			w = strings.ReplaceAll(w, "t.", a)
		}
		where = append(where, w)
		args = append(args, largs...)
	}
	if len(q.Types) > 0 {
		where = append(where, a+"transaction_type IN "+placeholders(len(q.Types)))
		for _, t := range q.Types {
			args = append(args, t)
		}
	}
	if q.AmountMinCents != nil {
		where = append(where, a+"amount_cents >= ?")
		args = append(args, *q.AmountMinCents)
	}
	if q.AmountMaxCents != nil {
		where = append(where, a+"amount_cents <= ?")
		args = append(args, *q.AmountMaxCents)
	}
	if q.OnlyAdjustments {
		where = append(where, a+"is_adjustment = 1")
	} else if !q.IncludeAdjustments {
		where = append(where, a+"is_adjustment = 0")
	}
	if q.Keyword != "" {
		like := "%" + q.Keyword + "%"
		where = append(where, "("+a+"remark LIKE ? OR "+a+"merchant_text LIKE ? OR "+a+"counterparty_name LIKE ?)")
		args = append(args, like, like, like)
	}
	if len(where) == 0 {
		return "1=1", nil, nil
	}
	return strings.Join(where, " AND "), args, nil
}

// OrderClause 排序键（§6.2：任何 SQL 不得只按 transaction_time 排序）。
func OrderClause(sort string) string {
	switch sort {
	case "time_asc":
		return "transaction_time ASC, created_seq ASC"
	case "amount_desc":
		return "amount_cents DESC, transaction_time DESC, created_seq DESC"
	case "amount_asc":
		return "amount_cents ASC, transaction_time DESC, created_seq DESC"
	case "created_desc":
		return "created_seq DESC"
	default: // time_desc
		return "transaction_time DESC, created_seq DESC"
	}
}
