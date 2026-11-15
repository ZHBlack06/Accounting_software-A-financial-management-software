package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Uninstall 执行卸载流程。
//
// 删除顺序刻意设计为「先清外设（快捷方式/注册表），再清文件，最后自删除」，
// 保证任何一步失败都不会留下仍在注册表中但已不存在的应用。
func Uninstall(dir string, removeData bool, prog *Progress) error {
	if prog == nil {
		prog = NewProgress(false)
	}
	cleaned := strings.TrimSpace(dir)
	if cleaned == "" {
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("无法定位卸载程序自身路径：%w", err)
		}
		cleaned = filepath.Dir(self)
	}
	cleaned = filepath.Clean(cleaned)
	if !isAbsWindowsPath(cleaned) || isDriveRoot(cleaned) {
		return fmt.Errorf("非法的卸载目录：%s", cleaned)
	}
	prog.Step(3, "开始卸载 %s %s", ProductName, DisplayVersion)
	prog.Log(LevelInfo, "安装目录：%s", cleaned)
	// 先离开安装目录：Windows 不允许删除仍被进程作为当前目录占用的文件夹。
	leaveInstallDir(cleaned, prog)
	if removeData {
		prog.Log(LevelWarn, "已选择同时删除用户数据目录 %s", DataDirLabel(cleaned))
	} else {
		prog.Log(LevelInfo, "将保留用户数据目录 %s", DataDirLabel(cleaned))
	}

	// 1. 结束正在运行的应用（仅限本安装目录内的进程）。
	killRunningApp(cleaned, prog)

	// 2. 快捷方式（Go 原生删除 + 校验，不依赖 PowerShell）。
	prog.Step(25, "清理快捷方式")
	if err := removeAllShortcuts(prog); err != nil {
		prog.Log(LevelWarn, "快捷方式未能全部清理：%v", err)
	}

	// 3. 注册表（Go 原生 registry.DeleteKey + 校验）。
	prog.Step(40, "清理卸载注册表信息")
	if err := RemoveUninstallEntry(prog); err != nil {
		prog.Log(LevelWarn, "%v", err)
	}

	// 4. 安装目录内的文件（保留 data/ 与自身）。
	prog.Step(55, "删除程序文件")
	if !dirExists(cleaned) {
		prog.Log(LevelWarn, "安装目录不存在，跳过文件删除：%s", cleaned)
	} else {
		if err := removeInstalledFiles(cleaned, removeData, prog); err != nil {
			return err
		}
	}

	// 4.5 安装目录之外的用户数据目录（v1.1 起）。
	// 必须在自删除代理启动前处理：该目录不在安装目录内，与运行中的
	// uninstall.exe 无占用冲突，由主进程直接删除最可靠。
	if !prof.DataInInstallDir && removeData {
		removeUserDataDir(cleaned, prog)
	}

	prog.Step(85, "文件清理完成")

	// 若安装目录已彻底清空（例如只保留了 data 且 data 不存在），顺手删除空目录。
	maybeRemoveEmptyDir(cleaned, prog)

	// 5. 自删除：若当前运行的就是安装目录内的 uninstall.exe，则交给 %TEMP% 里的小代理处理。
	self, err := os.Executable()
	if err != nil {
		prog.Log(LevelWarn, "无法定位自身路径，跳过自删除：%v", err)
		return nil
	}
	selfDir := filepath.Clean(filepath.Dir(self))
	inside := strings.EqualFold(selfDir, cleaned)
	if inside {
		if err := spawnCleanupAgent(cleaned, removeData); err != nil {
			prog.Log(LevelWarn, "自删除代理启动失败（可能残留 %s）：%v", UninstallExeName, err)
			return nil
		}
		prog.Log(LevelInfo, "已启动清理代理（%%TEMP%%\\jz-clean-*.cmd，<1KB），本进程退出后删除 %s", UninstallExeName)
	} else {
		// 从外部（例如安装包本体）触发卸载时，直接删除目录内的 uninstall.exe。
		uninstaller := joinPath(cleaned, UninstallExeName)
		if fileExists(uninstaller) {
			if err := os.Remove(uninstaller); err != nil {
				prog.Log(LevelWarn, "删除 %s 失败：%v", UninstallExeName, err)
			}
		}
		prog.Log(LevelInfo, "当前并非从安装目录运行，跳过自删除")
		// uninstall.exe 删除后目录可能才真正变空，这里需要再判定一次。
		maybeRemoveEmptyDir(cleaned, prog)
	}

	prog.SetPercent(100)
	prog.Log(LevelOK, "卸载完成")
	return nil
}

