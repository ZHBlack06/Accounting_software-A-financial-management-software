package main

// 本文件集中承载「多版本（profile）」差异，其它源文件只使用这里导出的变量，
// 从而做到同一套安装器代码同时支持 v1.0（Web + 启动.bat）、v1.1 与 v1.2（桌面版 exe）。
//
// profile 在编译期通过 -ldflags "-X main.profileName=v1.1" 注入；
// 未注入时默认 v1.0，保证 v1.0 安装包构建方式与历史完全一致（无需额外参数）。

import (
	"os"
	"path/filepath"
)

// profileName 是当前编译目标版本标识，可被 -ldflags -X 覆盖。
var profileName = "v1.0"

// Profile 描述一个发行版本在安装器中的全部差异项。
//
// 设计原则：只描述「差异」，共性（产品名「个人记账」、卸载程序名、install.json 名）
// 保持为全局常量，避免过度参数化。
type Profile struct {
	// Name 是 profile 标识（v1.0 / v1.1 / v1.2）。
	Name string
	// Version 是语义化版本号，写入 install.json 与注册表 DisplayVersion。
	Version string
	// DisplayVersion 是界面展示用短版本号（v1.0 / v1.1 / v1.2）。
	DisplayVersion string
	// AppExeName 是主程序文件名。
	AppExeName string
	// LauncherName 是启动脚本文件名；空字符串表示该版本不使用启动脚本。
	LauncherName string
	// DefaultDirName 是默认安装目录的末级目录名（%LOCALAPPDATA%\Programs\<它>）。
	DefaultDirName string
	// ShortcutTarget 是快捷方式指向的文件名（相对安装目录）。
	ShortcutTarget string
	// UninstallKeyName 是 HKCU 卸载信息下的键名。
	UninstallKeyName string
	// UninstallDisplayName 是「程序和功能」中展示的名称。
	UninstallDisplayName string
	// MutexName 是单实例互斥量名，按版本区分以便两个版本可同时安装。
	MutexName string
	// KillProcesses 是卸载前需要结束的进程名（按顺序尽力结束）。
	KillProcesses []string
	// DataInInstallDir 为 true 表示用户数据位于安装目录下的 data/；
	// 为 false 表示位于 %LOCALAPPDATA%\<产品名>\data（安装目录之外）。
	DataInInstallDir bool
}

// 各版本的 profile 定义。
var (
	// profileV10 是 v1.0（浏览器 + 内置后端，启动脚本拉起）的配置。
	profileV10 = Profile{
		Name:                 "v1.0",
		Version:              "1.0.0",
		DisplayVersion:       "v1.0",
		AppExeName:           "ledger.exe",
		LauncherName:         "启动.bat",
		DefaultDirName:       "个人记账v1.0",
		ShortcutTarget:       "启动.bat",
		UninstallKeyName:     "个人记账v1.0",
		UninstallDisplayName: "个人记账 v1.0",
		MutexName:            `Local\LedgerInstallerV1`,
		KillProcesses:        []string{"ledger.exe"},
		DataInInstallDir:     true,
	}

	// profileV12 是 v1.2（桌面版独立窗口，含通用表格导入 / 延迟冻结 / 单总账户）的配置。
	//
	// 与 v1.1 的关键共同点：**数据目录仍共用** %LOCALAPPDATA%\个人记账\data ——
	// 用户从 v1.1 升级到 v1.2 时账本自动延续，因此 DataInInstallDir=false，
	// 卸载时默认**绝不**删除该目录（只有用户显式 --remove-data 才删）。
	// 与 v1.1 的差异仅在：安装目录名、卸载注册表键、安装器互斥量、主程序（同名）。
	profileV12 = Profile{
		Name:                 "v1.2",
		Version:              "1.2.0",
		DisplayVersion:       "v1.2",
		AppExeName:           "ledger-desktop.exe",
		LauncherName:         "",
		DefaultDirName:       "记账v1.2",
		ShortcutTarget:       "ledger-desktop.exe",
		UninstallKeyName:     "个人记账v1.2",
		UninstallDisplayName: "个人记账 v1.2",
		MutexName:            `Local\LedgerInstallerV12`,
		KillProcesses:        []string{"ledger-desktop.exe"},
		DataInInstallDir:     false,
	}

	// profileV11 是 v1.1（桌面版独立窗口）的配置。
	// 数据目录改为 %LOCALAPPDATA%\个人记账\data，因此升级/卸载都不会动到用户账本。
	profileV11 = Profile{
		Name:                 "v1.1",
		Version:              "1.1.0",
		DisplayVersion:       "v1.1",
		AppExeName:           "ledger-desktop.exe",
		LauncherName:         "",
		DefaultDirName:       "个人记账v1.1",
		ShortcutTarget:       "ledger-desktop.exe",
		UninstallKeyName:     "个人记账v1.1",
		UninstallDisplayName: "个人记账 v1.1",
		MutexName:            `Local\LedgerInstallerV11`,
		KillProcesses:        []string{"ledger-desktop.exe", "ledger.exe"},
		DataInInstallDir:     false,
	}
)

