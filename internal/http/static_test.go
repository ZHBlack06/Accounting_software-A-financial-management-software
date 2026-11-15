package http

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// newStaticTestEngine 构造仅带 NoRoute 兜底路由的引擎（不装配 Auth/Meta 依赖）。
func newStaticTestEngine(t *testing.T, webDir string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(staticFallback(webDir))
	return r
}

// prepareDist 在临时目录生成最小前端产物：index.html + assets/app-abc.js。
func prepareDist(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("创建 assets 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte("<!doctype html><title>个人记账</title><div id=app></div>"), 0o644); err != nil {
		t.Fatalf("写 index.html 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app-abc.js"),
		[]byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("写 assets/app-abc.js 失败: %v", err)
	}
	return dir
}

// doGet 发起一次请求，返回状态码与响应体。
func doGet(r *gin.Engine, path string) (int, string) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// TestStaticFallback 覆盖 P2-4 修复后的四类静态路由行为。
func TestStaticFallback(t *testing.T) {
	dist := prepareDist(t)
	r := newStaticTestEngine(t, dist)

	t.Run("真实文件正常返回", func(t *testing.T) {
		code, body := doGet(r, "/assets/app-abc.js")
		if code != http.StatusOK {
			t.Fatalf("真实资源应 200，实际 %d", code)
		}
		if !strings.Contains(body, "console.log") {
			t.Fatalf("响应体不是目标文件内容: %q", body)
		}
	})

	t.Run("缺失的带扩展名资源返回404且不回落index", func(t *testing.T) {
		code, body := doGet(r, "/assets/nope-xyz.js")
		if code != http.StatusNotFound {
			t.Fatalf("缺失资源应 404，实际 %d", code)
		}
		if strings.Contains(body, "id=app") || strings.Contains(body, "<title>") {
			t.Fatalf("缺失资源不应返回 index.html，实际响应体: %q", body)
		}
	})

	t.Run("缺失的图标等其它扩展名返回404", func(t *testing.T) {
		if code, _ := doGet(r, "/favicon.ico"); code != http.StatusNotFound {
			t.Fatalf("/favicon.ico 应 404，实际 %d", code)
		}
	})

	t.Run("无扩展名前端路由回落index", func(t *testing.T) {
		for _, p := range []string{"/", "/transactions", "/settings/accounts"} {
			code, body := doGet(r, p)
			if code != http.StatusOK {
				t.Fatalf("%s 应 200，实际 %d", p, code)
			}
			if !strings.Contains(body, "id=app") {
				t.Fatalf("%s 应返回 index.html，实际 %q", p, body)
			}
		}
	})

	t.Run("api前缀返回JSON404", func(t *testing.T) {
		code, body := doGet(r, "/api/not-exist")
		if code != http.StatusNotFound {
			t.Fatalf("/api/not-exist 应 404，实际 %d", code)
		}
		if !strings.Contains(body, `"code"`) || !strings.Contains(body, "接口不存在") {
			t.Fatalf("/api/not-exist 应返回统一 JSON 错误体，实际 %q", body)
		}
	})
}

// TestStaticFallbackPathTraversal 保证 ../ 不能读到 webDir 之外的文件。
func TestStaticFallbackPathTraversal(t *testing.T) {
	dist := prepareDist(t)
	r := newStaticTestEngine(t, dist)

	code, body := doGet(r, "/../secret.txt")
	if code == http.StatusOK && strings.Contains(body, "secret") {
		t.Fatalf("路径穿越不应读到 webDir 之外的文件: %d %q", code, body)
	}
	code, body = doGet(r, "/assets/../../secret.txt")
	if code == http.StatusOK && strings.Contains(body, "secret") {
		t.Fatalf("路径穿越不应读到 webDir 之外的文件: %d %q", code, body)
	}
}

// TestStaticFallbackWithoutDist 前端未构建时返回内联提示页而不是 500。
func TestStaticFallbackWithoutDist(t *testing.T) {
	r := newStaticTestEngine(t, filepath.Join(t.TempDir(), "not-exists"))

	code, body := doGet(r, "/transactions")
	if code != http.StatusOK {
		t.Fatalf("前端缺失时应返回提示页 200，实际 %d", code)
	}
	if !strings.Contains(body, "前端尚未构建") {
		t.Fatalf("应返回构建提示页，实际 %q", body)
	}
	if code, body := doGet(r, "/api/health"); code != http.StatusNotFound ||
		!strings.Contains(body, "接口不存在") {
		t.Fatalf("api 404 应保持 JSON，实际 %d %q", code, body)
	}
}
