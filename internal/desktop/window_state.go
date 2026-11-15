package desktop

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// windowState 是窗口尺寸记忆（<dataDir>/window.json）。
type windowState struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// 窗口尺寸约束（含边框的外部尺寸）。
const (
	minWindowWidth    = 960
	minWindowHeight   = 640
	maxWindowWidth    = 6000
	maxWindowHeight   = 4000
	windowStateFile   = "window.json"
	saveStateInterval = 2 * time.Second
)

// statePath 返回窗口状态文件路径。
func statePath(dataDir string) string {
	return filepath.Join(dataDir, windowStateFile)
}

// loadWindowState 读取上次窗口尺寸；缺失或非法时返回默认尺寸。
func loadWindowState(dataDir string, defWidth, defHeight int) windowState {
	st := windowState{Width: defWidth, Height: defHeight}
	raw, err := os.ReadFile(statePath(dataDir))
	if err != nil {
		return st
	}
	var saved windowState
	if err := json.Unmarshal(raw, &saved); err != nil {
		slog.Warn("窗口状态文件损坏，使用默认尺寸", "err", err)
		return st
	}
	if saved.Width < minWindowWidth || saved.Width > maxWindowWidth ||
		saved.Height < minWindowHeight || saved.Height > maxWindowHeight {
		slog.Warn("窗口状态超出合理范围，使用默认尺寸",
			"width", saved.Width, "height", saved.Height)
		return st
	}
	return saved
}

// saveWindowState 写入窗口尺寸（失败只记日志，不影响退出）。
func saveWindowState(dataDir string, st windowState) {
	if st.Width < minWindowWidth || st.Height < minWindowHeight {
		return
	}
	raw, err := json.Marshal(st)
	if err != nil {
		slog.Warn("序列化窗口状态失败", "err", err)
		return
	}
	if err := os.WriteFile(statePath(dataDir), raw, 0o644); err != nil {
		slog.Warn("写入窗口状态失败", "err", err)
	}
}

// trackWindowSize 周期采样窗口尺寸，返回停止函数；停止后把最后一次有效尺寸落盘。
// hwnd 在窗口销毁后失效，此时采样失败自动跳过，保留最后一次有效值。
func trackWindowSize(hwnd uintptr, dataDir string) func() {
	stop := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var last windowState
	var hasLast bool

	go func() {
		ticker := time.NewTicker(saveStateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			if w, h, ok := windowSize(hwnd); ok {
				mu.Lock()
				last = windowState{Width: w, Height: h}
				hasLast = true
				mu.Unlock()
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(stop)
			mu.Lock()
			st, ok := last, hasLast
			mu.Unlock()
			if !ok {
				return
			}
			saveWindowState(dataDir, st)
			slog.Info("窗口尺寸已记忆", "width", st.Width, "height", st.Height)
		})
	}
}
