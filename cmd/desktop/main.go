// 个人记账软件 v1.2 桌面版入口：双击后打开 WebView2 独立窗口，内置后端自动启动。
//
// 构建（无控制台窗口、单文件、纯 Go 无 cgo）：
//
//	CGO_ENABLED=0 GOOS=windows go build -trimpath \
//	  -ldflags "-H windowsgui -s -w" -o ledger-desktop.exe ./cmd/desktop
package main

import (
	"flag"
	"os"
	"runtime"

	"ledger/internal/desktop"
)

func main() {
	// WebView2 的窗口与消息循环必须固定在同一 OS 线程上，否则 Dispatch 会投递到错误线程。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var (
		dataDir      string
		debug        bool
		port         int
		resetUICache bool
	)
	flag.StringVar(&dataDir, "data-dir", "",
		"数据目录（默认 %LOCALAPPDATA%\\个人记账\\data，可用 LEDGER_DATA_DIR 覆盖）")
	flag.BoolVar(&debug, "debug", false, "开启 WebView2 调试（右键菜单与 DevTools）")
	flag.IntVar(&port, "port", 8770, "首选监听端口，被占用时自动改用系统分配端口")
	flag.BoolVar(&resetUICache, "reset-ui-cache", false,
		"启动前清空界面缓存（WebView2 缓存与解压出的前端资源），用于排查「界面没更新」，不影响账本数据")
	flag.Parse()

	os.Exit(desktop.Run(desktop.Options{
		DataDir:       dataDir,
		Debug:         debug,
		PreferredPort: port,
		ResetUICache:  resetUICache,
	}))
}
