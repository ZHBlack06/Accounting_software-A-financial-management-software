package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// StatsService 统计聚合（§6.10：SQL 粗筛 + Go 分桶）。
type StatsService struct {
	s       *store.Store
	txr     *store.TxRepo
	master  *store.MasterRepo
	balance *BalanceService
	meta    *store.MetaRepo
}

// NewStatsService 构造。
func NewStatsService(s *store.Store, txr *store.TxRepo, master *store.MasterRepo,
	balance *BalanceService, meta *store.MetaRepo) *StatsService {
	return &StatsService{s: s, txr: txr, master: master, balance: balance, meta: meta}
}

// statWhere 统计通用过滤（复用 TxQuery 构造器，alias=t）。
func (s *StatsService) statWhere(q *store.TxQuery) (string, []any, error) {
	return s.statWhereAlias(q, "t")
}

// statWhereAlias 指定 SQL 别名（TagReport 用 v 作别名）。
func (s *StatsService) statWhereAlias(q *store.TxQuery, alias string) (string, []any, error) {
	qq := *q
	qq.IncludeAdjustments = true // 调整项单独聚列，不在 where 排除
	qq.IncludeDeleted = false
	return store.BuildTxWhere(&qq, alias, s.txr.Views(), s.master.ChildCategoryIDs)
}

// SummaryResult /stats/summary 响应。
type SummaryResult struct {
	StartTime              string `json:"start_time"`
	EndTime                string `json:"end_time"`
	IncomeCents            int64  `json:"income_cents"`
	ExpenseCents           int64  `json:"expense_cents"`
	NetCents               int64  `json:"net_cents"`
	TotalBalanceCents      int64  `json:"total_balance_cents"`
	AdjustmentIncomeCents  int64  `json:"adjustment_income_cents"`
	AdjustmentExpenseCents int64  `json:"adjustment_expense_cents"`
	AdjustmentNetCents     int64  `json:"adjustment_net_cents"`
	TransactionCount       int64  `json:"transaction_count"`
	DayCount               int64  `json:"day_count"`
	AvgExpensePerDayCents  int64  `json:"avg_expense_per_day_cents"`
	ExcludeAdjustments     bool   `json:"exclude_adjustments"`
}

// Summary 汇总（§5.5 端点 44）。excludeAdjustments=false 时收支含调整项。
func (s *StatsService) Summary(ctx context.Context, q *store.TxQuery, excludeAdj bool) (*SummaryResult, error) {
	where, args, err := s.statWhere(q)
	if err != nil {
		return nil, err
	}
	var statInc, statExp, adjNet, adjInc, adjExp, count int64
	err = s.s.SQL.QueryRow(`SELECT
		COALESCE(SUM(t.stat_income_cents),0), COALESCE(SUM(t.stat_expense_cents),0),
		COALESCE(SUM(t.adjustment_signed_cents),0),
		COALESCE(SUM(CASE WHEN t.is_adjustment=1 AND t.signed_cents>0 THEN t.amount_cents ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN t.is_adjustment=1 AND t.signed_cents<0 THEN t.amount_cents ELSE 0 END),0),
		COUNT(1)
		FROM v_transactions_signed t WHERE `+where, args...).
		Scan(&statInc, &statExp, &adjNet, &adjInc, &adjExp, &count)
	if err != nil {
		return nil, err
	}
	inc, exp := statInc, statExp
	if !excludeAdj {
		inc += adjInc
		exp += adjExp
	}
	balances, err := s.balance.Current(ctx)
	if err != nil {
		return nil, err
	}
	start, end := rangeBounds(q)
	dayCount := int64(1)
	if start != nil && end != nil {
		dayCount = (timeutil.DayStart(*end)-timeutil.DayStart(*start))/86400 + 1
	}
	var avg int64
	if dayCount > 0 {
		avg = exp / dayCount
	}
	return &SummaryResult{
		StartTime: timeutil.FormatLocal(derefOr(start, 0)), EndTime: timeutil.FormatLocal(derefOr(end, 0)),
		IncomeCents: inc, ExpenseCents: exp, NetCents: inc - exp,
		TotalBalanceCents:      balances.Total,
		AdjustmentIncomeCents:  adjInc,
		AdjustmentExpenseCents: adjExp,
		AdjustmentNetCents:     adjNet,
		TransactionCount:       count,
		DayCount:               dayCount,
		AvgExpensePerDayCents:  avg,
		ExcludeAdjustments:     excludeAdj,
	}, nil
}

