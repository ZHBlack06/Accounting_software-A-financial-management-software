package desktop

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ledger/internal/config"
)

// appFolderName 是 %LOCALAPPDATA% 下的应用目录名。
const appFolderName = "个人记账"

// logKeepCount 是桌面日志保留的最大文件数（按日期名滚动的最近 N 天）。
const logKeepCount = 7

// ResolveDataDir 解析数据目录，优先级：显式参数（--data-dir） > LEDGER_DATA_DIR >
// %LOCALAPPDATA%\个人记账\data（LOCALAPPDATA 缺失时退回 os.UserConfigDir）。
// 不硬编码用户名，全部由系统环境变量给出。
func ResolveDataDir(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	if v := os.Getenv("LEDGER_DATA_DIR"); v != "" {
		return filepath.Abs(v)
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		cfgDir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("无法确定数据目录（LOCALAPPDATA 与 UserConfigDir 均不可用）: %w", err)
		}
		base = cfgDir
	}
	return filepath.Join(base, appFolderName, "data"), nil
}

// SetupLogging 初始化桌面版日志：写入 <dataDir>/logs/desktop-YYYYMMDD.log，
// 同时输出到 stderr（有控制台时可见，-H windowsgui 下 stderr 写入失败不影响文件日志）。
// 返回关闭函数与日志文件路径。
func SetupLogging(dataDir string) (func() error, string, error) {
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, "", fmt.Errorf("创建日志目录 %s 失败: %w", logDir, err)
	}
	name := "desktop-" + time.Now().In(config.Local()).Format("20060102") + ".log"
	path := filepath.Join(logDir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("打开日志文件 %s 失败: %w", path, err)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(
		io.MultiWriter(f, os.Stderr), &slog.HandlerOptions{Level: slog.LevelInfo})))

	rotateLogs(logDir, logKeepCount)
	return f.Close, path, nil
}

// rotateLogs 只保留日期名最大的 keep 个 desktop-*.log（日期名天然有序）。
func rotateLogs(logDir string, keep int) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "desktop-") && strings.HasSuffix(n, ".log") {
			names = append(names, n)
		}
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(logDir, n)); err != nil {
			slog.Warn("清理旧日志失败", "file", n, "err", err)
		}
	}
}
