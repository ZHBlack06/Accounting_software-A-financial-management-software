package http

import (
	"io"
	"strconv"

	"github.com/gin-gonic/gin"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/sheetparse"
	"ledger/internal/service"
)

// ImportSheetHandler 通用表格导入（Excel/CSV）处理器（§2.2–§2.4）。
type ImportSheetHandler struct {
	sheets *service.ImportSheetService
}

// NewImportSheetHandler 构造。
func NewImportSheetHandler(s *service.ImportSheetService) *ImportSheetHandler {
	return &ImportSheetHandler{sheets: s}
}

// probeMemory multipart 内存缓冲上限（超出部分落临时文件）。
const probeMemory = 32 << 20

// Probe POST /api/import/sheet/probe（multipart: files[] + 可选 account_id）。
func (h *ImportSheetHandler) Probe(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(probeMemory); err != nil {
		BadRequest(c, "请求必须为 multipart/form-data，且字段名为 files")
		return
	}
	form := c.Request.MultipartForm
	if form == nil {
		BadRequest(c, "请求必须为 multipart/form-data，且字段名为 files")
		return
	}
	defer func() { _ = form.RemoveAll() }()

	files := form.File["files"]
	if len(files) == 0 {
		files = form.File["file"] // 兼容单数字段名
	}
	if len(files) == 0 {
		BadRequest(c, "files 必填（至少 1 个 .xlsx/.xlsm/.csv）")
		return
	}
	if len(files) > 20 {
		BadRequest(c, "单次最多上传 20 个文件")
		return
	}
	inputs := make([]service.ProbeFileInput, 0, len(files))
	var total int64
	for _, fh := range files {
		if fh.Size > sheetparse.MaxFileSize {
			ErrCode(c, apierr.CodeImportTooLarge,
				"单个文件超过 30MB 上限，请拆分后重试",
				map[string]any{"file_name": fh.Filename, "size": fh.Size})
			return
		}
		total += fh.Size
		if total > sheetparse.MaxBatchSize {
			ErrCode(c, apierr.CodeImportTooLarge,
				"本批文件合计超过 60MB 上限，请分批导入",
				map[string]any{"total_size": total})
			return
		}
		f, err := fh.Open()
		if err != nil {
			Err(c, apierr.Newf(apierr.CodeValidation, "读取上传文件「%s」失败: %v", fh.Filename, err))
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, sheetparse.MaxFileSize+1))
		_ = f.Close()
		if err != nil {
			Err(c, apierr.Newf(apierr.CodeValidation, "读取上传文件「%s」失败: %v", fh.Filename, err))
			return
		}
		inputs = append(inputs, service.ProbeFileInput{FileName: fh.Filename, Data: data})
	}
	res, err := h.sheets.Probe(c.Request.Context(), inputs)
	if err != nil {
		Err(c, err)
		return
	}
	if acc := c.PostForm("account_id"); acc != "" {
		// 与默认值无关，仅回显给前端做映射预填
		OK(c, gin.H{"batch_id": res.BatchID, "files": res.Files,
			"warnings": res.Warnings, "default_account_id": acc})
		return
	}
	OK(c, res)
}

// Preview POST /api/import/sheet/preview。
func (h *ImportSheetHandler) Preview(c *gin.Context) {
	var in service.PreviewInput
	if err := c.ShouldBindJSON(&in); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	res, err := h.sheets.Preview(c.Request.Context(), &in)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Commit POST /api/import/sheet/commit。
func (h *ImportSheetHandler) Commit(c *gin.Context) {
	var in service.CommitInput
	if err := c.ShouldBindJSON(&in); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	res, err := h.sheets.Commit(c.Request.Context(), &in)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Batches GET /api/import/batches?limit=20（导入批次审计列表，P2）。
func (h *ImportSheetHandler) Batches(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := h.sheets.Batches(limit)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"total": len(items), "page": 1, "page_size": limit, "items": items})
}
