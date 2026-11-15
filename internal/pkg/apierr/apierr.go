// Package apierr 定义统一业务错误类型与错误码 → HTTP 状态映射。
// 所有 handler 的错误必须经 apierr 构造，由 response.Err 统一输出错误体。
package apierr

import (
	"fmt"
	"net/http"
)

// Error 业务错误。Code 见 docs/ARCHITECTURE.md §10.1。
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New 构造业务错误。
func New(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Newf 带格式化消息构造业务错误。
func Newf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithDetails 附加 details 后返回自身（链式）。
func (e *Error) WithDetails(details map[string]any) *Error {
	e.Details = details
	return e
}

// 常用错误码常量（与 §10.1 一一对应）。
const (
	CodeValidation         = "VALIDATION_ERROR"
	CodeUnauthorized       = "UNAUTHORIZED"
	CodeBadToken           = "BAD_TOKEN"
	CodeNotFound           = "NOT_FOUND"
	CodeFrozen             = "FROZEN"
	CodeDuplicate          = "DUPLICATE"
	CodeInUse              = "IN_USE"
	CodeAccountInUse       = "ACCOUNT_IN_USE"
	CodeCategoryInUse      = "CATEGORY_IN_USE"
	CodeTransferImmutable  = "TRANSFER_IMMUTABLE"
	CodeNoEffect           = "NO_EFFECT"
	CodeRetention          = "RETENTION"
	CodeSequenceOverflow   = "SEQUENCE_OVERFLOW"
	CodeMaterializeFailed  = "MATERIALIZE_FAILED"
	CodeOCRUnavailable     = "OCR_UNAVAILABLE"
	CodeOCRUpstream        = "OCR_UPSTREAM_ERROR"
	CodeOCRTaskNotFound    = "OCR_TASK_NOT_FOUND"
	CodeUnsupportedCSV     = "UNSUPPORTED_CSV"
	CodeImportBatchExpired = "IMPORT_BATCH_EXPIRED"
	CodeRestoring          = "RESTORING"
	CodeBackupFailed       = "BACKUP_FAILED"
	CodeInternal           = "INTERNAL"

	// v1.2 新增（§2.7）
	CodeUnsupportedSpreadsheet = "UNSUPPORTED_SPREADSHEET"
	CodeImportTooLarge         = "IMPORT_TOO_LARGE"
	CodeImportSheetInvalid     = "IMPORT_SHEET_INVALID"
	CodeImportBatchCommitted   = "IMPORT_BATCH_COMMITTED"
)

// statusMap 错误码 → HTTP 状态。
var statusMap = map[string]int{
	CodeValidation:         http.StatusBadRequest,
	CodeUnauthorized:       http.StatusUnauthorized,
	CodeBadToken:           http.StatusUnauthorized,
	CodeNotFound:           http.StatusNotFound,
	CodeFrozen:             http.StatusConflict,
	CodeDuplicate:          http.StatusConflict,
	CodeInUse:              http.StatusConflict,
	CodeAccountInUse:       http.StatusConflict,
	CodeCategoryInUse:      http.StatusConflict,
	CodeTransferImmutable:  http.StatusBadRequest,
	CodeNoEffect:           http.StatusBadRequest,
	CodeRetention:          http.StatusConflict,
	CodeSequenceOverflow:   http.StatusInternalServerError,
	CodeMaterializeFailed:  http.StatusInternalServerError,
	CodeOCRUnavailable:     http.StatusServiceUnavailable,
	CodeOCRUpstream:        http.StatusBadGateway,
	CodeOCRTaskNotFound:    http.StatusNotFound,
	CodeUnsupportedCSV:     http.StatusUnprocessableEntity,
	CodeImportBatchExpired: http.StatusGone,
	CodeRestoring:          http.StatusServiceUnavailable,
	CodeBackupFailed:       http.StatusInternalServerError,
	CodeInternal:           http.StatusInternalServerError,

	CodeUnsupportedSpreadsheet: http.StatusUnprocessableEntity,
	CodeImportTooLarge:         http.StatusUnprocessableEntity,
	CodeImportSheetInvalid:     http.StatusBadRequest,
	CodeImportBatchCommitted:   http.StatusConflict,
}

// StatusOf 返回错误码对应的 HTTP 状态；未知错误一律 500。
func StatusOf(err error) int {
	if e, ok := err.(*Error); ok {
		if s, ok2 := statusMap[e.Code]; ok2 {
			return s
		}
	}
	return http.StatusInternalServerError
}
