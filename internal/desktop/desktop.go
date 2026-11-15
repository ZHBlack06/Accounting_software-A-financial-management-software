// Package desktop 实现个人记账 v1.2 桌面壳：
// 单实例互斥、内嵌后端、内嵌前端资源、WebView2 独立窗口、自动登录与优雅退出。
package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	webview2 "github.com/jchv/go-webview2"

	"ledger/internal/app"
	"ledger/internal/config"
	webassets "ledger/web"
)

// windowTitle 同时用于窗口标题、FindWindow 查找与错误弹窗（三处必须一致）。
// **从 config.Version 运行时推导，不要硬编码**：写死版本号会导致「改了 config.Version
// 但窗口还显示旧版本」，用户会误判为「没更新」。
var windowTitle = buildWindowTitle(config.Version)

// buildWindowTitle 由版本号生成窗口标题：去掉末尾补丁位（1.2.0 → "个人记账 v1.2"）。
func buildWindowTitle(version string) string {
	v := strings.TrimSpace(version)
	v = strings.TrimSuffix(v, ".0")
	return "个人记账 v" + v
}

const (
	// singleInstanceMutex 单实例互斥量。**v1.2 必须与 v1.1 不同名**
	// （v1.1 是 `Local\JizhangDesktopV11`）：同名时若 v1.1 正在运行，双击 v1.2 会
	// 立即退出并把 v1.1 的窗口顶到前面，用户看到的是旧界面（旧标题、无新功能），
	// 极容易被误判为「升级没生效」。改名为 V12 后两版可并存（数据目录仍共用）。
	singleInstanceMutex = `Local\JizhangDesktopV12`

	defaultWindowWidth  = 1280
	defaultWindowHeight = 860

	// readyTimeout 是等待 /api/health 返回 200 的上限。
	readyTimeout = 15 * time.Second
	// shutdownTimeout 是关闭窗口后优雅退出服务端的上限。
	shutdownTimeout = 5 * time.Second
	// assetsPrefix 是内嵌文件系统内的前端根目录。
	assetsPrefix = "dist"
)

// Options 是桌面壳启动参数（由 cmd/desktop 从命令行解析后传入）。
type Options struct {
	DataDir       string // --data-dir；空表示按环境变量/系统目录推断
	Debug         bool   // --debug：开启 WebView2 右键菜单与 DevTools
	PreferredPort int    // 首选端口，被占用时自动改用系统分配端口
	ResetUICache  bool   // --reset-ui-cache：启动前清空 WebView2 缓存与解压出的前端资源
}

// Run 启动桌面版并阻塞到窗口关闭，返回进程退出码。
func Run(opts Options) int {
	if opts.PreferredPort <= 0 {
		opts.PreferredPort = 8770
	}
	return run(opts)
}

// run 是 Run 的实际实现，所有失败路径都返回退出码并已记录原因。
func run(opts Options) (code int) {
	dataDir, err := ResolveDataDir(opts.DataDir)
	if err != nil {
		showError("数据目录不可用", err)
		return 1
	}
	closeLog, logPath, err := SetupLogging(dataDir)
	if err != nil {
		showError("日志初始化失败", err)
		return 1
	}
	defer func() {
		if closeErr := closeLog(); closeErr != nil {
			slog.Warn("关闭日志文件失败", "err", closeErr)
		}
	}()
	// defer 后进先出：本 defer 比 closeLog 更晚注册，因此会先执行，
	// 保证"桌面版退出"这条日志能写进文件而不是写到已关闭的句柄上。
	defer func() { slog.Info("桌面版退出", "code", code) }()
	slog.Info("桌面版启动", "version", config.Version, "pid", os.Getpid(),
		"data_dir", dataDir, "log", logPath, "debug", opts.Debug)

	// 0. 可选：清理界面缓存（排障用）。只删浏览器缓存与解压出的前端资源，
	// 绝不触碰 ledger.db / 备份 / 截图等业务数据。
	if opts.ResetUICache {
		resetUICache(dataDir)
	}

	// 1. 单实例：已有实例则唤出它的窗口并安静退出（退出码 0）。
	mutex, isFirst, err := acquireSingleInstance(singleInstanceMutex)
	if err != nil {
		slog.Error("单实例互斥量创建失败", "err", err)
		showError("启动失败：单实例检查异常", err)
		return 1
	}
	if !isFirst {
		focused := focusExistingWindow(windowTitle)
		slog.Info("检测到已有实例在运行，本进程退出", "window_focused", focused, "pid", os.Getpid())
		releaseSingleInstance(mutex)
		return 0
	}
	defer releaseSingleInstance(mutex)

	// 2. 解压内嵌前端资源（按内容哈希分目录，同版本复用）。
	webDir, err := ExtractAssets(webassets.Dist, assetsPrefix, dataDir)
	if err != nil {
		slog.Error("前端资源解压失败", "err", err)
		showError("启动失败：前端资源解压失败", err)
		return 1
	}

	// 3. 装配并启动内嵌后端。
	cfg := config.Default().ApplyEnv()
	cfg.Version = config.Version
	cfg.DataDir = dataDir
	cfg.WebDir = webDir
	cfg.Host = "127.0.0.1"
	cfg.Port = opts.PreferredPort
	cfg.PrintToken = false

	application, err := app.New(cfg)
	if err != nil {
		slog.Error("后端装配失败", "err", err)
		showError("启动失败：数据初始化异常（详见日志 "+logPath+"）", err)
		return 1
	}
	started := false
	defer func() {
		if started {
			return
		}
		shutdownApp(application)
	}()

	if startErr := application.Start(); startErr != nil {
		// 首选端口（默认 8770）被别的程序占用时，改让内核分配空闲端口。
		slog.Warn("首选端口不可用，改用系统分配端口", "port", cfg.Port, "err", startErr)
		cfg.Port = 0
		if startErr = application.Start(); startErr != nil {
			slog.Error("内置服务监听失败", "err", startErr)
			showError("启动失败：内置服务无法监听端口", startErr)
			return 1
		}
	}
	started = true
	baseURL := application.URL()
	slog.Info("内置服务已就绪", "addr", application.Addr(), "url", baseURL, "version", cfg.Version)

	// 4. 等待后端真正可用，避免窗口先加载导致白屏 502。
	if err := waitReady(baseURL, readyTimeout); err != nil {
		slog.Error("等待服务就绪超时", "url", baseURL, "err", err)
		shutdownApp(application)
		showError("启动失败：内置服务在 15 秒内未就绪", err)
		return 1
	}

	token, tokErr := application.Meta.Get("auth_token")
	if tokErr != nil || token == "" {
		slog.Warn("读取访问令牌失败，将需要在界面手动登录", "err", tokErr)
	}

	// 5. 创建窗口。
	state := loadWindowState(dataDir, defaultWindowWidth, defaultWindowHeight)
	view := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     opts.Debug,
		DataPath:  webView2DataPath(dataDir),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  windowTitle,
			Width:  uint(state.Width),
			Height: uint(state.Height),
			Center: true,
		},
	})
	if view == nil {
		slog.Error("WebView2 初始化失败", "reason", errWebView2Missing)
		shutdownApp(application)
		showWebView2MissingHint()
		return 1
	}

	// 自动登录：页面脚本执行前写入令牌，用户打开即进入首页，无需手动粘贴。
	if token != "" {
		view.Init(injectTokenScript(token))
	}

	stopTracker := trackWindowSize(uintptr(view.Window()), dataDir)
	installInterruptHandler(view)
	// 标题栏与任务栏图标：WebView2 自建窗口默认使用系统通用图标，
	// 这里从 exe 内嵌的图标资源（rsrc.syso，ID=1）取图并 WM_SETICON 设置。
	setWindowIcon(uintptr(view.Window()), windowTitle)

	view.Navigate(baseURL + "/")
	slog.Info("窗口已创建", "width", state.Width, "height", state.Height, "url", baseURL)

	view.Run() // 阻塞到窗口关闭

	stopTracker()
	slog.Info("窗口已关闭，正在关闭内置服务")
	shutdownApp(application)
	CleanupOldAssets(dataDir, webDir)
	return 0
}

