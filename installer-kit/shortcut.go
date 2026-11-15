package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

// encodePSCommand 把 PowerShell 脚本编码为 -EncodedCommand 需要的 Base64(UTF-16LE)。
// 这样可以彻底规避中文路径与引号在命令行上的转义问题。
func encodePSCommand(script string) string {
	codes := utf16.Encode([]rune(script))
	buf := make([]byte, 0, len(codes)*2)
	for _, c := range codes {
		buf = append(buf, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// psQuote 把字符串转成 PowerShell 单引号字面量。
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// runPowerShell 以隐藏窗口方式执行 PowerShell 脚本并返回标准输出。
//
// 关于「非 0 退出」的处理：Windows 首次调用 PowerShell 会加载模块并向 stderr 打 CLIXML
// （"正在准备首次使用模块"），此时可能以 exit status 1 结束，但 stdout 里的脚本输出
// 已经完整可用。旧实现一律判为失败，导致上层「静默跳过」清理动作（team-lead 复现的
// 第 1 次卸载残留快捷方式/注册表就是这个原因）。
// 现在：只要 stdout 非空就返回成功，由调用方按输出标记（LNK_OK / KILL / PATH=）判断。
func runPowerShell(script string) (string, error) {
	cmd := exec.Command("powershell",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodePSCommand(script),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		out := stdout.String()
		if strings.TrimSpace(out) != "" {
			// 冷启动的 CLIXML/module 警告：输出有效，按成功处理。
			return out, nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(out)
		}
		return out, fmt.Errorf("PowerShell 执行失败：%v %s", err, msg)
	}
	return stdout.String(), nil
}

// runHiddenCommand 以隐藏窗口方式执行外部命令并返回合并输出。
func runHiddenCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildShortcutScript 生成创建快捷方式的 PowerShell 脚本。
// 独立成函数便于单元测试对脚本做语法校验。
// arguments 非空时写入 $sc.Arguments（卸载快捷方式必须带 --uninstall，
// 否则双击后 uninstall.exe 会按「安装」模式启动，弹出安装界面）。
func buildShortcutScript(lnkPath, target, workingDir, iconLocation, description, arguments string) string {
	lines := []string{
		`$ErrorActionPreference = "Stop"`,
		`$ws = New-Object -ComObject WScript.Shell`,
		`$sc = $ws.CreateShortcut(` + psQuote(lnkPath) + `)`,
		`$sc.TargetPath = ` + psQuote(target),
		`$sc.WorkingDirectory = ` + psQuote(workingDir),
		`$sc.IconLocation = ` + psQuote(iconLocation),
		`$sc.Description = ` + psQuote(description),
		`$sc.WindowStyle = 1`,
	}
	if arguments != "" {
		lines = append(lines, `$sc.Arguments = `+psQuote(arguments))
	}
	lines = append(lines, `$sc.Save()`, `Write-Output "LNK_OK"`)
	return strings.Join(lines, "\r\n")
}

// createShortcut 通过 WScript.Shell COM 创建 .lnk 快捷方式。
func createShortcut(lnkPath, target, workingDir, iconLocation, description, arguments string) error {
	if lnkPath == "" {
		return errors.New("快捷方式路径为空")
	}
	if err := ensureDir(dirOfPath(lnkPath)); err != nil {
		return err
	}
	script := buildShortcutScript(lnkPath, target, workingDir, iconLocation, description, arguments)
	out, err := runPowerShell(script)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "LNK_OK") {
		return fmt.Errorf("创建快捷方式失败：%s", lnkPath)
	}
	return nil
}

// createAllShortcuts 按选项创建桌面与开始菜单快捷方式。
// 快捷方式目标由 profile 决定：v1.0 指向 启动.bat，v1.1 指向 ledger-desktop.exe。
func createAllShortcuts(installDir string, desktop, startMenu bool, prog *Progress) error {
	target := joinPath(installDir, prof.ShortcutTarget)
	if !fileExists(target) {
		// 目标文件缺失时退化为直接指向主程序，保证快捷方式始终可用。
		target = joinPath(installDir, AppExeName)
	}
	icon := joinPath(installDir, AppExeName) + ",0"
	desc := ProductName + " " + DisplayVersion

	if desktop {
		lnk := desktopShortcutPath()
		if err := createShortcut(lnk, target, installDir, icon, desc, ""); err != nil {
			return fmt.Errorf("创建桌面快捷方式失败：%w", err)
		}
		prog.Log(LevelInfo, "已创建桌面快捷方式：%s", lnk)
	} else {
		prog.Log(LevelInfo, "已跳过桌面快捷方式")
	}

	if startMenu {
		folder := startMenuFolder()
		if folder == "" {
			return errors.New("无法定位开始菜单目录")
		}
		if err := ensureDir(folder); err != nil {
			return err
		}
		mainLnk := startMenuShortcutPath()
		if err := createShortcut(mainLnk, target, installDir, icon, desc, ""); err != nil {
			return fmt.Errorf("创建开始菜单快捷方式失败：%w", err)
		}
		// 卸载快捷方式必须显式带 --uninstall：uninstall.exe 与安装包是同一个二进制，
		// 不带参数会进入安装界面（QA 发现的 P1 缺陷）。
		unLnk := startMenuUninstallShortcutPath()
		if err := createShortcut(unLnk, joinPath(installDir, UninstallExeName),
			installDir, icon, "卸载 "+desc, "--uninstall"); err != nil {
			return fmt.Errorf("创建卸载快捷方式失败：%w", err)
		}
		// 使用手册（v1.2 起随包分发的离线 HTML）；文件缺失时跳过，不影响安装。
		if manual := joinPath(installDir, ManualFileName); fileExists(manual) {
			manualLnk := joinPath(folder, "使用手册.lnk")
			if err := createShortcut(manualLnk, manual, installDir, icon, desc+" 使用手册", ""); err != nil {
				prog.Log(LevelWarn, "创建使用手册快捷方式失败（跳过）：%v", err)
			} else {
				prog.Log(LevelInfo, "已创建使用手册快捷方式：%s", manualLnk)
			}
		}
		prog.Log(LevelInfo, "已创建开始菜单快捷方式：%s", folder)
	} else {
		prog.Log(LevelInfo, "已跳过开始菜单快捷方式")
	}
	return nil
}

// removeAllShortcuts 删除桌面快捷方式与开始菜单文件夹。
//
// 全程使用 Go 原生 os.Remove / os.RemoveAll，**不依赖 PowerShell**：Windows 首次
// 调用 PowerShell 时会加载模块并向 stderr 打 CLIXML（"正在准备首次使用模块"）并
// 以非 0 退出，旧实现据此判为失败后只记日志就继续，导致快捷方式被静默跳过（team-lead
// 实测的第 1 次卸载残留）。原生删除没有这个"冷启动"问题。
//
// 安全约束：只删除位于 Desktop / Start Menu\Programs 之下的路径，避免配置异常时误删。
func removeAllShortcuts(prog *Progress) error {
	var firstErr error

	if lnk := desktopShortcutPath(); lnk != "" && fileExists(lnk) {
		if !underDir(lnk, desktopDir()) {
			prog.Log(LevelWarn, "桌面快捷方式路径异常，跳过删除：%s", lnk)
		} else {
			if err := removeVerified(lnk); err != nil {
				prog.Log(LevelWarn, "删除桌面快捷方式失败：%v", err)
				if firstErr == nil {
					firstErr = err
				}
			} else {
				prog.Log(LevelInfo, "已删除桌面快捷方式：%s", lnk)
			}
		}
	}

	if folder := startMenuFolder(); folder != "" && dirExists(folder) {
		if !underDir(folder, startMenuProgramsDir()) {
			prog.Log(LevelWarn, "开始菜单文件夹路径异常，跳过删除：%s", folder)
		} else {
			if err := removeVerified(folder); err != nil {
				prog.Log(LevelWarn, "删除开始菜单文件夹失败：%v", err)
				if firstErr == nil {
					firstErr = err
				}
			} else {
				prog.Log(LevelInfo, "已删除开始菜单文件夹：%s", folder)
			}
		}
	}
	return firstErr
}

// removeVerified 删除文件/目录并**校验确实消失**，失败时短暂重试一次。
// 「删除调用返回 nil 但对象仍在」在实际环境中出现过（句柄未释放），校验比信任返回值可靠。
func removeVerified(path string) error {
	if err := removePath(path); err != nil {
		return err
	}
	if !fileExists(path) {
		return nil
	}
	time.Sleep(300 * time.Millisecond)
	if err := removePath(path); err != nil {
		return err
	}
	if fileExists(path) {
		return fmt.Errorf("%s 仍存在（可能被占用）", path)
	}
	return nil
}

// underDir 判断 path 是否位于 dir 之下（大小写不敏感，含自身）。
func underDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	p := strings.ToLower(filepath.Clean(path))
	d := strings.ToLower(strings.TrimRight(filepath.Clean(dir), `\/`))
	if d == "" {
		return false
	}
	return p == d || strings.HasPrefix(p, d+`\`)
}

// buildFolderDialogScript 生成「选择文件夹」对话框的 PowerShell 脚本。
func buildFolderDialogScript(initial string) string {
	return strings.Join([]string{
		`$ErrorActionPreference = "Stop"`,
		`Add-Type -AssemblyName System.Windows.Forms`,
		`Add-Type -AssemblyName System.Drawing`,
		`$owner = New-Object System.Windows.Forms.Form`,
		`$owner.Text = ` + psQuote("选择安装位置"),
		`$owner.TopMost = $true`,
		`$owner.ShowInTaskbar = $false`,
		`$owner.FormBorderStyle = "None"`,
		`$owner.StartPosition = "CenterScreen"`,
		`$owner.Size = New-Object System.Drawing.Size(1, 1)`,
		`$owner.Opacity = 0`,
		`$owner.Show()`,
		`$dlg = New-Object System.Windows.Forms.FolderBrowserDialog`,
		`$dlg.Description = ` + psQuote(ProductName+" "+DisplayVersion+" 的安装位置"),
		`$dlg.ShowNewFolderButton = $true`,
		`if ($true) { $dlg.SelectedPath = ` + psQuote(initial) + ` }`,
		`$res = $dlg.ShowDialog($owner)`,
		`if ($res -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write("PATH=" + $dlg.SelectedPath) }`,
		`$owner.Close()`,
	}, "\r\n")
}

// chooseFolder 调用系统「选择文件夹」对话框并返回用户选择。
// 用户取消时返回空字符串与 nil 错误。
func chooseFolder(initial string) (string, error) {
	out, err := runPowerShell(buildFolderDialogScript(initial))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "PATH=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "PATH=")), nil
		}
	}
	return "", nil
}
