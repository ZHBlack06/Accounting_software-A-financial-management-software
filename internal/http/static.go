package http

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"ledger/internal/pkg/apierr"
)

// apiNotFound 返回统一 JSON 404（与 API 错误体一致）。
func apiNotFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"error": &apierr.Error{
		Code: apierr.CodeNotFound, Message: "接口不存在"}})
}

// notBuiltPage 前端产物缺失时的内联提示页（避免 500，也避免误判为可用前端）。
func notBuiltPage() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || c.Request.URL.Path == "/api" {
			apiNotFound(c)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(
			`<!doctype html><meta charset="utf-8"><title>个人记账</title>
			<body style="font-family:sans-serif;padding:2em">
			<h2>个人记账后端已启动</h2>
			<p>前端尚未构建。请先执行 <code>cd web &amp;&amp; npm ci &amp;&amp; npm run build</code> 生成 web/dist。</p>
			<p>API 健康检查：<a href="/api/health">/api/health</a></p></body>`))
	}
}

// staticFallback 构造 SPA 静态资源兜底处理器（gin NoRoute）。
//
// 规则（v1.1 修复 v1.0 遗留 P2-4：缺失静态资源不再返回 200 + index.html）：
//  1. /api/ 前缀 → JSON 404；
//  2. 命中 webDir 下的真实文件 → 直接返回；
//  3. 未命中且路径带扩展名（/assets/x.js、/favicon.ico）→ 404 纯文本；
//  4. 其余（无扩展名的前端路由，如 /transactions）→ index.html（SPA fallback）。
//
// 路径先以 "/" 前缀 Clean，阻止 ../ 逃逸到 webDir 之外。
// 缓存策略（v1.2 修复「打包后仍显示旧界面」）：
//   - 入口 index.html 与 SPA fallback 一律 no-store：入口一旦被 WebView2/浏览器缓存，
//     就会引用上一版本已不存在的 assets/<hash>.js，表现为「升级后界面没变/功能缺失」；
//   - /assets/ 下的文件名自带内容哈希，可放心长缓存 immutable。
func setStaticCacheHeaders(c *gin.Context, rel string) {
	if strings.HasPrefix(rel, "assets/") {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

func staticFallback(webDir string) gin.HandlerFunc {
	if webDir == "" {
		return notBuiltPage()
	}
	if st, err := os.Stat(webDir); err != nil || !st.IsDir() {
		return notBuiltPage()
	}
	index := filepath.Join(webDir, "index.html")
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") || p == "/api" {
			apiNotFound(c)
			return
		}
		// rel 为去掉前导分隔符后的相对路径；filepath.IsLocal 拒绝 .. 与绝对路径，
		// 保证不会读到 webDir 之外的文件。
		rel := strings.TrimLeft(filepath.Clean("/"+p), `/\`)
		if rel != "" {
			if filepath.IsLocal(rel) {
				if fp := filepath.Join(webDir, rel); fp != webDir {
					if st, err := os.Stat(fp); err == nil && !st.IsDir() {
						setStaticCacheHeaders(c, filepath.ToSlash(rel))
						c.File(fp)
						return
					}
				}
			}
			if filepath.Ext(rel) != "" {
				c.String(http.StatusNotFound, "404 not found: %s", p)
				return
			}
		}
		if _, err := os.Stat(index); err != nil {
			notBuiltPage()(c)
			return
		}
		setStaticCacheHeaders(c, "index.html")
		c.File(index) // SPA fallback
	}
}
