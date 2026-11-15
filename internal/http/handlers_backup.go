package http

import (
	"github.com/gin-gonic/gin"

	"ledger/internal/pkg/apierr"
	"ledger/internal/service"
)

// BackupHandler 备份（§5.9 端点 61–64）。
type BackupHandler struct {
	backup *service.BackupService
}

// NewBackupHandler 构造。
func NewBackupHandler(b *service.BackupService) *BackupHandler {
	return &BackupHandler{backup: b}
}

// Create POST /api/backup/create（端点 61）。
func (h *BackupHandler) Create(c *gin.Context) {
	info, err := h.backup.Create(c.Request.Context())
	if err != nil {
		Err(c, err)
		return
	}
	Created(c, info)
}

// List GET /api/backup/list（端点 62）。
func (h *BackupHandler) List(c *gin.Context) {
	items, err := h.backup.List()
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"total": len(items), "items": items})
}

// Restore POST /api/backup/restore {file, confirm}（端点 63）。
// 恢复期间全部 API 返回 503 RESTORING。
func (h *BackupHandler) Restore(c *gin.Context) {
	var body struct {
		File    string `json:"file"`
		Confirm bool   `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.File == "" {
		BadRequest(c, "file 必填")
		return
	}
	if !body.Confirm {
		ErrCode(c, apierr.CodeValidation, "恢复会覆盖当前数据库，需 confirm:true",
			map[string]any{"suggest": "{\"file\":\"...\",\"confirm\":true}"})
		return
	}
	SetRestoring(true)
	defer SetRestoring(false)
	res, err := h.backup.Restore(c.Request.Context(), body.File)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Delete DELETE /api/backup/:file（端点 64，支持 date/name.zip 路径）。
func (h *BackupHandler) Delete(c *gin.Context) {
	file := c.Param("file")
	if file == "" {
		file = c.Query("file")
	}
	if err := h.backup.Delete(file); err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"ok": true})
}