// waitReady 轮询 /api/health 直到返回 200 或超时。
// 使用禁用代理的 Transport：本机回环地址不能走系统 http_proxy，
// 否则装了代理软件的机器会拿到 502 而误判"服务未就绪"。
func waitReady(baseURL string, timeout time.Duration) error {
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil,
			MaxIdleConns:          2,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 2 * time.Second,
		},
	}
	defer client.CloseIdleConnections()
	url := baseURL + "/api/health"
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				slog.Info("健康检查通过", "url", url, "status", resp.StatusCode)
				return nil
			}
			lastErr = fmt.Errorf("health 返回 %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(150 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("超时 %s", timeout)
	}
	return lastErr
}

// injectTokenScript 生成注入脚本：仅在 localStorage 无令牌时写入，避免覆盖用户手动输入。
func injectTokenScript(token string) string {
	quoted, err := json.Marshal(token)
	if err != nil {
		return ""
	}
	return "try{if(!localStorage.getItem('token')){localStorage.setItem('token'," +
		string(quoted) + ")}}catch(e){}"
}

// installInterruptHandler 让 Ctrl+C / 控制台关闭信号也能正常关窗退出。
func installInterruptHandler(view webview2.WebView) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		for range sigCh {
			slog.Info("收到中断信号，关闭窗口")
			// Dispatch 保证在 UI 线程执行 Destroy（Terminate 只能在创建线程调用）。
			view.Dispatch(func() { view.Destroy() })
		}
	}()
}

// shutdownApp 以固定超时优雅关闭后端（忽略返回错误，只记录日志）。
func shutdownApp(application *app.App) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := application.Shutdown(ctx); err != nil {
		slog.Error("关闭内置服务时出现错误", "err", err)
		return
	}
	slog.Info("内置服务已关闭")
}

// webView2DataPath 返回 WebView2 用户数据目录（浏览器缓存/配置落在数据目录内，卸载时一并清理）。
//
// 目录按**应用版本**隔离：v1.1 与 v1.2 共用同一个业务数据目录，但都监听 127.0.0.1:8770。
// 若共用浏览器缓存，升级后 WebView2 可能复用上一版缓存的 index.html，而它引用的
// assets/<旧hash>.js 在新版本里已不存在 —— 表现为「升级后界面没变、功能缺失」。
func webView2DataPath(dataDir string) string {
	return filepath.Join(dataDir, "webview2", "v"+desktopCacheVersion())
}

// desktopCacheVersion 返回用于隔离浏览器缓存的版本标识（1.2.0 → "1.2"）。
func desktopCacheVersion() string {
	v := strings.TrimSpace(config.Version)
	if v == "" {
		return "unknown"
	}
	return strings.TrimSuffix(v, ".0")
}

// resetUICache 清理界面层缓存（排障用）：WebView2 浏览器缓存 + 解压出的前端资源。
// 业务数据（ledger.db、backups、screenshots、logs）不受影响。
func resetUICache(dataDir string) {
	targets := []string{
		filepath.Join(dataDir, "webview2"),
		filepath.Join(dataDir, "web-assets"),
	}
	for _, t := range targets {
		if err := os.RemoveAll(t); err != nil {
			slog.Warn("清理界面缓存失败", "dir", t, "err", err)
			continue
		}
		slog.Info("已清理界面缓存", "dir", t)
	}
}
