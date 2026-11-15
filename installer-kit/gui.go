package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	webview "github.com/jchv/go-webview2"
)

// 界面窗口尺寸（客户区）。
const (
	windowWidth  = 720
	windowHeight = 520
	// 外层窗口尺寸需包含边框与标题栏。
	outerWidth  = 736
	outerHeight = 596
)

// guiServer 承载本地 HTTP 服务：WebView2 页面通过 fetch 调用这些接口，
// 这样无需依赖 WebView 的 JS 绑定能力，进度与日志也更容易做增量推送。
type guiServer struct {
	mode string // "install" 或 "uninstall"
	opts cliOptions
	prog *Progress
	meta PayloadMeta

	mu      sync.Mutex
	started bool
	exit    func()
	code    int
}

// stateResponse 是 /api/state 的响应体。
type stateResponse struct {
	Mode            string `json:"mode"`
	Product         string `json:"product"`
	Version         string `json:"version"`
	DisplayVersion  string `json:"displayVersion"`
	Dir             string `json:"dir"`
	DefaultDir      string `json:"defaultDir"`
	PayloadSize     int64  `json:"payloadSize"`
	PayloadSizeText string `json:"payloadSizeText"`
	PayloadFiles    int    `json:"payloadFiles"`
	FreeSpace       int64  `json:"freeSpace"`
	FreeSpaceText   string `json:"freeSpaceText"`
	Existing        bool   `json:"existing"`
	ExistingVersion string `json:"existingVersion"`
	Installed       bool   `json:"installed"`
	DirSize         int64  `json:"dirSize"`
	DirSizeText     string `json:"dirSizeText"`
	DataExists      bool   `json:"dataExists"`
	DataDir         string `json:"dataDir"`
	DesktopLnk      bool   `json:"desktopLnk"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
}

// logResponse 是 /api/log 的响应体。
type logResponse struct {
	Lines     []LogLine `json:"lines"`
	Percent   int       `json:"percent"`
	Done      bool      `json:"done"`
	OK        bool      `json:"ok"`
	Error     string    `json:"error"`
	Dir       string    `json:"dir"`
	NextIndex int       `json:"nextIndex"`
}

// installRequest 是 /api/install 的请求体。
type installRequest struct {
	Dir       string `json:"dir"`
	Desktop   bool   `json:"desktop"`
	StartMenu bool   `json:"startMenu"`
	Launch    bool   `json:"launch"`
}

// uninstallRequest 是 /api/uninstall 的请求体。
type uninstallRequest struct {
	RemoveData bool `json:"removeData"`
}

// browseRequest 是 /api/browse 的请求体。
type browseRequest struct {
	Current string `json:"current"`
}

// simpleResponse 是通用响应体。
type simpleResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Path  string `json:"path,omitempty"`
}

// runInstallGUI 启动图形安装界面。
func runInstallGUI(opts cliOptions) int {
	title := WindowTitle + " 安装程序"
	handle, already, err := acquireSingleInstance(SingleInstanceMutexName)
	if err != nil {
		ShowMessageBox(title, err.Error(), true)
		return 1
	}
	defer releaseHandle(handle)
	if already {
		ShowMessageBox(title, "安装程序已经在运行中，请勿重复打开。", false)
		// 触发前台显示已有窗口：激活同标题窗口。
		return 0
	}

	s := &guiServer{
		mode: "install",
		opts: opts,
		prog: NewProgress(false),
		meta: loadPayloadMeta(),
	}
	return runWindow(title, s)
}

// runUninstallGUI 启动图形卸载界面。
func runUninstallGUI(opts cliOptions) int {
	title := WindowTitle + " 卸载程序"
	handle, already, err := acquireSingleInstance(SingleInstanceMutexName + `-uninstall`)
	if err != nil {
		ShowMessageBox(title, err.Error(), true)
		return 1
	}
	defer releaseHandle(handle)
	if already {
		ShowMessageBox(title, "卸载程序已经在运行中，请勿重复打开。", false)
		return 0
	}

	dir := opts.dir
	if strings.TrimSpace(dir) == "" {
		self, err := os.Executable()
		if err != nil {
			ShowMessageBox(title, "无法定位卸载程序自身路径："+err.Error(), true)
			return 1
		}
		dir = filepath.Dir(self)
	}
	opts.dir = filepath.Clean(dir)

	s := &guiServer{
		mode: "uninstall",
		opts: opts,
		prog: NewProgress(false),
		meta: loadPayloadMeta(),
	}
	return runWindow(title, s)
}

// runWindow 启动本地服务与 WebView2 窗口，阻塞直到窗口关闭。
func runWindow(title string, s *guiServer) int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ShowMessageBox(title, "无法启动本地界面服务："+err.Error(), true)
		return 1
	}

	mux := http.NewServeMux()
	s.register(mux)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		_ = server.Close()
	}()

	url := "http://" + listener.Addr().String() + "/"

	w := webview.NewWithOptions(webview.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		// 每个版本使用独立的 WebView2 数据目录，避免 v1.0 / v1.1 同时安装时互相锁文件。
		DataPath: filepath.Join(os.TempDir(), "ledger-installer-webview2-"+prof.Name),
		WindowOptions: webview.WindowOptions{
			Title:  title,
			Width:  outerWidth,
			Height: outerHeight,
			Center: true,
		},
	})
	if w == nil {
		ShowMessageBox(title,
			"无法创建界面窗口，请先安装 Microsoft Edge WebView2 运行时。\n\n"+
				"也可以使用静默模式安装：\n"+
				fmt.Sprintf(`setup.exe --silent --dir="D:\Apps\%s"`, prof.DefaultDirName), true)
		return 1
	}
	defer w.Destroy()

	w.SetSize(windowWidth, windowHeight, webview.HintFixed)
	w.Navigate(url)

	s.mu.Lock()
	s.exit = func() { w.Destroy() }
	s.mu.Unlock()

	w.Run()
	return s.exitCode()
}

// exitCode 返回进程退出码。
func (s *guiServer) exitCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code
}

// setExitCode 记录退出码。
func (s *guiServer) setExitCode(code int) {
	s.mu.Lock()
	s.code = code
	s.mu.Unlock()
}

// requestExit 请求关闭窗口。
func (s *guiServer) requestExit() {
	s.mu.Lock()
	fn := s.exit
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// register 注册全部 HTTP 路由。
func (s *guiServer) register(mux *http.ServeMux) {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic("内嵌界面资源缺失：" + err.Error())
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/browse", s.handleBrowse)
	mux.HandleFunc("/api/install", s.handleInstall)
	mux.HandleFunc("/api/uninstall", s.handleUninstall)
	mux.HandleFunc("/api/log", s.handleLog)
	mux.HandleFunc("/api/exit", s.handleExit)
}

// handleState 返回界面初始化所需信息。
func (s *guiServer) handleState(w http.ResponseWriter, r *http.Request) {
	resp := stateResponse{
		Mode:            s.mode,
		Product:         ProductName,
		Version:         ProductVersion,
		DisplayVersion:  DisplayVersion,
		DefaultDir:      defaultInstallDir(),
		PayloadSize:     s.meta.UncompressedSize,
		PayloadSizeText: humanSize(s.meta.UncompressedSize),
		PayloadFiles:    s.meta.Files,
	}
	if s.mode == "uninstall" {
		dir := s.opts.dir
		resp.Dir = dir
		resp.Installed = dirExists(dir)
		resp.Existing = resp.Installed
		resp.DirSize = dirSize(dir)
		resp.DirSizeText = humanSize(resp.DirSize)
		// v1.0 的数据目录在安装目录内，v1.1 起固定在 %LOCALAPPDATA%\个人记账\data。
		resp.DataDir = DataDirLabel(dir)
		resp.DataExists = dirExists(DataDirPath(dir))
		resp.DesktopLnk = fileExists(desktopShortcutPath())
		if info := ReadInstallInfo(dir); info != nil {
			resp.ExistingVersion = info.Version
		}
	} else {
		dir := defaultInstallDir()
		if strings.TrimSpace(s.opts.dir) != "" {
			dir = resolveInstallDir(s.opts.dir)
		}
		resp.Dir = dir
		resp.DataDir = DataDirLabel(dir)
		resp.DataExists = dirExists(DataDirPath(dir))
		resp.DesktopLnk = fileExists(desktopShortcutPath())
		if info := ReadInstallInfo(dir); info != nil {
			resp.Existing = true
			resp.ExistingVersion = info.Version
			resp.Installed = true
		} else if UninstallEntryExists() {
			resp.Existing = true
			resp.Installed = true
		}
		if free, err := DiskFreeBytes(dir); err == nil {
			resp.FreeSpace = int64(free)
			resp.FreeSpaceText = humanSize(int64(free))
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleBrowse 打开系统「选择文件夹」对话框。
func (s *guiServer) handleBrowse(w http.ResponseWriter, r *http.Request) {
	var req browseRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Current == "" {
		req.Current = defaultInstallDir()
	}
	path, err := chooseFolder(req.Current)
	if err != nil {
		writeJSON(w, http.StatusOK, simpleResponse{OK: false, Error: "打开文件夹对话框失败：" + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, simpleResponse{OK: true, Path: path})
}

// handleInstall 启动安装流程（异步）。
func (s *guiServer) handleInstall(w http.ResponseWriter, r *http.Request) {
	if s.mode != "install" {
		writeJSON(w, http.StatusBadRequest, simpleResponse{OK: false, Error: "当前模式不支持安装"})
		return
	}
	var req installRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, simpleResponse{OK: false, Error: "请求格式错误"})
		return
	}
	dir, err := ValidateInstallDir(req.Dir)
	if err != nil {
		writeJSON(w, http.StatusOK, simpleResponse{OK: false, Error: err.Error()})
		return
	}
	if err := s.begin(); err != nil {
		writeJSON(w, http.StatusOK, simpleResponse{OK: false, Error: err.Error()})
		return
	}
	s.prog.Reset()
	s.opts.dir = dir
	go func() {
		finalDir, installErr := Install(InstallOptions{
			Dir:               dir,
			DesktopShortcut:   req.Desktop,
			StartMenuShortcut: req.StartMenu,
			Launch:            req.Launch,
			Progress:          s.prog,
		})
		s.prog.Finish(installErr, finalDir)
		if installErr != nil {
			s.setExitCode(1)
			s.prog.Log(LevelError, "安装失败：%v", installErr)
		}
	}()
	writeJSON(w, http.StatusOK, simpleResponse{OK: true, Path: dir})
}

// handleUninstall 启动卸载流程（异步）。
func (s *guiServer) handleUninstall(w http.ResponseWriter, r *http.Request) {
	if s.mode != "uninstall" {
		writeJSON(w, http.StatusBadRequest, simpleResponse{OK: false, Error: "当前模式不支持卸载"})
		return
	}
	var req uninstallRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := s.begin(); err != nil {
		writeJSON(w, http.StatusOK, simpleResponse{OK: false, Error: err.Error()})
		return
	}
	s.prog.Reset()
	dir := s.opts.dir
	go func() {
		uninstallErr := Uninstall(dir, req.RemoveData, s.prog)
		s.prog.Finish(uninstallErr, dir)
		if uninstallErr != nil {
			s.setExitCode(1)
			s.prog.Log(LevelError, "卸载失败：%v", uninstallErr)
		}
	}()
	writeJSON(w, http.StatusOK, simpleResponse{OK: true, Path: dir})
}

// handleLog 返回增量日志与整体状态。
func (s *guiServer) handleLog(w http.ResponseWriter, r *http.Request) {
	since := 0
	if v := r.URL.Query().Get("since"); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &since)
	}
	lines, percent, done, ok, errMsg, dir := s.prog.Snapshot(since)
	if lines == nil {
		lines = []LogLine{}
	}
	writeJSON(w, http.StatusOK, logResponse{
		Lines:     lines,
		Percent:   percent,
		Done:      done,
		OK:        ok,
		Error:     errMsg,
		Dir:       dir,
		NextIndex: s.prog.LineCount(),
	})
}

// handleExit 关闭窗口。
func (s *guiServer) handleExit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, simpleResponse{OK: true})
	go func() {
		time.Sleep(80 * time.Millisecond)
		s.requestExit()
	}()
}

// begin 标记流程已启动，防止重复提交。
func (s *guiServer) begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started && !s.prog.Done() {
		return errors.New("操作正在进行中，请稍候")
	}
	s.started = true
	return nil
}

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
