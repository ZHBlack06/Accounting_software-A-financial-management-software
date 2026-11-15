package http

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"ledger/internal/service"
	"ledger/internal/store"
)

// ReportHandler /api/reports/* 处理器（§5.6）。
type ReportHandler struct {
	reports *service.ReportService
}

// NewReportHandler 构造。
func NewReportHandler(r *service.ReportService) *ReportHandler {
	return &ReportHandler{reports: r}
}

// Category GET /api/reports/category?level=&type=&start_time=&end_time=&compare=
func (h *ReportHandler) Category(c *gin.Context) {
	q, excludeAdj, err := parseStatsQuery(c)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	level, _ := strconv.Atoi(c.DefaultQuery("level", "1"))
	res, err := h.reports.CategoryReport(c.Request.Context(), q,
		c.DefaultQuery("type", "expense"), level, c.DefaultQuery("compare", "prev_period"), excludeAdj)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Account GET /api/reports/account?start_time=&end_time=
func (h *ReportHandler) Account(c *gin.Context) {
	q, _, err := parseStatsQuery(c)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	res, err := h.reports.AccountReport(c.Request.Context(), q)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Tag GET /api/reports/tag?tag_ids=a,b&start_time=&end_time=&type=
func (h *ReportHandler) Tag(c *gin.Context) {
	q, _, err := parseStatsQuery(c)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	res, err := h.reports.TagReport(c.Request.Context(), q,
		store.SplitCSV(c.Query("tag_ids")), c.DefaultQuery("type", "expense"))
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// PeriodCompare GET /api/reports/period-compare?period=month&base=2026-10
func (h *ReportHandler) PeriodCompare(c *gin.Context) {
	excludeAdj := true
	if c.Query("exclude_adjustments") == "false" {
		excludeAdj = false
	}
	res, err := h.reports.PeriodCompare(c.Request.Context(),
		c.DefaultQuery("period", "month"), c.Query("base"), excludeAdj)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}
