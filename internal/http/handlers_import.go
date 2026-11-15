package http

import (
	"io"

	"github.com/gin-gonic/gin"

	"ledger/internal/service"
)

// ImportHandler CSV 导入（§5.7）。
type ImportHandler struct {
	imports *service.ImportService
}

// NewImportHandler 构造。
func NewImportHandler(s *service.ImportService) *ImportHandler {
	return &ImportHandler{imports: s}
}

// Preview POST /api/import/csv/preview（multipart: file / account_id / source）。
func (h *ImportHandler) Preview(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		BadRequest(c, "file 必填（multipart/form-data）")
		return
	}
	f, err := fh.Open()
	if err != nil {
		Err(c, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		Err(c, err)
		return
	}
	res, err := h.imports.Preview(c.Request.Context(), data,
		c.PostForm("account_id"), c.DefaultPostForm("source", "auto"))
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Commit POST /api/import/csv/commit。
func (h *ImportHandler) Commit(c *gin.Context) {
	var in service.ImportCommitInput
	if err := c.ShouldBindJSON(&in); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	res, err := h.imports.Commit(c.Request.Context(), &in)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}
