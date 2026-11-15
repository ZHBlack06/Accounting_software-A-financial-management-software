package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// ReportService 报表（§5.6 端点 50–53）。
type ReportService struct {
	s       *store.Store
	txr     *store.TxRepo
	master  *store.MasterRepo
	balance *BalanceService
	stats   *StatsService
}

// NewReportService 构造。
func NewReportService(s *store.Store, txr *store.TxRepo, master *store.MasterRepo,
	balance *BalanceService, stats *StatsService) *ReportService {
	return &ReportService{s: s, txr: txr, master: master, balance: balance, stats: stats}
}

// catCell 分类金额单元。
type catCell struct {
	amount int64
	count  int64
}

// catSums 区间内按分类聚合（level=1 按 category_id；level=2 按 sub_category_id）。
func (r *ReportService) catSums(q *store.TxQuery, typ string, level int) (map[string]*catCell, int64, error) {
	where, args, err := r.stats.statWhere(q)
	if err != nil {
		return nil, 0, err
	}
	sumCol := "t.stat_expense_cents"
	if typ == "income" {
		sumCol = "t.stat_income_cents"
	}
	groupCol := "t.category_id"
	if level == 2 {
		groupCol = "t.sub_category_id"
	}
	rows, err := r.s.SQL.Query(`SELECT IFNULL(`+groupCol+`,''), COALESCE(SUM(`+sumCol+`),0), COUNT(1)
		FROM v_transactions_signed t WHERE `+where+` GROUP BY IFNULL(`+groupCol+`,'')`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := map[string]*catCell{}
	var total int64
	for rows.Next() {
		var id string
		var amount, count int64
		if err = rows.Scan(&id, &amount, &count); err != nil {
			return nil, 0, err
		}
		out[id] = &catCell{amount: amount, count: count}
		total += amount
	}
	return out, total, rows.Err()
}

// pctOrNil (cur-prev)/prev*100；prev==0 返回 nil（避免 Inf）。
func pctOrNil(cur, prev int64) *float64 {
	if prev == 0 {
		return nil
	}
	v := round2(float64(cur-prev) * 100 / float64(prev))
	return &v
}

// CategoryReportItem 分类报表行。
type CategoryReportItem struct {
	CategoryID       *string  `json:"category_id"`
	Name             string   `json:"name"`
	AmountCents      int64    `json:"amount_cents"`
	Percent          float64  `json:"percent"`
	PrevPeriodCents  int64    `json:"prev_period_cents"`
	PrevPeriodPct    *float64 `json:"prev_period_pct"`
	PrevYearCents    int64    `json:"prev_year_cents"`
	PrevYearPct      *float64 `json:"prev_year_pct"`
	TransactionCount int64    `json:"transaction_count"`
}

// CategoryReportResult /reports/category 响应。
type CategoryReportResult struct {
	Level              int                  `json:"level"`
	Type               string               `json:"type"`
	TotalCents         int64                `json:"total_cents"`
	ExcludeAdjustments bool                 `json:"exclude_adjustments"`
	Compare            string               `json:"compare"`
	Items              []CategoryReportItem `json:"items"`
}

// shiftRange 计算上期/去年同期区间。
func shiftRange(start, end int64, mode string) (int64, int64) {
	if mode == "prev_year" {
		loc := timeutil.Local()
		s := time.Unix(start, 0).In(loc).AddDate(-1, 0, 0).Unix()
		e := time.Unix(end, 0).In(loc).AddDate(-1, 0, 0).Unix()
		return s, e
	}
	span := end - start + 1
	return start - span, start - 1
}

// CategoryReport 分类报表（端点 50，支持环比/同比，prev=0 → null）。
func (r *ReportService) CategoryReport(ctx context.Context, q *store.TxQuery, typ string, level int,
	compare string, excludeAdj bool) (*CategoryReportResult, error) {
	if typ != "income" {
		typ = "expense"
	}
	if level != 2 {
		level = 1
	}
	if compare != "prev_year" && compare != "none" {
		compare = "prev_period"
	}
	if q.StartTime == nil || q.EndTime == nil {
		return nil, apierr.New(apierr.CodeValidation, "start_time/end_time 必填")
	}
	cur, total, err := r.catSums(q, typ, level)
	if err != nil {
		return nil, err
	}
	prev := map[string]*catCell{}
	prevYear := map[string]*catCell{}
	if compare != "none" {
		q2 := *q
		s, e := shiftRange(*q.StartTime, *q.EndTime, "prev_period")
		q2.StartTime, q2.EndTime = &s, &e
		if prev, _, err = r.catSums(&q2, typ, level); err != nil {
			return nil, err
		}
		q3 := *q
		s2, e2 := shiftRange(*q.StartTime, *q.EndTime, "prev_year")
		q3.StartTime, q3.EndTime = &s2, &e2
		if prevYear, _, err = r.catSums(&q3, typ, level); err != nil {
			return nil, err
		}
	}
	res := &CategoryReportResult{Level: level, Type: typ, TotalCents: total,
		ExcludeAdjustments: excludeAdj, Compare: compare, Items: []CategoryReportItem{}}
	for id, cell := range cur {
		item := CategoryReportItem{AmountCents: cell.amount, TransactionCount: cell.count}
		if total > 0 {
			item.Percent = round2(float64(cell.amount) * 100 / float64(total))
		}
		if id == "" {
			item.Name = "未分类"
		} else {
			cid := id
			item.CategoryID = &cid
			if cat, err := r.master.GetCategory(id); err == nil {
				item.Name = cat.Name
			}
		}
		if pc, ok := prev[id]; ok {
			item.PrevPeriodCents = pc.amount
		}
		if py, ok := prevYear[id]; ok {
			item.PrevYearCents = py.amount
		}
		item.PrevPeriodPct = pctOrNil(item.AmountCents, item.PrevPeriodCents)
		item.PrevYearPct = pctOrNil(item.AmountCents, item.PrevYearCents)
		res.Items = append(res.Items, item)
	}
	sort.Slice(res.Items, func(i, j int) bool { return res.Items[i].AmountCents > res.Items[j].AmountCents })
	return res, nil
}

// AccountReportItem 账户报表行。
type AccountReportItem struct {
	AccountID        string `json:"account_id"`
	Name             string `json:"name"`
	IncomeCents      int64  `json:"income_cents"`
	ExpenseCents     int64  `json:"expense_cents"`
	TransferInCents  int64  `json:"transfer_in_cents"`
	TransferOutCents int64  `json:"transfer_out_cents"`
	NetCents         int64  `json:"net_cents"`
	BalanceCents     int64  `json:"balance_cents"`
}

// AccountReportResult /reports/account 响应。
type AccountReportResult struct {
	Items              []AccountReportItem `json:"items"`
	TotalBalanceCents  int64               `json:"total_balance_cents"`
}

// AccountReport 账户报表（端点 51）。
func (r *ReportService) AccountReport(ctx context.Context, q *store.TxQuery) (*AccountReportResult, error) {
	where, args, err := r.stats.statWhere(q)
	if err != nil {
		return nil, err
	}
	rows, err := r.s.SQL.Query(`SELECT t.account_id,
		COALESCE(SUM(t.stat_income_cents),0), COALESCE(SUM(t.stat_expense_cents),0),
		COALESCE(SUM(CASE WHEN t.transaction_type='transfer' AND t.transfer_direction='in'
			THEN t.amount_cents ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN t.transaction_type='transfer' AND t.transfer_direction='out'
			THEN t.amount_cents ELSE 0 END),0)
		FROM v_transactions_signed t WHERE `+where+` GROUP BY t.account_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type sums struct{ inc, exp, tin, tout int64 }
	byAcc := map[string]*sums{}
	for rows.Next() {
		var id string
		s := &sums{}
		if err = rows.Scan(&id, &s.inc, &s.exp, &s.tin, &s.tout); err != nil {
			return nil, err
		}
		byAcc[id] = s
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	accs, err := r.master.ListAccounts(true)
	if err != nil {
		return nil, err
	}
	balances, err := r.balance.Current(ctx)
	if err != nil {
		return nil, err
	}
	res := &AccountReportResult{Items: []AccountReportItem{}, TotalBalanceCents: balances.Total}
	for _, a := range accs {
		item := AccountReportItem{AccountID: a.ID, Name: a.Name, BalanceCents: balances.ByAccount[a.ID]}
		if a.CardTail != "" {
			item.Name = a.Name + "（" + a.CardTail + "）"
		}
		if s, ok := byAcc[a.ID]; ok {
			item.IncomeCents, item.ExpenseCents = s.inc, s.exp
			item.TransferInCents, item.TransferOutCents = s.tin, s.tout
		}
		item.NetCents = item.IncomeCents - item.ExpenseCents
		res.Items = append(res.Items, item)
	}
	return res, nil
}

// TagReportItem 标签报表行。
type TagReportItem struct {
	TagID            string  `json:"tag_id"`
	Name             string  `json:"name"`
	Color            string  `json:"color"`
	AmountCents      int64   `json:"amount_cents"`
	Percent          float64 `json:"percent"`
	TransactionCount int64   `json:"transaction_count"`
}

// TagReportResult /reports/tag 响应（total_cents 为去重口径）。
type TagReportResult struct {
	Items      []TagReportItem `json:"items"`
	TotalCents int64           `json:"total_cents"`
}

// TagReport 标签报表（端点 52）：同一交易命中多标签不重复计入 total_cents。
func (r *ReportService) TagReport(ctx context.Context, q *store.TxQuery, tagIDs []string, typ string) (*TagReportResult, error) {
	if len(tagIDs) == 0 {
		return nil, apierr.New(apierr.CodeValidation, "tag_ids 必填")
	}
	if typ != "income" {
		typ = "expense"
	}
	qq := *q
	qq.TagIDs = nil // 标签过滤在 SQL 里单独处理
	where, args, err := r.stats.statWhereAlias(&qq, "v")
	if err != nil {
		return nil, err
	}
	sumCol := "v.stat_expense_cents"
	if typ == "income" {
		sumCol = "v.stat_income_cents"
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(tagIDs)), ",")
	rows, err := r.s.SQL.Query(`SELECT tt.tag_id, COALESCE(SUM(`+sumCol+`),0), COUNT(DISTINCT v.id)
		FROM transaction_tags tt JOIN v_transactions_signed v ON v.id = tt.transaction_id
		WHERE tt.tag_id IN (`+ph+`) AND `+where+` GROUP BY tt.tag_id`,
		append(toAnySlice(tagIDs), args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res := &TagReportResult{Items: []TagReportItem{}}
	byTag := map[string]*TagReportItem{}
	for rows.Next() {
		item := &TagReportItem{}
		if err = rows.Scan(&item.TagID, &item.AmountCents, &item.TransactionCount); err != nil {
			return nil, err
		}
		byTag[item.TagID] = item
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// total_cents：命中任一选中标签的交易去重后金额
	var total int64
	err = r.s.SQL.QueryRow(`SELECT COALESCE(SUM(sub.amount),0) FROM (
		SELECT v.id AS id, MAX(`+sumCol+`) AS amount
		FROM transaction_tags tt JOIN v_transactions_signed v ON v.id = tt.transaction_id
		WHERE tt.tag_id IN (`+ph+`) AND `+where+` GROUP BY v.id) sub`,
		append(toAnySlice(tagIDs), args...)...).Scan(&total)
	if err != nil {
		return nil, err
	}
	res.TotalCents = total
	for _, id := range tagIDs {
		item, ok := byTag[id]
		if !ok {
			continue
		}
		if tag, err := r.master.GetTag(id); err == nil {
			item.Name, item.Color = tag.Name, tag.Color
		}
		if total > 0 {
			item.Percent = round2(float64(item.AmountCents) * 100 / float64(total))
		}
		res.Items = append(res.Items, *item)
	}
	sort.Slice(res.Items, func(i, j int) bool { return res.Items[i].AmountCents > res.Items[j].AmountCents })
	return res, nil
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// PeriodSummary 单期汇总。
type PeriodSummary struct {
	Label       string `json:"label"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	IncomeCents int64  `json:"income_cents"`
	ExpenseCents int64 `json:"expense_cents"`
	NetCents    int64  `json:"net_cents"`
}

// PeriodDiff 差异。
type PeriodDiff struct {
	IncomeCents  int64    `json:"income_cents"`
	IncomePct    *float64 `json:"income_pct"`
	ExpenseCents int64    `json:"expense_cents"`
	ExpensePct   *float64 `json:"expense_pct"`
}

// PeriodCompareResult /reports/period-compare 响应。
type PeriodCompareResult struct {
	Period             string        `json:"period"`
	Current            PeriodSummary `json:"current"`
	Previous           PeriodSummary `json:"previous"`
	PrevYear           PeriodSummary `json:"prev_year"`
	Diff               PeriodDiff    `json:"diff"`
	ExcludeAdjustments bool          `json:"exclude_adjustments"`
}

// periodRange 计算 period/base 的起止（含 label）。
func periodRange(period, base string) (start, end int64, label string, err error) {
	loc := timeutil.Local()
	switch period {
	case "quarter":
		var year, q int
		if _, err = fmt.Sscanf(base, "%d-Q%d", &year, &q); err != nil || q < 1 || q > 4 {
			return 0, 0, "", apierr.New(apierr.CodeValidation, "base 格式应为 YYYY-Qn（如 2026-Q3）")
		}
		startT := time.Date(year, time.Month((q-1)*3+1), 1, 0, 0, 0, 0, loc)
		endT := startT.AddDate(0, 3, -1)
		return startT.Unix(), endT.Unix() + 86399, base, nil
	case "year":
		var year int
		if _, err = fmt.Sscanf(base, "%d", &year); err != nil {
			return 0, 0, "", apierr.New(apierr.CodeValidation, "base 格式应为 YYYY")
		}
		startT := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
		endT := time.Date(year, 12, 31, 0, 0, 0, 0, loc)
		return startT.Unix(), endT.Unix() + 86399, base, nil
	default: // month
		t, e := time.ParseInLocation("2006-01", base, loc)
		if e != nil {
			return 0, 0, "", apierr.New(apierr.CodeValidation, "base 格式应为 YYYY-MM（如 2026-10）")
		}
		startT := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		endT := startT.AddDate(0, 1, -1)
		return startT.Unix(), endT.Unix() + 86399, t.Format("2006-01"), nil
	}
}

// sumRange 区间收支汇总。
func (r *ReportService) sumRange(start, end int64, excludeAdj bool) (int64, int64, error) {
	var statInc, statExp, adjInc, adjExp int64
	err := r.s.SQL.QueryRow(`SELECT
		COALESCE(SUM(t.stat_income_cents),0), COALESCE(SUM(t.stat_expense_cents),0),
		COALESCE(SUM(CASE WHEN t.is_adjustment=1 AND t.signed_cents>0 THEN t.amount_cents ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN t.is_adjustment=1 AND t.signed_cents<0 THEN t.amount_cents ELSE 0 END),0)
		FROM v_transactions_signed t WHERE t.transaction_time BETWEEN ? AND ?`, start, end).
		Scan(&statInc, &statExp, &adjInc, &adjExp)
	if err != nil {
		return 0, 0, err
	}
	if !excludeAdj {
		statInc += adjInc
		statExp += adjExp
	}
	return statInc, statExp, nil
}

// PeriodCompare 周期对比（端点 53）：本期/上期/去年同期 + 差异（prev=0 → null）。
func (r *ReportService) PeriodCompare(ctx context.Context, period, base string, excludeAdj bool) (*PeriodCompareResult, error) {
	loc := timeutil.Local()
	if period == "" {
		period = "month"
	}
	if base == "" {
		base = time.Now().In(loc).Format("2006-01")
	}
	curStart, curEnd, curLabel, err := periodRange(period, base)
	if err != nil {
		return nil, err
	}
	// 上期
	var prevStart, prevEnd int64
	var prevLabel string
	switch period {
	case "quarter":
		prevStart = time.Unix(curStart, 0).In(loc).AddDate(0, -3, 0).Unix()
		prevLabel = time.Unix(prevStart, 0).In(loc).Format("2006") + "-Q" +
			fmt.Sprint((int(time.Unix(prevStart, 0).In(loc).Month())-1)/3+1)
	case "year":
		prevStart = time.Unix(curStart, 0).In(loc).AddDate(-1, 0, 0).Unix()
		prevLabel = fmt.Sprint(time.Unix(prevStart, 0).In(loc).Year())
	default:
		prevStart = time.Unix(curStart, 0).In(loc).AddDate(0, -1, 0).Unix()
		prevLabel = time.Unix(prevStart, 0).In(loc).Format("2006-01")
	}
	prevEnd = curStart - 1
	// 去年同期
	yearStart := time.Unix(curStart, 0).In(loc).AddDate(-1, 0, 0).Unix()
	yearEnd := time.Unix(curEnd, 0).In(loc).AddDate(-1, 0, 0).Unix()
	yearLabel := curLabel
	if period == "year" {
		yearLabel = fmt.Sprint(time.Unix(yearStart, 0).In(loc).Year())
	} else if period == "quarter" {
		yearLabel = time.Unix(yearStart, 0).In(loc).Format("2006") + curLabel[4:]
	} else {
		yearLabel = time.Unix(yearStart, 0).In(loc).Format("2006-01")
	}

	inc1, exp1, err := r.sumRange(curStart, curEnd, excludeAdj)
	if err != nil {
		return nil, err
	}
	inc2, exp2, err := r.sumRange(prevStart, prevEnd, excludeAdj)
	if err != nil {
		return nil, err
	}
	inc3, exp3, err := r.sumRange(yearStart, yearEnd, excludeAdj)
	if err != nil {
		return nil, err
	}
	return &PeriodCompareResult{
		Period: period,
		Current: PeriodSummary{Label: curLabel,
			StartTime: timeutil.FormatLocal(curStart), EndTime: timeutil.FormatLocal(curEnd),
			IncomeCents: inc1, ExpenseCents: exp1, NetCents: inc1 - exp1},
		Previous: PeriodSummary{Label: prevLabel,
			StartTime: timeutil.FormatLocal(prevStart), EndTime: timeutil.FormatLocal(prevEnd),
			IncomeCents: inc2, ExpenseCents: exp2, NetCents: inc2 - exp2},
		PrevYear: PeriodSummary{Label: yearLabel,
			StartTime: timeutil.FormatLocal(yearStart), EndTime: timeutil.FormatLocal(yearEnd),
			IncomeCents: inc3, ExpenseCents: exp3, NetCents: inc3 - exp3},
		Diff: PeriodDiff{
			IncomeCents:  inc1 - inc2,
			IncomePct:    pctOrNil(inc1, inc2),
			ExpenseCents: exp1 - exp2,
			ExpensePct:   pctOrNil(exp1, exp2),
		},
		ExcludeAdjustments: excludeAdj,
	}, nil
}
