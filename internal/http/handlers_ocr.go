package http

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"ledger/internal/service"
)

// OCRHandler OCR 代理（§5.8 端点 56–60）。
type OCRHandler struct {
	ocr *service.OCRService
}

// NewOCRHandler 构造。
func NewOCRHandler(o *service.OCRService) *OCRHandler {
	return &OCRHandler{ocr: o}
}

// Recognize POST /api/ocr/recognize（multipart：file 或 screenshot_id；model；mode）。
func (h *OCRHandler) Recognize(c *gin.Context) {
	in := &service.RecognizeInput{
		ScreenshotID: c.PostForm("screenshot_id"),
		Model:        c.DefaultPostForm("model", "auto"),
		Mode:         c.DefaultPostForm("mode", "sync"),
		Hint:         c.PostForm("hint"),
	}
	if fh, err := c.FormFile("file"); err == nil {
		f, err := fh.Open()
		if err != nil {
			Err(c, err)
			return
		}
		defer f.Close()
		if in.File, err = io.ReadAll(f); err != nil {
			Err(c, err)
			return
		}
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext == "" {
			ext = ".jpg"
		}
		in.Ext = ext
	}
	res, err := h.ocr.Recognize(c.Request.Context(), in)
	if err != nil {
		Err(c, err)
		return
	}
	if in.Mode == "async" {
		c.JSON(202, res)
		return
	}
	OK(c, res)
}

// RecognizeBatch POST /api/ocr/recognize-batch（多文件，异步）。
func (h *OCRHandler) RecognizeBatch(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		BadRequest(c, "multipart/form-data 非法")
		return
	}
	files := form.File["files"]
	if len(files) == 0 {
		files = form.File["file"]
	}
	if len(files) == 0 {
		BadRequest(c, "files 必填")
		return
	}
	taskIDs := make([]string, 0, len(files))
	screenshotIDs := make([]string, 0, len(files))
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			Err(c, err)
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			Err(c, err)
			return
		}
		res, err := h.ocr.Recognize(c.Request.Context(), &service.RecognizeInput{
			File: data, Ext: filepath.Ext(fh.Filename), Mode: "async",
			Model: "auto", Hint: c.PostForm("hint"),
		})
		if err != nil {
			Err(c, err)
			return
		}
		taskIDs = append(taskIDs, res.TaskID)
		screenshotIDs = append(screenshotIDs, res.ScreenshotID)
	}
	c.JSON(202, gin.H{"task_ids": taskIDs, "screenshot_ids": screenshotIDs})
}

// Task GET /api/ocr/tasks/:id（端点 58）。
func (h *OCRHandler) Task(c *gin.Context) {
	res, err := h.ocr.Task(c.Request.Context(), c.Param("id"))
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// Model GET /api/ocr/model（端点 59）。
func (h *OCRHandler) Model(c *gin.Context) {
	res, err := h.ocr.Model(c.Request.Context())
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}

// SwitchModel PUT /api/ocr/model（端点 60）。
func (h *OCRHandler) SwitchModel(c *gin.Context) {
	var body struct {
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Model == "" {
		BadRequest(c, "model 必填")
		return
	}
	res, err := h.ocr.SwitchModel(c.Request.Context(), body.Model)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}