func derefOr(p *int64, def int64) int64 {
	if p == nil {
		return def
	}
	return *p
}

func rangeBounds(q *store.TxQuery) (*int64, *int64) { return q.StartTime, q.EndTime }

// ---------------- trend ----------------

// TrendPoint 趋势点。
type TrendPoint struct {
	Bucket          string `json:"bucket"`
	Label           string `json:"label"`
	IncomeCents     int64  `json:"income_cents"`
	ExpenseCents    int64  `json:"expense_cents"`
	NetCents        int64  `json:"net_cents"`
	AdjustmentCents int64  `json:"adjustment_cents"`
	Count           int64  `json:"count"`
}

// TrendResult /stats/trend 响应。
type TrendResult struct {
	Granularity string       `json:"granularity"`
	Downgraded  bool         `json:"downgraded,omitempty"`
	Points      []TrendPoint `json:"points"`
}

// Trend 分桶趋势（端点 45；day 且区间 > 92 天自动降级 week）。
func (s *StatsService) Trend(ctx context.Context, q *store.TxQuery, granularity string) (*TrendResult, error) {
	if granularity == "" {
		granularity = "day"
	}
	start, end := rangeBounds(q)
	if start == nil || end == nil {
		return nil, fmt.Errorf("start_time/end_time 必填")
	}
	downgraded := false
	if granularity == "day" && *end-*start > 92*86400 {
		granularity = "week"
		downgraded = true
	}
	where, args, err := s.statWhere(q)
	if err != nil {
		return nil, err
	}
	rows, err := s.s.SQL.Query(`SELECT t.local_date,
		COALESCE(SUM(t.stat_income_cents),0), COALESCE(SUM(t.stat_expense_cents),0),
		COALESCE(SUM(t.adjustment_signed_cents),0), COUNT(1)
		FROM v_transactions_signed t WHERE `+where+` GROUP BY t.local_date ORDER BY t.local_date`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type dayAgg struct {
		inc, exp, adj, cnt int64
	}
	byDay := map[string]*dayAgg{}
	for rows.Next() {
		var d string
		a := &dayAgg{}
		if err = rows.Scan(&d, &a.inc, &a.exp, &a.adj, &a.cnt); err != nil {
			return nil, err
		}
		byDay[d] = a
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	// 生成桶（空桶补 0）
	buckets := bucketKeys(*start, *end, granularity)
	points := make([]TrendPoint, 0, len(buckets))
	for _, b := range buckets {
		p := TrendPoint{Bucket: b.key, Label: b.label}
		for _, d := range b.days {
			if a, ok := byDay[d]; ok {
				p.IncomeCents += a.inc
				p.ExpenseCents += a.exp
				p.AdjustmentCents += a.adj
				p.Count += a.cnt
			}
		}
		p.NetCents = p.IncomeCents - p.ExpenseCents
		points = append(points, p)
	}
	return &TrendResult{Granularity: granularity, Downgraded: downgraded, Points: points}, nil
}

type bucket struct {
	key, label string
	days       []string
}

// bucketKeys 按粒度生成连续桶（周一起始）。
func bucketKeys(start, end int64, granularity string) []bucket {
	loc := timeutil.Local()
	var out []bucket
	switch granularity {
	case "week":
		cur := timeutil.WeekStart(start)
		for cur <= end {
			var days []string
			for i := 0; i < 7; i++ {
				days = append(days, timeutil.LocalDate(cur+int64(i)*86400))
			}
			t := time.Unix(cur, 0).In(loc)
			out = append(out, bucket{key: t.Format("2006-01-02"), label: t.Format("01-02"), days: days})
			cur += 7 * 86400
		}
	case "month":
		cur := timeutil.MonthStart(start)
		for cur <= end {
			t := time.Unix(cur, 0).In(loc)
			next := t.AddDate(0, 1, 0).Unix()
			var days []string
			for d := cur; d < next; d += 86400 {
				days = append(days, timeutil.LocalDate(d))
			}
			out = append(out, bucket{key: t.Format("2006-01"), label: t.Format("2006-01"), days: days})
			cur = next
		}
	case "year":
		cur := timeutil.YearStart(start)
		for cur <= end {
			t := time.Unix(cur, 0).In(loc)
			next := t.AddDate(1, 0, 0).Unix()
			var days []string
			for d := cur; d < next; d += 86400 {
				days = append(days, timeutil.LocalDate(d))
			}
			out = append(out, bucket{key: t.Format("2006"), label: t.Format("2006"), days: days})
			cur = next
		}
	default: // day
		for d := timeutil.DayStart(start); d <= end; d += 86400 {
			t := time.Unix(d, 0).In(loc)
			out = append(out, bucket{key: t.Format("2006-01-02"), label: t.Format("01-02"),
				days: []string{t.Format("2006-01-02")}})
		}
	}
	return out
}

// ---------------- category breakdown / ranking ----------------

// BreakdownItem 分类占比项。
type BreakdownItem struct {
	CategoryID       *string         `json:"category_id"`
	Name             string          `json:"name"`
	ParentID         *string         `json:"parent_id"`
	AmountCents      int64           `json:"amount_cents"`
	Percent          float64         `json:"percent"`
	TransactionCount int64           `json:"transaction_count"`
	Children         []BreakdownItem `json:"children,omitempty"`
}

// BreakdownResult /stats/category-breakdown 响应。
type BreakdownResult struct {
	Type       string          `json:"type"`
	Level      int             `json:"level"`
	TotalCents int64           `json:"total_cents"`
	Items      []BreakdownItem `json:"items"`
}

// CategoryBreakdown 分类占比（端点 46，level=2 带 children）。
func (s *StatsService) CategoryBreakdown(ctx context.Context, q *store.TxQuery, typ string, level int) (*BreakdownResult, error) {
	if typ != "income" {
		typ = "expense"
	}
	where, args, err := s.statWhere(q)
	if err != nil {
		return nil, err
	}
	sumCol := "t.stat_expense_cents"
	if typ == "income" {
		sumCol = "t.stat_income_cents"
	}
	rows, err := s.s.SQL.Query(`SELECT IFNULL(t.category_id,''), IFNULL(t.sub_category_id,''),
		COALESCE(SUM(`+sumCol+`),0), COUNT(1)
		FROM v_transactions_signed t WHERE `+where+`
		GROUP BY IFNULL(t.category_id,''), IFNULL(t.sub_category_id,'')`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type cell struct {
		amount, count int64
	}
	l1Map := map[string]*cell{}
	l2Map := map[string]map[string]*cell{} // l1 → l2 → cell
	var total int64
	for rows.Next() {
		var l1, l2 string
		c := &cell{}
		if err = rows.Scan(&l1, &l2, &c.amount, &c.count); err != nil {
			return nil, err
		}
		if l1 == "" && l2 != "" {
			// 只有二级时回填其父级
			if sub, err := s.master.GetCategory(l2); err == nil && sub.ParentID != nil {
				l1 = *sub.ParentID
			}
		}
		agg := l1Map[l1]
		if agg == nil {
			agg = &cell{}
			l1Map[l1] = agg
		}
		agg.amount += c.amount
		agg.count += c.count
		if l2 != "" {
			if l2Map[l1] == nil {
				l2Map[l1] = map[string]*cell{}
			}
			l2c := l2Map[l1][l2]
			if l2c == nil {
				l2c = &cell{}
				l2Map[l1][l2] = l2c
			}
			l2c.amount += c.amount
			l2c.count += c.count
		}
		total += c.amount
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	pct := func(v int64) float64 {
		if total == 0 {
			return 0
		}
		return round2(float64(v) * 100 / float64(total))
	}
	var items []BreakdownItem
	for l1, c := range l1Map {
		if c.amount == 0 {
			continue
		}
		item := BreakdownItem{AmountCents: c.amount, Percent: pct(c.amount), TransactionCount: c.count}
		if l1 == "" {
			item.Name = "未分类"
		} else {
			id := l1
			item.CategoryID = &id
			if cat, err := s.master.GetCategory(l1); err == nil {
				item.Name = cat.Name
			}
		}
		if level == 2 {
			for l2, lc := range l2Map[l1] {
				if lc.amount == 0 {
					continue
				}
				child := BreakdownItem{AmountCents: lc.amount, Percent: pct(lc.amount),
					TransactionCount: lc.count, ParentID: item.CategoryID}
				id := l2
				child.CategoryID = &id
				if cat, err := s.master.GetCategory(l2); err == nil {
					child.Name = cat.Name
				}
				item.Children = append(item.Children, child)
			}
			sort.Slice(item.Children, func(i, j int) bool {
				return item.Children[i].AmountCents > item.Children[j].AmountCents
			})
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].AmountCents > items[j].AmountCents })
	if items == nil {
		items = []BreakdownItem{}
	}
	return &BreakdownResult{Type: typ, Level: level, TotalCents: total, Items: items}, nil
}

// RankingItem 分类排行项。
type RankingItem struct {
	Rank             int     `json:"rank"`
	CategoryID       *string `json:"category_id"`
	Name             string  `json:"name"`
	AmountCents      int64   `json:"amount_cents"`
	Percent          float64 `json:"percent"`
	TransactionCount int64   `json:"transaction_count"`
}

// CategoryRanking 分类排行（端点 48）。
func (s *StatsService) CategoryRanking(ctx context.Context, q *store.TxQuery, typ string, limit int) ([]RankingItem, error) {
	bd, err := s.CategoryBreakdown(ctx, q, typ, 1)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	out := []RankingItem{}
	for i, item := range bd.Items {
		if i >= limit {
			break
		}
		out = append(out, RankingItem{
			Rank: i + 1, CategoryID: item.CategoryID, Name: item.Name,
			AmountCents: item.AmountCents, Percent: item.Percent,
			TransactionCount: item.TransactionCount,
		})
	}
	return out, nil
}

// ---------------- account balance series ----------------

// BalancePoint 余额点。
type BalancePoint struct {
	Bucket       string `json:"bucket"`
	BalanceCents int64  `json:"balance_cents"`
}

// AccountSeries 单账户余额序列。
type AccountSeries struct {
	AccountID string         `json:"account_id"`
	Name      string         `json:"name"`
	Points    []BalancePoint `json:"points"`
}

// BalanceSeriesResult /stats/account-balance-series 响应。
type BalanceSeriesResult struct {
	Granularity string          `json:"granularity"`
	Series      []AccountSeries `json:"series"`
	TotalPoints []BalancePoint  `json:"total_points"`
}

// AccountBalanceSeries 每日收盘余额（端点 47；纯累计口径，跨冻结/动态边界无跳变）。
func (s *StatsService) AccountBalanceSeries(ctx context.Context, accountIDs []string, start, end int64) (*BalanceSeriesResult, error) {
	startDate := timeutil.LocalDate(timeutil.DayStart(start))
	endDate := timeutil.LocalDate(end)
	// 区间日列表
	var days []string
	for d := timeutil.DayStart(start); d <= end; d += 86400 {
		days = append(days, timeutil.LocalDate(d))
	}

	accs, err := s.master.ListAccounts(true)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range accountIDs {
		want[id] = true
	}
	res := &BalanceSeriesResult{Granularity: "day", Series: []AccountSeries{}, TotalPoints: []BalancePoint{}}
	for _, a := range accs {
		if len(want) > 0 && !want[a.ID] {
			continue
		}
		accum, err := s.dailyAccum(a.ID, a.InitialBalanceCents, startDate, endDate)
		if err != nil {
			return nil, err
		}
		name := a.Name
		if a.CardTail != "" {
			name = a.Name + "（" + a.CardTail + "）"
		}
		as := AccountSeries{AccountID: a.ID, Name: name, Points: []BalancePoint{}}
		cur := accum.base
		for _, d := range days {
			cur += accum.byDay[d]
			as.Points = append(as.Points, BalancePoint{Bucket: d, BalanceCents: cur})
		}
		res.Series = append(res.Series, as)
	}
	// 总余额序列：始终为全部 is_count_in_total=1 账户之和（含归档），与 /accounts 的 total 口径一致
	totalBase := int64(0)
	totalByDay := map[string]int64{}
	for _, a := range accs {
		if a.IsCountInTotal != 1 {
			continue
		}
		accum, err := s.dailyAccum(a.ID, a.InitialBalanceCents, startDate, endDate)
		if err != nil {
			return nil, err
		}
		totalBase += accum.base
		for d, v := range accum.byDay {
			totalByDay[d] += v
		}
	}
	cur := totalBase
	for _, d := range days {
		cur += totalByDay[d]
		res.TotalPoints = append(res.TotalPoints, BalancePoint{Bucket: d, BalanceCents: cur})
	}
	return res, nil
}

type seriesAccum struct {
	base  int64
	byDay map[string]int64
}

// dailyAccum 账户区间前收盘基数 + 区间每日净额。
func (s *StatsService) dailyAccum(accountID string, initial int64, startDate, endDate string) (*seriesAccum, error) {
	var base int64
	err := s.s.SQL.QueryRow(`SELECT ? + COALESCE((
		SELECT SUM(x.signed_cents) FROM v_transactions_signed x
		WHERE x.account_id = ? AND x.local_date < ?), 0)`,
		initial, accountID, startDate).Scan(&base)
	if err != nil {
		return nil, err
	}
	rows, err := s.s.SQL.Query(`SELECT local_date, COALESCE(SUM(signed_cents),0)
		FROM v_transactions_signed WHERE account_id = ? AND local_date BETWEEN ? AND ?
		GROUP BY local_date`, accountID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]int64{}
	for rows.Next() {
		var d string
		var v int64
		if err = rows.Scan(&d, &v); err != nil {
			return nil, err
		}
		byDay[d] = v
	}
	return &seriesAccum{base: base, byDay: byDay}, rows.Err()
}

// ---------------- high amount ----------------

// HighAmountResult /stats/high-amount 响应（items 由 handler 转 DTO）。
type HighAmountResult struct {
	ThresholdCents int64                `json:"threshold_cents"`
	AutoThreshold  bool                 `json:"auto_threshold"`
	Transactions   []*store.Transaction `json:"-"`
	TotalCents     int64                `json:"total_cents"`
}

// HighAmount 高额交易（端点 49）。阈值 = max(配置, P95)；支出笔数 < 20 时用配置值。
func (s *StatsService) HighAmount(ctx context.Context, q *store.TxQuery, threshold *int64, limit int) (*HighAmountResult, error) {
	if limit <= 0 {
		limit = 20
	}
	// 只统计支出、非调整、未删除
	qq := *q
	qq.Types = []string{"expense"}
	qq.IncludeAdjustments = false
	where, args, err := s.statWhere(&qq)
	if err != nil {
		return nil, err
	}
	var count int64
	if err = s.s.SQL.QueryRow(`SELECT COUNT(1) FROM v_transactions_signed t WHERE `+where, args...).Scan(&count); err != nil {
		return nil, err
	}
	cfgThreshold := int64(50000)
	if v, err := s.meta.Get("high_amount_threshold_cents"); err == nil && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfgThreshold = n
		}
	}
	th := cfgThreshold
	auto := false
	if threshold != nil && *threshold > 0 {
		th = *threshold
	} else if count >= 20 {
		// P95：OFFSET = ceil(n*0.95)-1
		offset := (count*95+99)/100 - 1
		var p95 int64
		err = s.s.SQL.QueryRow(`SELECT t.amount_cents FROM v_transactions_signed t
			WHERE `+where+` ORDER BY t.amount_cents LIMIT 1 OFFSET ?`,
			append(append([]any{}, args...), offset)...).Scan(&p95)
		if err == nil && p95 > th {
			th = p95
		}
		auto = true
	}
	// 取明细
	txs, _, _, err := s.txr.ListTransactions(&store.TxQuery{
		StartTime: q.StartTime, EndTime: q.EndTime, Types: []string{"expense"},
		AccountIDs: q.AccountIDs, CategoryID: q.CategoryID, IncludeSub: q.IncludeSub,
		TagIDs: q.TagIDs, TagMatch: q.TagMatch, MerchantID: q.MerchantID,
		RegionID: q.RegionID, LedgerID: q.LedgerID,
		AmountMinCents: &th, IncludeAdjustments: false,
		Sort: "amount_desc", Page: 1, PageSize: limit,
	}, s.master.ChildCategoryIDs)
	if err != nil {
		return nil, err
	}
	var totalCents int64
	for _, t := range txs {
		totalCents += t.AmountCents
	}
	return &HighAmountResult{
		ThresholdCents: th, AutoThreshold: auto, Transactions: txs, TotalCents: totalCents,
	}, nil
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
