package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// InstallOptions 描述一次安装请求。
type InstallOptions struct {
	// Dir 是用户指定的安装目录（可为空，表示使用默认目录）。
	Dir string
	// DesktopShortcut 表示是否创建桌面快捷方式。
	DesktopShortcut bool
	// StartMenuShortcut 表示是否创建开始菜单快捷方式。
	StartMenuShortcut bool
	// Launch 表示安装完成后是否立即启动。
	Launch bool
	// Progress 用于记录日志与进度。
	Progress *Progress
}

// Install 执行完整安装流程，返回最终安装目录。
func Install(opts InstallOptions) (string, error) {
	prog := opts.Progress
	if prog == nil {
		prog = NewProgress(false)
	}
	prog.Step(2, "开始安装 %s %s", ProductName, DisplayVersion)

	rawDir := opts.Dir
	if strings.TrimSpace(rawDir) == "" {
		rawDir = defaultInstallDir()
	}
	dir, err := ValidateInstallDir(rawDir)
	if err != nil {
		return "", err
	}
	prog.Step(5, "安装位置检查通过：%s", dir)

	meta := loadPayloadMeta()
	if meta.Files == 0 {
		return "", errors.New("安装包损坏：载荷为空或无法读取")
	}
	prog.Log(LevelInfo, "载荷 %s（%d 个文件，解压后约 %s）",
		humanSize(meta.CompressedSize), meta.Files, humanSize(meta.UncompressedSize))

	// 磁盘空间检查。
	free, err := DiskFreeBytes(dir)
	if err != nil {
		prog.Log(LevelWarn, "无法查询目标磁盘可用空间：%v", err)
	} else {
		prog.Log(LevelInfo, "目标磁盘可用空间：%s", humanSize(int64(free)))
		if int64(free) < meta.UncompressedSize {
			return "", fmt.Errorf("目标磁盘可用空间不足（需要 %s，可用 %s）",
				humanSize(meta.UncompressedSize), humanSize(int64(free)))
		}
	}

	// 升级检测。
	upgrade := false
	if info := ReadInstallInfo(dir); info != nil {
		upgrade = true
		prog.Log(LevelWarn, "检测到已安装 %s %s，将覆盖升级（保留数据目录 %s）",
			info.Product, info.Version, DataDirLabel(dir))
	} else if dirExists(dir) && !isDirEmpty(dir) {
		prog.Log(LevelWarn, "目标目录已存在且非空，将合并写入（不会删除已有文件，数据目录 %s 会被保留）",
			DataDirLabel(dir))
	}

	if err := ensureDir(dir); err != nil {
		return "", err
	}
	prog.Step(9, "已创建安装目录")

	// 覆盖前先腾出被占用的主程序，同时检测「程序正在运行」。
	if err := prepareForOverwrite(dir); err != nil {
		return "", err
	}

	prog.Step(10, "正在解压程序文件…")
	written, err := extractPayload(dir, prog)
	if err != nil {
		return "", fmt.Errorf("解压失败：%w", err)
	}
	prog.Step(70, "已解压 %d 个文件", written)

	// 清理升级时留下的 .old 备份。
	_ = os.Remove(joinPath(dir, AppExeName+".old"))

	// 写 install.json。
	info := InstallInfo{
		Product:       ProductName,
		Version:       ProductVersion,
		InstallDir:    dir,
		InstalledAt:   time.Now().Format(time.RFC3339),
		PayloadSHA256: meta.SHA256,
		Files:         meta.Files,
	}
	// v1.1 起用户数据目录移出安装目录，额外记录其绝对路径便于排障。
	// v1.0 保持为空（omitempty），install.json 内容与此前完全一致。
	if !prof.DataInInstallDir {
		info.DataDir = DataDirPath(dir)
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(joinPath(dir, InstallInfoName), data, 0o644); err != nil {
		return "", fmt.Errorf("写入 %s 失败：%w", InstallInfoName, err)
	}
	prog.Step(74, "已写入 %s", InstallInfoName)

	// 复制自身为 uninstall.exe。
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法定位安装程序自身路径：%w", err)
	}
	uninstaller := joinPath(dir, UninstallExeName)
	if err := copyFile(self, uninstaller); err != nil {
		return "", fmt.Errorf("生成卸载程序失败：%w", err)
	}
	prog.Step(80, "已生成卸载程序 %s", UninstallExeName)

	// 快捷方式。
	if err := createAllShortcuts(dir, opts.DesktopShortcut, opts.StartMenuShortcut, prog); err != nil {
		return "", err
	}
	prog.Step(88, "快捷方式处理完成")

	// 卸载注册表信息。
	if err := WriteUninstallEntry(dir, meta, prog); err != nil {
		return "", err
	}
	prog.Step(94, "注册表信息写入完成")

	// 立即启动。
	if opts.Launch {
		if err := launchInstalled(dir); err != nil {
			prog.Log(LevelWarn, "启动 %s 失败：%v", ProductName, err)
		} else {
			prog.Log(LevelInfo, "已启动 %s", ProductName)
		}
	} else {
		prog.Log(LevelInfo, "已跳过自动启动")
	}

	if upgrade {
		prog.Log(LevelOK, "升级完成：%s", dir)
	} else {
		prog.Log(LevelOK, "安装完成：%s", dir)
	}
	prog.SetPercent(100)
	return dir, nil
}

// prepareForOverwrite 通过重命名探测主程序是否被占用（正在运行）。
func prepareForOverwrite(dir string) error {
	exe := joinPath(dir, AppExeName)
	if !fileExists(exe) {
		return nil
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("%s 正在运行或被占用，请先关闭正在运行的个人记账后再安装", AppExeName)
	}
	return nil
}

// isDirEmpty 判断目录是否为空（不存在时视为空）。
func isDirEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	return len(entries) == 0
}

// launchInstalled 启动已安装的应用：有启动脚本时用脚本（v1.0），
// 否则直接运行主程序（v1.1 桌面版）。
func launchInstalled(dir string) error {
	var cmd *exec.Cmd
	if LauncherName != "" {
		if bat := joinPath(dir, LauncherName); fileExists(bat) {
			cmd = exec.Command("cmd", "/c", "start", "", LauncherName)
			cmd.Dir = dir
		}
	}
	if cmd == nil {
		cmd = exec.Command(joinPath(dir, AppExeName))
		cmd.Dir = dir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
	return cmd.Start()
}

// silentInstall 执行静默安装，按约定输出 INSTALL_OK / INSTALL_FAIL。
func silentInstall(opts cliOptions) int {
	prog := NewProgress(true)
	prog.Log(LevelInfo, "%s %s 静默安装开始", ProductName, ProductVersion)
	dir, err := Install(InstallOptions{
		Dir:               opts.dir,
		DesktopShortcut:   !opts.noShortcuts && !opts.noDesktop,
		StartMenuShortcut: !opts.noShortcuts && !opts.noStartMenu,
		Launch:            !opts.noLaunch,
		Progress:          prog,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "INSTALL_FAIL %v\n", err)
		return 1
	}
	prog.Finish(nil, dir)
	fmt.Printf("INSTALL_OK %s\n", dir)
	return 0
}

// resolveInstallDir 返回规范化的安装目录（用于界面展示）。
func resolveInstallDir(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return defaultInstallDir()
	}
	if dir, err := ValidateInstallDir(raw); err == nil {
		return dir
	}
	return filepath.Clean(raw)
}
