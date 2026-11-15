// Package http HTTP 层：路由、中间件、DTO 与全部 handler。
package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"ledger/internal/pkg/apierr"
)

// OK 200 返回资源对象（无 envelope，§5 约定）。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, data)
}

// Created 201 返回新建资源。
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, data)
}

// Err 统一错误输出：apierr.Error 按码表映射状态，其他错误 500 INTERNAL。
func Err(c *gin.Context, err error) {
	var ae *apierr.Error
	if errors.As(err, &ae) {
		c.JSON(apierr.StatusOf(ae), gin.H{"error": ae})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": &apierr.Error{
		Code:    apierr.CodeInternal,
		Message: "服务器内部错误",
		Details: map[string]any{"request_id": c.GetString("req_id")},
	}})
}

// ErrCode 直接按错误码输出（400/409 等具体构造助手）。
func ErrCode(c *gin.Context, code, message string, details map[string]any) {
	ae := &apierr.Error{Code: code, Message: message, Details: details}
	c.JSON(apierr.StatusOf(ae), gin.H{"error": ae})
}

// BadRequest 400 VALIDATION_ERROR 快捷方式。
func BadRequest(c *gin.Context, message string) {
	ErrCode(c, apierr.CodeValidation, message, nil)
}

// NotFound 404 NOT_FOUND 快捷方式。
func NotFound(c *gin.Context, message string) {
	ErrCode(c, apierr.CodeNotFound, message, nil)
}

// Conflict 409 快捷方式。
func Conflict(c *gin.Context, code, message string, details map[string]any) {
	ErrCode(c, code, message, details)
}