// resolveProfile 根据标识返回对应配置；未知标识回退到 v1.0 并保持可用。
func resolveProfile(name string) Profile {
	switch name {
	case "v1.2", "1.2":
		return profileV12
	case "v1.1", "1.1":
		return profileV11
	case "v1.0", "1.0", "":
		return profileV10
	default:
		return profileV10
	}
}

// prof 是当前二进制生效的 profile。
var prof = resolveProfile(profileName)

// 产品元信息：除版本差异外均由当前 profile 派生，供其它源文件直接引用。
var (
	// ProductName 是产品展示名（两个版本共用）。
	ProductName = "个人记账"
	// ProductVersion 是语义化版本号，与主程序 /api/health 返回一致。
	ProductVersion = prof.Version
	// DisplayVersion 是界面与快捷方式中使用的短版本号。
	DisplayVersion = prof.DisplayVersion
	// AppExeName 是主程序文件名。
	AppExeName = prof.AppExeName
	// LauncherName 是启动脚本文件名（v1.1 为空）。
	LauncherName = prof.LauncherName
	// UninstallExeName 是卸载程序文件名。
	UninstallExeName = "uninstall.exe"
	// ManualFileName 是随包分发的离线使用手册（HTML）；缺失时安装器跳过对应快捷方式。
	ManualFileName = "使用手册.html"
	// InstallInfoName 是安装信息文件名。
	InstallInfoName = "install.json"
	// DataDirName 是数据目录的末级目录名。
	DataDirName = "data"
	// Publisher 是卸载注册表项中的发布者。
	Publisher = "个人"
	// UninstallRegPath 是 HKCU 下的卸载信息注册表路径。
	UninstallRegPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + prof.UninstallKeyName
	// UninstallRegDisplayName 是「程序和功能」中展示的名称。
	UninstallRegDisplayName = prof.UninstallDisplayName
	// SingleInstanceMutexName 用于防止图形安装器被重复打开（按版本区分）。
	SingleInstanceMutexName = prof.MutexName
	// WindowTitle 是安装/卸载窗口与消息框的统一前缀（按版本区分）。
	WindowTitle = ProductName + " " + DisplayVersion
)

// localAppDataDir 返回当前用户的 %LOCALAPPDATA% 目录（缺失时逐级回退）。
func localAppDataDir() string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return base
	}
	if up := os.Getenv("USERPROFILE"); up != "" {
		return filepath.Join(up, "AppData", "Local")
	}
	return os.TempDir()
}

// DataDir 返回该 profile 下用户数据目录的绝对路径。
//
// v1.0 的数据目录在安装目录内（<installDir>\data）；
// v1.1 的数据目录固定在 %LOCALAPPDATA%\个人记账\data，与安装位置解耦，
// 这样卸载/重装/换盘都不会影响账本数据。
func (p Profile) DataDir(installDir string) string {
	if p.DataInInstallDir {
		if installDir == "" {
			return ""
		}
		return filepath.Join(installDir, DataDirName)
	}
	return filepath.Join(localAppDataDir(), ProductName, DataDirName)
}

// DataDirPath 返回当前 profile 下用户数据目录的绝对路径。
func DataDirPath(installDir string) string {
	return prof.DataDir(installDir)
}

// DataDirLabel 返回数据目录的展示文案（供界面与日志使用）。
func DataDirLabel(installDir string) string {
	if p := DataDirPath(installDir); p != "" {
		return p
	}
	return DataDirName
}
