package http

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
	"ledger/internal/store"
)

// StatsHandler /api/stats/* 处理器（§5.5）。
type StatsHandler struct {
	stats *service.StatsService
	txnH  *TransactionHandler
	mat   *service.Materializer
}

// NewStatsHandler 构造。
func NewStatsHandler(stats *service.StatsService, txnH *TransactionHandler, mat *service.Materializer) *StatsHandler {
	return &StatsHandler{stats: stats, txnH: txnH, mat: mat}
}

// parseStatsQuery 解析通用查询参数（缺省最近 30 天）。
func parseStatsQuery(c *gin.Context) (*store.TxQuery, bool, error) {
	q := &store.TxQuery{IncludeSub: true, IncludeAdjustments: true}
	startStr, endStr := c.Query("start_time"), c.Query("end_time")
	now := time.Now().Unix()
	if endStr == "" {
		v := now
		q.EndTime = &v
	}
	if startStr == "" {
		v := timeutil.DayStart(now) - 29*86400
		q.StartTime = &v
	}
	start, end, err := store.ParseTimeRange(startStr, endStr)
	if err != nil {
		return nil, false, err
	}
	if start != nil {
		q.StartTime = start
	}
	if end != nil {
		q.EndTime = end
	}
	var accIDs []string
	for _, v := range c.QueryArray("account_id") {
		accIDs = append(accIDs, store.SplitCSV(v)...)
	}
	q.AccountIDs = accIDs
	q.CategoryID = c.Query("category_id")
	if c.Query("include_sub") == "false" {
		q.IncludeSub = false
	}
	q.TagIDs = store.SplitCSV(c.Query("tag_ids"))
	q.TagMatch = c.DefaultQuery("tag_match", "any")
	q.MerchantID = c.Query("merchant_id")
	q.RegionID = c.Query("region_id")
	q.LedgerID = c.Query("ledger_id")
	q.Types = store.SplitCSV(c.Query("type"))
	excludeAdj := true
	if v := c.Query("exclude_adjustments"); v == "false" {
		excludeAdj = false
	}
	return q, excludeAdj, nil
}

// Summary GET /api/stats/summary
func (h *StatsHandler) Summary(c *gin.Context) {
	h.mat.EnsureFresh(c.Request.Context())
	q, excludeAdj, err := parseStatsQuery(c)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	res, err := h.stats.Summary(c.Request.Context(), q, excludeAdj)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Trend GET /api/stats/trend?granularity=day|week|month|year
func (h *StatsHandler) Trend(c *gin.Context) {
	h.mat.EnsureFresh(c.Request.Context())
	q, _, err := parseStatsQuery(c)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	res, err := h.stats.Trend(c.Request.Context(), q, c.DefaultQuery("granularity", "day"))
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Breakdown GET /api/stats/category-breakdown?type=expense&level=1|2
func (h *StatsHandler) Breakdown(c *gin.Context) {
	q, _, err := parseStatsQuery(c)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	level, _ := strconv.Atoi(c.DefaultQuery("level", "1"))
	res, err := h.stats.CategoryBreakdown(c.Request.Context(), q, c.DefaultQuery("type", "expense"), level)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// BalanceSeries GET /api/stats/account-balance-series?account_ids=a,b&granularity=day
func (h *StatsHandler) BalanceSeries(c *gin.Context) {
	h.mat.EnsureFresh(c.Request.Context())
	q, _, err := parseStatsQuery(c)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	accIDs := store.SplitCSV(c.Query("account_ids"))
	if len(accIDs) == 0 {
		for _, v := range c.QueryArray("account_id") {
			accIDs = append(accIDs, store.SplitCSV(v)...)
		}
	}
	res, err := h.stats.AccountBalanceSeries(c.Request.Context(), accIDs, *q.StartTime, *q.EndTime)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Ranking GET /api/stats/category-ranking?type=expense&limit=10
func (h *StatsHandler) Ranking(c *gin.Context) {
	q, _, err := parseStatsQuery(c)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	items, err := h.stats.CategoryRanking(c.Request.Context(), q, c.DefaultQuery("type", "expense"), limit)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"items": items})
}

// HighAmount GET /api/stats/high-amount?threshold_cents=&limit=20
func (h *StatsHandler) HighAmount(c *gin.Context) {
	q, _, err := parseStatsQuery(c)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	var threshold *int64
	if v := c.Query("threshold_cents"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			BadRequest(c, "threshold_cents 必须为整数")
			return
		}
		threshold = &n
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	res, err := h.stats.HighAmount(c.Request.Context(), q, threshold, limit)
	if err != nil {
		Err(c, err)
		return
	}
	params, err := h.txnH.buildDTOParams(c, res.Transactions, false)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{
		"threshold_cents": res.ThresholdCents,
		"auto_threshold":  res.AutoThreshold,
		"items":           toDTOs(res.Transactions, params),
		"total_cents":     res.TotalCents,
	})
}
