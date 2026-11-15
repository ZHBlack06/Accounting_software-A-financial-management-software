package http

import (
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"ledger/internal/service"
	"ledger/internal/store"
)

// SystemHandler 维护端点 + 截图（§5.10 端点 65–69）。
type SystemHandler struct {
	mat     *service.Materializer
	balance *service.BalanceService
	maint   *service.MaintenanceService
	ssRepo  *store.ScreenshotRepo
	ss      *service.ScreenshotStore
}

// NewSystemHandler 构造。
func NewSystemHandler(mat *service.Materializer, balance *service.BalanceService,
	maint *service.MaintenanceService, ssRepo *store.ScreenshotRepo,
	ss *service.ScreenshotStore) *SystemHandler {
	return &SystemHandler{mat: mat, balance: balance, maint: maint, ssRepo: ssRepo, ss: ss}
}

// Cleanup POST /api/maintenance/cleanup（端点 67）。
func (h *SystemHandler) Cleanup(c *gin.Context) {
	res, err := h.maint.Cleanup(c.Request.Context(), time.Now())
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Rematerialize POST /api/maintenance/rematerialize {full:true}（端点 68）。
func (h *SystemHandler) Rematerialize(c *gin.Context) {
	var body struct {
		Full bool `json:"full"`
	}
	_ = c.ShouldBindJSON(&body)
	started := time.Now()
	var rows int
	if body.Full {
		res, err := h.mat.ForceFull(c.Request.Context(), time.Now())
		if err != nil {
			Err(c, err)
			return
		}
		rows = res.RowsProcessed
	} else {
		res, err := h.mat.Materialize(c.Request.Context(), time.Now())
		if err != nil {
			Err(c, err)
			return
		}
		rows = res.RowsProcessed
	}
	verify, err := h.balance.Verify()
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{
		"rows":    rows,
		"seconds": time.Since(started).Seconds(),
		"verify":  verify,
	})
}

// Verify GET /api/maintenance/verify（端点 69）。
func (h *SystemHandler) Verify(c *gin.Context) {
	res, err := h.balance.Verify()
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// ScreenshotMeta GET /api/screenshots/:id（端点 65）。
func (h *SystemHandler) ScreenshotMeta(c *gin.Context) {
	ss, err := h.ssRepo.Get(c.Param("id"))
	if err != nil {
		Err(c, err)
		return
	}
	if ss == nil {
		NotFound(c, "截图不存在")
		return
	}
	OK(c, gin.H{
		"id": ss.ID, "transaction_id": ss.TransactionID,
		"original_path": ss.OriginalPath, "compressed_path": ss.CompressedPath,
		"ocr_result_path": ss.OCRResultPath, "ocr_status": ss.OCRStatus,
		"ocr_model": ss.OCRModel, "ocr_elapsed_ms": ss.OCRElapsedMs,
		"need_review": ss.NeedReview, "file_size": ss.FileSize,
		"url": "/api/screenshots/" + ss.ID + "/file?size=compressed",
	})
}

// ScreenshotFile GET /api/screenshots/:id/file?size=compressed|original（端点 66）。
func (h *SystemHandler) ScreenshotFile(c *gin.Context) {
	ss, err := h.ssRepo.Get(c.Param("id"))
	if err != nil {
		Err(c, err)
		return
	}
	if ss == nil {
		NotFound(c, "截图不存在")
		return
	}
	rel := ss.CompressedPath
	if c.Query("size") == "original" {
		rel = ss.OriginalPath
	}
	abs := h.ss.Abs(rel)
	if _, err := os.Stat(abs); err != nil {
		NotFound(c, "截图文件不存在")
		return
	}
	mimeType := mime.TypeByExtension(filepath.Ext(abs))
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	c.Header("Content-Type", mimeType)
	c.File(abs)
}