// removeInstalledFiles 删除安装目录内除数据目录外的所有内容。
// 数据目录只在「数据位于安装目录内」（v1.0）时才需要在此特殊保护。
func removeInstalledFiles(dir string, removeData bool, prog *Progress) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读取安装目录失败：%w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if prof.DataInInstallDir && !removeData && strings.EqualFold(name, DataDirName) {
			prog.Log(LevelInfo, "保留数据目录：%s", joinPath(dir, name))
			continue
		}
		if strings.EqualFold(name, UninstallExeName) {
			// 自身文件由自删除代理处理。
			continue
		}
		target := joinPath(dir, name)
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("删除 %s 失败：%w", target, err)
		}
	}
	prog.Log(LevelInfo, "已删除安装目录内的程序文件")
	return nil
}

// removeUserDataDir 删除安装目录之外的用户数据目录（仅 v1.1 及以后）。
// 删除时连父目录一并清理：%LOCALAPPDATA%\个人记账 会在 data 删除后变空，
// 顺手移除可以避免留下空壳目录。
func removeUserDataDir(installDir string, prog *Progress) {
	dataDir := DataDirPath(installDir)
	if dataDir == "" {
		return
	}
	if !dirExists(dataDir) {
		prog.Log(LevelInfo, "用户数据目录不存在，跳过：%s", dataDir)
		return
	}
	if err := os.RemoveAll(dataDir); err != nil {
		prog.Log(LevelWarn, "删除用户数据目录失败：%s（%v）", dataDir, err)
		return
	}
	parent := filepath.Dir(dataDir)
	if parent != "" && dirExists(parent) && isDirEmpty(parent) {
		_ = os.Remove(parent)
	}
	prog.Log(LevelInfo, "已删除用户数据目录：%s", dataDir)
}

// killRunningApp 结束「可执行文件位于本产品安装目录内」的正在运行进程。
//
// 不能按进程名无差别结束：v1.0 与 v1.1 的进程名可能同时存在（v1.1 profile 会尽力
// 结束 ledger.exe），无差别 taskkill 会把用户正开着的另一版本一起杀掉（QA 报的 P2）。
// 因此只按路径前缀匹配本安装目录，且 $_.Path 取不到时跳过（绝不 fallback 到全杀）。
func killRunningApp(installDir string, prog *Progress) {
	if strings.TrimSpace(installDir) == "" {
		prog.Log(LevelInfo, "安装目录未知，跳过进程检查")
		return
	}
	script := buildKillScript(installDir, prof.KillProcesses)
	if script == "" {
		return
	}
	prog.Step(10, "检查正在运行的进程（仅限本安装目录）")
	out, err := runPowerShell(script)
	if err != nil {
		prog.Log(LevelWarn, "PowerShell 进程检查失败（改用原生兜底）：%v", err)
		fallbackKillByPath(installDir, prog)
		return
	}
	killed, skipped := 0, 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "KILL "):
			killed++
			prog.Log(LevelInfo, "已结束进程（位于安装目录内）：%s", strings.TrimPrefix(line, "KILL "))
		case strings.HasPrefix(line, "OTHER "):
			prog.Log(LevelInfo, "跳过不属于本安装目录的同名进程：%s", strings.TrimPrefix(line, "OTHER "))
		case strings.HasPrefix(line, "SKIP "):
			skipped++
			prog.Log(LevelInfo, "跳过无法读取映像路径的进程：%s", strings.TrimPrefix(line, "SKIP "))
		}
	}
	if killed == 0 && skipped == 0 {
		prog.Log(LevelInfo, "未检测到本安装目录下正在运行的程序")
	}
	if killed > 0 {
		time.Sleep(700 * time.Millisecond)
	}
}

