// Package main 实现「个人记账」的 Windows 安装/卸载程序。
//
// 同一个二进制文件承担四种角色：
//  1. 图形安装器（默认）：双击运行，WebView2 原生窗口，可自定义安装位置。
//  2. 图形卸载器：--uninstall。
//  3. 静默安装/卸载：--silent（供自动化验证与批量部署使用）。
//  4. 自清理进程：--self-clean（安装目录内的 uninstall.exe 无法删除自身，
//     由复制到 %TEMP% 的副本代为删除）。
//
// 该程序只写 HKCU 注册表、不使用管理员权限，因此任何普通账户均可完成安装。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// 产品元信息统一由 profile.go 按编译目标版本派生（ProductName、ProductVersion、
// DisplayVersion、AppExeName、UninstallRegPath 等），本文件只消费这些变量。

// cliOptions 汇总所有命令行参数，便于在 main 与各子流程间传递。
type cliOptions struct {
	uninstall   bool
	silent      bool
	dir         string
	noShortcuts bool
	noLaunch    bool
	noDesktop   bool
	noStartMenu bool
	removeData  bool
	selfClean   bool
	target      string
	showVersion bool
}

// main 是程序入口：锁定主线程（WebView2 要求窗口与消息循环同线程）后分发模式。
func main() {
	runtime.LockOSThread()
	os.Exit(run())
}

// run 解析参数并分发到具体模式，返回进程退出码。
func run() int {
	opts := parseFlags()

	// 同名二进制的兜底：以 uninstall.exe 身份启动时默认进入卸载流程。
	// 这样用户直接双击安装目录里的 uninstall.exe（不带参数），
	// 以及旧版本创建的、漏传 --uninstall 的卸载快捷方式，都不会误入安装界面。
	if self, err := os.Executable(); err == nil {
		applyUninstallAutoDetect(self, &opts)
	}

	if opts.showVersion {
		fmt.Printf("%s %s\n", ProductName, ProductVersion)
		return 0
	}

	// 自清理进程：等待原进程退出后删除其可执行文件，然后删除自身。
	if opts.selfClean {
		if err := runSelfClean(opts.target, opts.dir, opts.removeData); err != nil {
			fmt.Fprintf(os.Stderr, "SELF_CLEAN_FAIL %v\n", err)
			return 1
		}
		fmt.Println("SELF_CLEAN_OK")
		return 0
	}

	if opts.uninstall {
		if opts.silent {
			return silentUninstall(opts)
		}
		return runUninstallGUI(opts)
	}

	if opts.silent {
		return silentInstall(opts)
	}
	return runInstallGUI(opts)
}

// applyUninstallAutoDetect 在未显式指定 --uninstall 时，按可执行文件名推断卸载模式。
//
// 安装包与卸载程序是同一个二进制，仅靠参数区分角色。若只依赖参数，任何漏传参数的
// 启动方式（直接双击 uninstall.exe、旧的卸载快捷方式）都会误入安装界面，
// 这也是 QA 报的 P1 缺陷。这里按文件名兜底：
//   - <安装目录>\uninstall.exe        -> 卸载
//   - 记账v1.1-安装包.exe             -> 安装（文件名匹配不上，行为不变）
func applyUninstallAutoDetect(exePath string, opts *cliOptions) {
	if opts == nil || opts.uninstall || opts.selfClean {
		return
	}
	base := strings.ToLower(filepath.Base(exePath))
	base = strings.TrimSuffix(base, ".exe")
	if base == strings.TrimSuffix(strings.ToLower(UninstallExeName), ".exe") {
		opts.uninstall = true
	}
}

// parseFlags 定义并解析命令行参数。Go 的 flag 包同时接受 -x 与 --x 两种写法。
func parseFlags() cliOptions {
	var opts cliOptions
	flag.BoolVar(&opts.uninstall, "uninstall", false, "卸载模式（默认安装）")
	flag.BoolVar(&opts.silent, "silent", false, "静默模式，不显示图形界面")
	flag.StringVar(&opts.dir, "dir", "", "安装/卸载目标目录（绝对路径）")
	flag.BoolVar(&opts.noShortcuts, "no-shortcuts", false, "不创建任何快捷方式")
	flag.BoolVar(&opts.noLaunch, "no-launch", false, "安装完成后不自动启动")
	flag.BoolVar(&opts.noDesktop, "no-desktop", false, "不创建桌面快捷方式")
	flag.BoolVar(&opts.noStartMenu, "no-startmenu", false, "不创建开始菜单快捷方式")
	flag.BoolVar(&opts.removeData, "remove-data", false, "卸载的同时删除数据目录 data/")
	flag.BoolVar(&opts.selfClean, "self-clean", false, "内部使用：自删除代理进程")
	flag.StringVar(&opts.target, "target", "", "内部使用：自删除代理要删除的文件")
	flag.BoolVar(&opts.showVersion, "version", false, "打印版本号后退出")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s %s 安装程序\n\n", ProductName, ProductVersion)
		fmt.Fprintf(os.Stderr, "用法：\n  setup.exe                                 图形安装\n")
		fmt.Fprintf(os.Stderr, "      setup.exe --silent --dir=\"D:\\Apps\\%s\"\n", prof.DefaultDirName)
		fmt.Fprintf(os.Stderr, "      uninstall.exe --uninstall [--silent] [--remove-data]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	return opts
}

// safeJoin 把 zip 内的相对路径安全地拼接到目标目录，并阻止目录穿越。
func safeJoin(base, rel string) (string, error) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	cleaned := filepath.Clean(filepath.FromSlash(rel))
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return base, nil
	}
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("非法的压缩包路径：%s", rel)
	}
	target := filepath.Join(base, cleaned)
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if targetAbs != baseAbs && !strings.HasPrefix(targetAbs, baseAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("压缩包路径越界：%s", rel)
	}
	return targetAbs, nil
}
