package http

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"ledger/internal/pkg/apierr"
)

// restoringFlag 备份恢复期间为 true，全部 API 返回 503 RESTORING。
var restoringFlag atomic.Bool

// SetRestoring 设置恢复中标志（备份 handler 使用）。
func SetRestoring(v bool) { restoringFlag.Store(v) }

// RestoringGate 恢复中闸门（健康检查与登录放行）。
func RestoringGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if restoringFlag.Load() {
			p := c.Request.URL.Path
			if p != "/api/health" && p != "/api/auth/login" {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": &apierr.Error{
					Code: apierr.CodeRestoring, Message: "备份恢复中，请稍候"}})
				return
			}
		}
		c.Next()
	}
}

// reqID 生成短请求 ID。
func reqID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req_unknown"
	}
	return hex.EncodeToString(b[:])
}

// Logger 中间件：slog JSON，含 req_id/path/method/dur_ms/status（§10.7）。
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" {
			id = reqID()
		}
		c.Set("req_id", id)
		c.Header("X-Request-Id", id)
		start := time.Now()
		c.Next()
		slog.Info("http",
			"req_id", id,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"dur_ms", time.Since(start).Milliseconds())
	}
}

// Recovery 中间件：panic → 500 INTERNAL 统一错误体。
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		slog.Error("panic recovered", "req_id", c.GetString("req_id"), "err", recovered)
		c.JSON(http.StatusInternalServerError, gin.H{"error": &apierr.Error{
			Code:    apierr.CodeInternal,
			Message: "服务器内部错误",
			Details: map[string]any{"request_id": c.GetString("req_id")},
		}})
	})
}

// Auth Bearer 校验。tokenFn 每次请求取当前 token（支持运行期轮换）。
func Auth(tokenFn func() (string, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": &apierr.Error{
				Code: apierr.CodeUnauthorized, Message: "缺少访问令牌，请先登录"}})
			return
		}
		want, err := tokenFn()
		if err != nil || want == "" {
			slog.Error("读取 auth_token 失败", "err", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": &apierr.Error{
				Code: apierr.CodeInternal, Message: "服务器内部错误"}})
			return
		}
		if strings.TrimPrefix(h, "Bearer ") != want {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": &apierr.Error{
				Code: apierr.CodeUnauthorized, Message: "访问令牌无效"}})
			return
		}
		c.Next()
	}
}
