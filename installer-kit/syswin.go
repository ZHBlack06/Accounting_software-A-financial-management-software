package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 通过 LazySystemDLL 调用少量 Win32 API，避免引入 cgo。
var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procCreateMutexW        = kernel32.NewProc("CreateMutexW")
	procGetDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
	procCloseHandle         = kernel32.NewProc("CloseHandle")
	procMessageBoxW         = user32.NewProc("MessageBoxW")
)

// 消息框按钮类型（避免依赖外部常量包）。
const (
	mbOK              = 0x00000000
	mbIconError       = 0x00000010
	mbIconInformation = 0x00000040
)

// acquireSingleInstance 创建一个会话内命名互斥量。
// 返回的第二个值表示「已有实例在运行」，第三个值是错误信息。
func acquireSingleInstance(name string) (windows.Handle, bool, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, false, err
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if h == 0 {
		return 0, false, fmt.Errorf("创建互斥量失败：%v", callErr)
	}
	if callErr != nil {
		if errno, ok := callErr.(syscall.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
			return windows.Handle(h), true, nil
		}
	}
	return windows.Handle(h), false, nil
}

// releaseHandle 关闭内核句柄。
func releaseHandle(h windows.Handle) {
	if h == 0 {
		return
	}
	_, _, _ = procCloseHandle.Call(uintptr(h))
}

// DiskFreeBytes 查询指定路径所在卷的可用字节数。
func DiskFreeBytes(path string) (uint64, error) {
	probe := existingVolumePath(path)
	p, err := windows.UTF16PtrFromString(probe)
	if err != nil {
		return 0, err
	}
	var freeForCaller, totalBytes, totalFree uint64
	r, _, callErr := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeForCaller)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, fmt.Errorf("查询磁盘空间失败（%s）：%v", probe, callErr)
	}
	return freeForCaller, nil
}

// ShowMessageBox 弹出一个原生消息框（用于 WebView2 不可用等极端情况）。
func ShowMessageBox(title, text string, isError bool) {
	t, err1 := windows.UTF16PtrFromString(title)
	c, err2 := windows.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", title, text)
		return
	}
	flags := uintptr(mbOK | mbIconInformation)
	if isError {
		flags = mbOK | mbIconError
	}
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)), flags)
}

// expandWinEnv 展开 Windows 风格的 %VAR% 环境变量。
// 注意 os.Expand 只识别 $VAR 语法，因此这里必须自行实现 %VAR% 的解析，
// 否则 User Shell Folders 中形如 "%USERPROFILE%\AppData\..." 的值不会被展开。
func expandWinEnv(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '%' {
			if j := strings.IndexByte(s[i+1:], '%'); j > 0 {
				name := s[i+1 : i+1+j]
				if v, ok := os.LookupEnv(name); ok {
					b.WriteString(v)
					i += j + 2
					continue
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// readUserShellFolder 从 HKCU 的 User Shell Folders 读取用户目录（如 Desktop）。
// 该注册表值类型为 REG_EXPAND_SZ，需要手动展开环境变量。
func readUserShellFolder(name string) (string, error) {
	const path = `Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders`
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return "", err
	}
	v = strings.TrimSpace(expandWinEnv(v))
	if v == "" {
		return "", fmt.Errorf("注册表项 %s 为空", name)
	}
	return v, nil
}