// fallbackKillByPath 是 PowerShell 不可用时的原生兜底：用 wmic 查询可执行文件路径
// 位于本安装目录内的进程再按 PID 结束。
//
// 安全约束：严格按「安装目录前缀 + 进程名白名单」双重过滤；wmic 不存在或解析失败时
// 只记 Warn，**绝不退回按镜像名全杀**（那会杀掉另一版本正在运行的实例）。
func fallbackKillByPath(installDir string, prog *Progress) {
	prefix := strings.TrimRight(installDir, `\/`) + `\`
	where := fmt.Sprintf("ExecutablePath like '%s%%'", strings.ReplaceAll(prefix, "'", "''"))
	out, err := runHiddenCommand("wmic", "process", "where", where, "get", "Name,ProcessId", "/format:csv")
	if err != nil {
		prog.Log(LevelWarn, "wmic 兜底不可用（跳过结束进程，不会无差别杀进程）：%v", err)
		return
	}
	wanted := make(map[string]bool, len(prof.KillProcesses))
	for _, n := range prof.KillProcesses {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			wanted[strings.TrimSuffix(n, ".exe")] = true
		}
	}
	killed := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "Node,") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(fields[1]))
		pid := strings.TrimSpace(fields[2])
		if pid == "" || !wanted[strings.TrimSuffix(name, ".exe")] {
			continue
		}
		if _, err := runHiddenCommand("taskkill", "/PID", pid, "/F"); err != nil {
			prog.Log(LevelWarn, "结束进程 %s(PID %s) 失败：%v", name, pid, err)
			continue
		}
		killed++
		prog.Log(LevelInfo, "已结束进程（原生兜底，位于安装目录内）：%s PID %s", name, pid)
	}
	if killed == 0 {
		prog.Log(LevelInfo, "原生兜底未发现本安装目录下正在运行的程序")
	} else {
		time.Sleep(700 * time.Millisecond)
	}
}

// buildKillScript 生成「按安装目录前缀匹配再结束进程」的 PowerShell 脚本。
// 独立成函数便于单元测试做语法校验。
func buildKillScript(installDir string, names []string) string {
	prefix := strings.ToLower(strings.TrimRight(installDir, `\/`) + `\`)
	var list []string
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.TrimSpace(strings.ToLower(n))
		n = strings.TrimSuffix(n, ".exe")
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		list = append(list, psQuote(n))
	}
	if len(list) == 0 {
		return ""
	}
	return strings.Join([]string{
		`$ErrorActionPreference = "SilentlyContinue"`,
		`$dirPrefix = ` + psQuote(prefix),
		`$names = @(` + strings.Join(list, ", ") + `)`,
		`foreach ($n in $names) {`,
		`  foreach ($p in @(Get-Process -Name $n -ErrorAction SilentlyContinue)) {`,
		`    $path = $null`,
		`    try { $path = $p.Path } catch { $path = $null }`,
		`    if ([string]::IsNullOrEmpty($path)) { Write-Output ("SKIP " + $p.Id + " " + $n); continue }`,
		`    if ($path.ToLower().StartsWith($dirPrefix)) {`,
		`      Write-Output ("KILL " + $p.Id + " " + $path)`,
		`      Stop-Process -Id $p.Id -Force`,
		`    } else {`,
		`      Write-Output ("OTHER " + $p.Id + " " + $path)`,
		`    }`,
		`  }`,
		`}`,
	}, "\r\n")
}

// cleanupAgentScript 是写入 %TEMP% 的清理代理（约 0.4 KB，纯 ASCII）。
//
// 设计要点（针对 QA 报的 P2：TEMP 残留 15–22 MB 副本）：
//   - **不再复制安装包本体**：旧实现把整份 exe 复制到 %TEMP% 再由副本自删除，
//     一旦删除失败就会永久残留一个 15–22 MB 的可执行文件。
//   - 路径通过命令行参数（%1/%2/%3）传入，而不是写进脚本：cmd.exe 按 OEM 代码页
//     解析 .cmd 文件，直接写入 UTF-8 中文路径会乱码；而参数经 CreateProcess 以
//     Unicode 传递，中文路径安全。
//   - 每轮等待约 1 秒、最多 60 轮，删成功立即跳到清理步骤。
//   - 脚本最后删除自身，因此 %TEMP% 不会残留 .cmd。
const cleanupAgentScript = "@echo off\r\n" +
	"setlocal\r\n" +
	"set \"EXE=%~1\"\r\n" +
	"set \"DIR=%~2\"\r\n" +
	"set \"MODE=%~3\"\r\n" +
	"for /L %%i in (1,1,60) do (\r\n" +
	"  if not exist \"%EXE%\" goto cleanup\r\n" +
	"  del /f /q \"%EXE%\" >nul 2>&1\r\n" +
	"  ping -n 2 127.0.0.1 >nul\r\n" +
	")\r\n" +
	":cleanup\r\n" +
	"if /i \"%MODE%\"==\"purge\" (rmdir /s /q \"%DIR%\" >nul 2>&1) else (rd \"%DIR%\" >nul 2>&1)\r\n" +
	"del /f /q \"%~f0\" >nul 2>&1\r\n"

// buildCleanupCmdLine 生成启动清理代理的命令行。
//
// 必须用 `cmd /c call "<脚本>" "<参数>"` 这种写法：实测 `cmd /c start /b "" "<脚本>"`
// 在 DETACHED_PROCESS + 隐藏窗口下会静默失败（脚本根本不执行、且残留 .cmd），
// 而 `call` 形式可正常执行并让脚本自删除。
func buildCleanupCmdLine(comspec, cmdPath, target, installDir, mode string) string {
	return fmt.Sprintf(`"%s" /c call "%s" "%s" "%s" %s`, comspec, cmdPath, target, installDir, mode)
}

// spawnCleanupAgent 写出并启动 %TEMP%\jz-clean-<pid>.cmd 清理代理。
//
// 代理进程在卸载程序退出后删除 <安装目录>\uninstall.exe 并清理安装目录：
// removeData 为真时整目录递归删除，否则仅在目录已空时删除目录（v1.0 保留 data\）。
func spawnCleanupAgent(installDir string, removeData bool) error {
	target := joinPath(installDir, UninstallExeName)
	cmdPath := filepath.Join(os.TempDir(), fmt.Sprintf("jz-clean-%d.cmd", os.Getpid()))
	if err := os.WriteFile(cmdPath, []byte(cleanupAgentScript), 0o644); err != nil {
		return err
	}
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	mode := "keep"
	if removeData {
		mode = "purge"
	}
	// cmd.Dir 设为 %TEMP%：避免继承来的当前目录（可能正要被删除）导致 cmd.exe 启动失败。
	rawCmdLine := buildCleanupCmdLine(comspec, cmdPath, target, installDir, mode)

	agent := exec.Command(comspec)
	agent.Dir = os.TempDir()
	agent.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CmdLine:       rawCmdLine,
		CreationFlags: 0x00000008, // DETACHED_PROCESS
	}
	if err := agent.Start(); err != nil {
		// 代理都没起来时兜底：登记重启删除，避免 uninstall.exe 永久残留。
		scheduleDeleteOnReboot(target)
		return err
	}
	return nil
}

// scheduleDeleteOnReboot 把文件登记为「重启时删除」（MOVEFILE_DELAY_UNTIL_REBOOT）。
//
// 仅作为代理启动失败时的兜底：该 API 需要写 HKLM\...\PendingFileRenameOperations，
// 普通账户会失败，因此忽略错误——非管理员场景下代理本身已能完成删除。
func scheduleDeleteOnReboot(path string) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	_ = windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}

// runSelfClean 是自删除代理进程的主体逻辑。
func runSelfClean(target, installDir string, removeData bool) error {
	if target == "" {
		return errors.New("缺少自删除目标文件")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := os.Remove(target)
		if err == nil {
			break
		}
		if os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待原进程退出超时：%w", err)
		}
		time.Sleep(300 * time.Millisecond)
	}

	if installDir != "" {
		cleaned := filepath.Clean(installDir)
		// 代理进程会继承父进程的工作目录，同样需要先离开安装目录才能删除它。
		leaveInstallDirSilent(cleaned)
		if isAbsWindowsPath(cleaned) && !isDriveRoot(cleaned) && dirExists(cleaned) {
			if removeData {
				_ = os.RemoveAll(cleaned)
				removeDirWithRetry(cleaned, nil)
			} else {
				entries, err := os.ReadDir(cleaned)
				if err == nil {
					remaining := 0
					for _, e := range entries {
						if strings.EqualFold(e.Name(), DataDirName) {
							remaining++
							continue
						}
						if err := os.RemoveAll(joinPath(cleaned, e.Name())); err != nil {
							remaining++
						}
					}
					if remaining == 0 {
						removeDirWithRetry(cleaned, nil)
					}
				}
			}
		}
	}

	deleteSelfLater()
	return nil
}

// deleteSelfLater 用 cmd + 延时循环删除正在运行的自身副本（%TEMP% 里的 ledger-uninstaller-*.exe）。
//
// 三个必须注意的细节：
//  1. 使用 SysProcAttr.CmdLine 手动拼接命令行：Go 默认会把整条含空格与引号的
//     参数转义后交给 cmd.exe，cmd 对 \" 的解析会破坏 del 的目标路径。
//  2. 必须把 cmd 的工作目录设为 %TEMP%：若继承来的当前目录已被删除或不可访问，
//     cmd.exe 会直接启动失败，del 便不会执行，副本就会永久残留在 TEMP 中。
//  3. 重试窗口要足够长：杀毒/索引服务可能在进程退出后短暂持有句柄。旧实现只重试
//     3 次（约 5 秒），QA 实测 18 次卸载中有 5 次在 15 秒后仍残留；现改为每约 1 秒
//     重试一次、最多 40 次（约 40 秒），删除成功后立即 exit 0 提前结束。
func deleteSelfLater() {
	self, err := os.Executable()
	if err != nil || self == "" {
		return
	}
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	// for /L %i 在 cmd /c（非批处理）语境下使用单百分号；多百分号会变成字面量。
	rawCmdLine := fmt.Sprintf(
		`"%s" /c for /L %%i in (1,1,40) do @(if not exist "%s" exit 0 & ping -n 2 127.0.0.1 >nul & del /f /q "%s" >nul 2>&1)`,
		comspec, self, self,
	)

	detached := exec.Command(comspec)
	detached.Dir = os.TempDir()
	detached.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CmdLine:       rawCmdLine,
		CreationFlags: 0x00000008, // DETACHED_PROCESS：父进程退出后继续执行
	}
	if err := detached.Start(); err == nil {
		return
	}

	// 兜底：个别环境下 DETACHED_PROCESS 会启动失败，退化为普通隐藏窗口再试一次，
	// 保证「至少有一次删除尝试」，避免副本永久残留。
	fallback := exec.Command(comspec)
	fallback.Dir = os.TempDir()
	fallback.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		CmdLine:    rawCmdLine,
	}
	_ = fallback.Start()
}

// leaveInstallDir 记录日志地把工作目录从安装目录移开。
func leaveInstallDir(dir string, prog *Progress) {
	if !cwdInside(dir) {
		return
	}
	tmp := os.TempDir()
	if err := os.Chdir(tmp); err != nil {
		prog.Log(LevelWarn, "无法切换工作目录（%v），安装目录可能因被占用而无法删除", err)
		return
	}
	prog.Log(LevelInfo, "已将工作目录切换到 %s，避免安装目录被占用", tmp)
}

// leaveInstallDirSilent 同 leaveInstallDir，但不记录日志（自删除代理使用）。
func leaveInstallDirSilent(dir string) {
	if !cwdInside(dir) {
		return
	}
	_ = os.Chdir(os.TempDir())
}

// cwdInside 判断当前工作目录是否就是 dir 或位于 dir 之下。
func cwdInside(dir string) bool {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return false
	}
	wd = filepath.Clean(wd)
	target := filepath.Clean(dir)
	if strings.EqualFold(wd, target) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(wd), strings.ToLower(target)+`\`)
}

// maybeRemoveEmptyDir 在目录已空时删除它。
func maybeRemoveEmptyDir(dir string, prog *Progress) {
	if !dirExists(dir) || !isDirEmpty(dir) {
		return
	}
	removeDirWithRetry(dir, prog)
}

// removeDirWithRetry 反复尝试删除目录，最终失败时给出明确的 warn 而不是静默忽略。
// prog 为 nil 时不输出日志（自删除代理在窗口关闭后已无法展示日志）。
func removeDirWithRetry(dir string, prog *Progress) {
	const attempts = 12
	var lastErr error
	for i := 0; i < attempts; i++ {
		if !dirExists(dir) {
			if prog != nil {
				prog.Log(LevelInfo, "已删除空安装目录：%s", dir)
			}
			return
		}
		if err := os.Remove(dir); err == nil {
			if prog != nil {
				prog.Log(LevelInfo, "已删除空安装目录：%s", dir)
			}
			return
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if prog != nil {
		prog.Log(LevelWarn, "安装目录已清空但无法删除（可能仍被其他程序占用）：%s（%v）", dir, lastErr)
	}
}

// silentUninstall 执行静默卸载，按约定输出 UNINSTALL_OK / UNINSTALL_FAIL。
func silentUninstall(opts cliOptions) int {
	prog := NewProgress(true)
	dir := opts.dir
	if strings.TrimSpace(dir) == "" {
		self, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "UNINSTALL_FAIL 无法定位卸载程序路径：%v\n", err)
			return 1
		}
		dir = filepath.Dir(self)
	}
	if err := Uninstall(dir, opts.removeData, prog); err != nil {
		fmt.Fprintf(os.Stderr, "UNINSTALL_FAIL %v\n", err)
		return 1
	}
	prog.Finish(nil, dir)
	fmt.Printf("UNINSTALL_OK %s\n", dir)
	return 0
}
