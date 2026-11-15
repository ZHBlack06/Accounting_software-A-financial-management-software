package desktop

import (
	"errors"
	"fmt"
	"log/slog"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 常量（只保留用到的部分，避免额外依赖）。
const (
	swRestore = 9 // SW_RESTORE

	mbOK            = 0x00000000
	mbIconError     = 0x00000010
	mbIconWarning   = 0x00000030
	mbSetForeground = 0x00010000

	errorAlreadyExists = 183 // ERROR_ALREADY_EXISTS

	wmSetIcon   = 0x0080 // WM_SETICON
	iconSmall   = 0      // ICON_SMALL
	iconBig     = 1      // ICON_BIG
	imageIcon   = 1      // IMAGE_ICON
	lrDefaultSz = 0x00000040
	appIconID   = 1 // 与 assets/app.rc 中的资源 ID 一致
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procFindWindowW         = user32.NewProc("FindWindowW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procMessageBoxW         = user32.NewProc("MessageBoxW")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procCreateMutexW        = kernel32.NewProc("CreateMutexW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

// rect 对应 Win32 RECT。
type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// errWebView2Missing 表示 WebView2 运行时无法初始化（未安装或版本不兼容）。
var errWebView2Missing = errors.New("WebView2 运行时初始化失败")

// MessageBox 弹出系统消息框（-H windowsgui 构建下唯一的用户提示渠道）。
func MessageBox(title, text string, flags uint32) {
	caption, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	body, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	_, _, _ = procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(caption)), uintptr(flags))
}

// acquireSingleInstance 创建命名互斥量判断是否已有实例。
// 返回的 handle 必须在进程存活期间保持打开；isFirst=false 表示已有实例在运行。
//
// 注意：必须使用 Proc.Call 直接返回的 lastErr（它在系统调用返回的同一线程内
// 由 GetLastError 捕获）判断 ERROR_ALREADY_EXISTS；另起一次 GetLastError 调用
// 会因为 goroutine 可能迁移到其它 OS 线程而读到错误值（v1.1 联调实测踩过）。
func acquireSingleInstance(name string) (handle windows.Handle, isFirst bool, err error) {
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, false, fmt.Errorf("互斥量名非法: %w", err)
	}
	r, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(ptr)))
	if r == 0 {
		return 0, false, fmt.Errorf("CreateMutexW 失败: %w", lastErr)
	}
	if errno, ok := lastErr.(syscall.Errno); ok && uintptr(errno) == errorAlreadyExists {
		return windows.Handle(r), false, nil
	}
	return windows.Handle(r), true, nil
}

// releaseSingleInstance 关闭互斥量句柄。
func releaseSingleInstance(h windows.Handle) {
	if h != 0 {
		_ = windows.CloseHandle(h)
	}
}

// findWindow 按标题查找顶层窗口，未找到返回 0。
func findWindow(title string) uintptr {
	ptr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(ptr)))
	return hwnd
}

// focusExistingWindow 恢复并前置已有实例窗口；找到返回 true。
// SW_RESTORE 本身即可把最小化窗口还原，无需先判断 IsIconic。
func focusExistingWindow(title string) bool {
	hwnd := findWindow(title)
	if hwnd == 0 {
		return false
	}
	_, _, _ = procShowWindow.Call(hwnd, swRestore)
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	return true
}

// windowSize 读取窗口外部尺寸（含边框），hwnd 失效时返回 ok=false。
func windowSize(hwnd uintptr) (width, height int, ok bool) {
	if hwnd == 0 {
		return 0, 0, false
	}
	var r rect
	ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ret == 0 {
		return 0, 0, false
	}
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// showWebView2MissingHint 提示用户安装 WebView2 运行时。
func showWebView2MissingHint() {
	MessageBox(windowTitle,
		"未能启动内嵌浏览器组件，需要安装 Microsoft Edge WebView2 运行时。\n\n"+
			"Windows 10 1803+ 与 Windows 11 通常已自带；若缺失，请下载安装后重试：\n"+
			"https://go.microsoft.com/fwlink/p/?LinkId=2124703",
		mbOK|mbIconError|mbSetForeground)
}

// showError 弹出错误提示（标题固定，正文含原因）。
func showError(caption string, err error) {
	text := caption
	if err != nil {
		text = caption + "\n\n" + err.Error()
	}
	MessageBox(windowTitle, text, mbOK|mbIconError|mbSetForeground)
}

// setWindowIcon 把 exe 内嵌的图标资源设到窗口标题栏与任务栏。
//
// 资源由 assets/app.rc 编译成 rsrc.syso 参与链接（ID=1）。失败只记日志：
// 图标属于外观，不能因为取不到就让程序起不来。
func setWindowIcon(hwnd uintptr, title string) {
	if hwnd == 0 {
		// 个别 WebView2 版本在 Run 之前拿不到窗口句柄，按标题兜底查找。
		hwnd = findWindow(title)
	}
	if hwnd == 0 {
		slog.Warn("窗口句柄不可用，跳过图标设置")
		return
	}
	hInst, _, _ := procGetModuleHandleW.Call(0)
	if hInst == 0 {
		slog.Warn("取模块句柄失败，跳过图标设置")
		return
	}
	big, _, _ := procLoadImageW.Call(hInst, appIconID, imageIcon, 0, 0, lrDefaultSz|0x8000)
	small, _, _ := procLoadImageW.Call(hInst, appIconID, imageIcon, 16, 16, 0x8000)
	if big != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconBig, big)
	}
	if small != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
	}
	if big == 0 && small == 0 {
		slog.Warn("未找到内嵌图标资源，沿用系统默认图标")
		return
	}
	slog.Info("窗口图标已设置")
}
