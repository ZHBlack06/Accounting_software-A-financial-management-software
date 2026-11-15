package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateInstallDir 覆盖安装路径校验的关键分支。
func TestValidateInstallDir(t *testing.T) {
	valid := []string{
		`D:\Apps\个人记账v1.0`,
		`C:\Users\Public\记账`,
	}
	for _, v := range valid {
		if _, err := ValidateInstallDir(v); err != nil {
			t.Errorf("期望通过校验但失败：%s -> %v", v, err)
		}
	}

	invalid := []string{
		``,
		`个人记账`,
		`relative\path`,
		`C:\Windows`,
		`C:\Windows\System32\记账`,
		`C:\Program Files\记账`,
		`C:\Program Files (x86)\记账`,
		`D:\`,
		`D:/`,
		`D:\Apps\记|账`,
		`D:\Apps\记<账>`,
		`D:\Apps\记账:1`,
	}
	for _, v := range invalid {
		if _, err := ValidateInstallDir(v); err == nil {
			t.Errorf("期望校验失败但通过了：%s", v)
		}
	}
}

// TestExpandWinEnv 验证 %VAR% 形式的环境变量展开。
func TestExpandWinEnv(t *testing.T) {
	t.Setenv("LEDGER_TEST_VAR", `D:\示例目录`)
	got := expandWinEnv(`%LEDGER_TEST_VAR%\子目录`)
	want := `D:\示例目录\子目录`
	if got != want {
		t.Fatalf("展开结果不符：got=%q want=%q", got, want)
	}
	if out := expandWinEnv(`%不存在的变量%\x`); out != `%不存在的变量%\x` {
		t.Fatalf("未知变量应原样保留，got=%q", out)
	}
}

// TestPowerShellScriptsParse 用 PowerShell 自带语法分析器校验生成的脚本。
// 脚本本身通过 -EncodedCommand 执行，这里只做静态语法检查，不会真正创建快捷方式。
func TestPowerShellScriptsParse(t *testing.T) {
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("未找到 powershell，跳过脚本语法校验")
	}

	scripts := map[string]string{
		"shortcut": buildShortcutScript(
			`C:\Users\测试用户\Desktop\个人记账 v1.0.lnk`,
			`C:\Apps\个人记账v1.0\启动.bat`,
			`C:\Apps\个人记账v1.0`,
			`C:\Apps\个人记账v1.0\ledger.exe,0`,
			`个人记账 v1.0`,
			"",
		),
		"uninstall-shortcut": buildShortcutScript(
			`C:\Users\测试用户\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\个人记账 v1.1\卸载 个人记账 v1.1.lnk`,
			`C:\Apps\个人记账v1.1\uninstall.exe`,
			`C:\Apps\个人记账v1.1`,
			`C:\Apps\个人记账v1.1\ledger-desktop.exe,0`,
			`卸载 个人记账 v1.1`,
			"--uninstall",
		),
		"folder": buildFolderDialogScript(`C:\Apps\个人记账v1.0`),
		"kill":   buildKillScript(`C:\Apps\个人记账v1.1`, []string{"ledger-desktop.exe", "ledger.exe"}),
	}

	dir := t.TempDir()
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".ps1")
			// Windows PowerShell 5.1 仅在存在 BOM 时才按 UTF-8 解析 .ps1，
			// 否则会按 ANSI 读取导致中文乱码，这里显式写入 UTF-8 BOM。
			content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(script)...)
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatalf("写入脚本失败：%v", err)
			}
			check := `$errs = $null; $toks = $null; ` +
				`[void][System.Management.Automation.Language.Parser]::ParseFile('` + path + `', [ref]$toks, [ref]$errs); ` +
				`if ($errs.Count -gt 0) { $errs | ForEach-Object { Write-Output $_.Message }; exit 1 } ` +
				`else { Write-Output 'PARSE_OK' }`
			cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", check)
			out, err := cmd.CombinedOutput()
			text := strings.TrimSpace(string(out))
			if err != nil || !strings.Contains(text, "PARSE_OK") {
				t.Fatalf("PowerShell 语法校验失败：%v\n%s", err, text)
			}
			t.Logf("%s 脚本语法校验通过", name)
		})
	}
}

// TestBuildShortcutScriptArguments 校验卸载快捷方式一定带 --uninstall 参数，
// 且普通快捷方式不写入多余的 Arguments（QA 报的 P1 缺陷的回归防线）。
func TestBuildShortcutScriptArguments(t *testing.T) {
	uninstall := buildShortcutScript(
		`C:\Users\u\Desktop\卸载 个人记账 v1.1.lnk`,
		`C:\Apps\个人记账v1.1\uninstall.exe`,
		`C:\Apps\个人记账v1.1`,
		`C:\Apps\个人记账v1.1\ledger-desktop.exe,0`,
		`卸载 个人记账 v1.1`,
		"--uninstall",
	)
	if !strings.Contains(uninstall, `$sc.Arguments = '--uninstall'`) {
		t.Fatalf("卸载快捷方式脚本必须写入 $sc.Arguments = '--uninstall'，实际：\n%s", uninstall)
	}

	normal := buildShortcutScript(
		`C:\Users\u\Desktop\个人记账 v1.1.lnk`,
		`C:\Apps\个人记账v1.1\ledger-desktop.exe`,
		`C:\Apps\个人记账v1.1`,
		`C:\Apps\个人记账v1.1\ledger-desktop.exe,0`,
		`个人记账 v1.1`,
		"",
	)
	if strings.Contains(normal, "$sc.Arguments") {
		t.Fatalf("普通快捷方式不应写入 Arguments，实际：\n%s", normal)
	}
}

// TestApplyUninstallAutoDetect 校验「按可执行文件名兜底进入卸载模式」。
func TestApplyUninstallAutoDetect(t *testing.T) {
	cases := []struct {
		exe  string
		want bool
	}{
		{`C:\Apps\个人记账v1.1\uninstall.exe`, true},
		{`C:\Apps\个人记账v1.0\UNINSTALL.EXE`, true},
		{`D:\Project\记账\记账v1.1\记账v1.1-安装包.exe`, false},
		{`D:\Project\记账\记账v1.0\记账v1.0-安装包.exe`, false},
	}
	for _, c := range cases {
		opts := cliOptions{}
		applyUninstallAutoDetect(c.exe, &opts)
		if opts.uninstall != c.want {
			t.Errorf("%s -> uninstall=%v, want %v", c.exe, opts.uninstall, c.want)
		}
	}

	// 显式参数优先，且自清理代理不会被误判为卸载。
	opts := cliOptions{uninstall: false, selfClean: true}
	applyUninstallAutoDetect(`C:\Temp\ledger-uninstaller-1.exe`, &opts)
	if opts.uninstall {
		t.Error("自清理代理不应进入卸载模式")
	}
}

// TestSafeJoin 验证压缩包路径不会越界。
func TestSafeJoin(t *testing.T) {
	base := `D:\Apps\记账`
	if _, err := safeJoin(base, `web/dist/index.html`); err != nil {
		t.Fatalf("正常路径不应报错：%v", err)
	}
	for _, bad := range []string{`..\evil.exe`, `../../evil.exe`, `C:\evil.exe`} {
		if _, err := safeJoin(base, bad); err == nil {
			t.Fatalf("越界路径应被拒绝：%s", bad)
		}
	}
}

// TestBuildKillScript 校验「只结束本安装目录内进程」的脚本生成规则（QA 报的 P2）。
func TestBuildKillScript(t *testing.T) {
	script := buildKillScript(`D:\Apps\个人记账v1.1`, []string{"ledger-desktop.exe", "ledger.exe"})
	if script == "" {
		t.Fatal("脚本不应为空")
	}
	// 路径前缀必须带尾部分隔符并统一小写，避免 D:\Apps\x 误匹配 D:\Apps\xyz。
	if !strings.Contains(script, `'d:\apps\个人记账v1.1\'`) {
		t.Fatalf("缺少规范化后的安装目录前缀：\n%s", script)
	}
	// 进程名不带 .exe（Get-Process -Name 要求不含扩展名）。
	if !strings.Contains(script, `'ledger-desktop'`) || !strings.Contains(script, `'ledger'`) {
		t.Fatalf("进程名应为不带扩展名的形式：\n%s", script)
	}
	if !strings.Contains(script, "StartsWith($dirPrefix)") {
		t.Fatalf("必须按安装目录前缀过滤：\n%s", script)
	}
	// 绝不能出现无差别杀进程的写法。
	if strings.Contains(script, "taskkill") {
		t.Fatalf("不应使用按镜像名全杀的 taskkill：\n%s", script)
	}
	if got := buildKillScript(`D:\Apps\x`, nil); got != "" {
		t.Fatalf("进程名为空时应返回空脚本，实际：%s", got)
	}
}

// TestBuildCleanupCmdLine 校验清理代理的命令行写法。
// 实测 `cmd /c start /b "" "<脚本>"` 在 DETACHED_PROCESS 下会静默失败（脚本不执行、
// 且 .cmd 残留），因此必须使用 `cmd /c call "<脚本>" ...`。
func TestBuildCleanupCmdLine(t *testing.T) {
	got := buildCleanupCmdLine(`C:\Windows\System32\cmd.exe`, `C:\Temp\jz-clean-1.cmd`,
		`D:\Apps\个人记账v1.1\uninstall.exe`, `D:\Apps\个人记账v1.1`, "purge")
	for _, want := range []string{`/c call "`, `"C:\Temp\jz-clean-1.cmd"`, `uninstall.exe`, `purge`} {
		if !strings.Contains(got, want) {
			t.Errorf("命令行缺少片段 %q：%s", want, got)
		}
	}
	if strings.Contains(got, "start /b") {
		t.Errorf("不应使用 start /b（实测静默失败）：%s", got)
	}
}

// TestCleanupAgentScript 校验清理代理脚本（P2 修复）：纯 ASCII、自删除、不再复制本体。
func TestCleanupAgentScript(t *testing.T) {
	for i := 0; i < len(cleanupAgentScript); i++ {
		if cleanupAgentScript[i] > 0x7F {
			t.Fatalf("代理脚本必须为纯 ASCII（cmd 按 OEM 代码页解析），偏移 %d 处出现非 ASCII 字节", i)
		}
	}
	for _, want := range []string{
		`set "EXE=%~1"`, // 路径经参数传入，避免中文写进 .cmd
		`set "DIR=%~2"`,
		`del /f /q "%EXE%"`,
		`rd "%DIR%"`, // 非 purge：仅在目录已空时删除
		`rmdir /s /q "%DIR%"`,
		`del /f /q "%~f0"`, // 代理自删除，TEMP 不留 .cmd
	} {
		if !strings.Contains(cleanupAgentScript, want) {
			t.Errorf("代理脚本缺少片段：%s", want)
		}
	}
	if len(cleanupAgentScript) > 1024 {
		t.Errorf("代理脚本应 <1KB，实际 %d 字节", len(cleanupAgentScript))
	}
}

// TestValidateInstallDirExpandsEnv 验证三种环境变量写法都能被展开为绝对路径。
// 历史实现只调用 os.ExpandEnv，导致文档宣称支持的 %VAR% 写法实际被拒绝（QA 发现的缺陷）。
func TestValidateInstallDirExpandsEnv(t *testing.T) {
	t.Setenv("LEDGER_TEST_BASE", `D:\测试根`)
	want := `D:\测试根\记账v1.1`
	cases := []string{
		`%LEDGER_TEST_BASE%\记账v1.1`,
		`$LEDGER_TEST_BASE\记账v1.1`,
		`${LEDGER_TEST_BASE}\记账v1.1`,
		`$env:LEDGER_TEST_BASE\记账v1.1`,
	}
	for _, in := range cases {
		got, err := ValidateInstallDir(in)
		if err != nil {
			t.Errorf("环境变量写法应被展开并通过校验：%s -> %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("展开结果不符：%s -> got=%q want=%q", in, got, want)
		}
	}

	// 未定义的变量不会被静默清空，仍应因「不是绝对路径」被拒绝。
	if _, err := ValidateInstallDir(`%LEDGER_NO_SUCH_VAR%\记账`); err == nil {
		t.Error("未定义的环境变量应被拒绝而非静默通过")
	}
}

// TestExpandPowerShellEnv 覆盖 $env:NAME 的展开细节。
func TestExpandPowerShellEnv(t *testing.T) {
	t.Setenv("LEDGER_ENV_STYLE", `D:\a`)
	t.Setenv("ProgramFiles(x86)", `C:\Program Files (x86)`)
	cases := []struct{ in, want string }{
		{`$env:LEDGER_ENV_STYLE\b`, `D:\a\b`},
		{`$env:ProgramFiles(x86)\x`, `C:\Program Files (x86)\x`},
		// 未定义变量：原样保留，交由后续校验报错。
		{`$env:LEDGER_NOT_DEFINED\b`, `$env:LEDGER_NOT_DEFINED\b`},
		// 普通 $VAR 不应被本函数处理（由 os.ExpandEnv 负责）。
		{`$LEDGER_ENV_STYLE\b`, `$LEDGER_ENV_STYLE\b`},
	}
	for _, c := range cases {
		if got := expandPowerShellEnv(c.in); got != c.want {
			t.Errorf("expandPowerShellEnv(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestUnderDir 校验快捷方式删除前的路径安全判断（防止配置异常时误删）。
func TestUnderDir(t *testing.T) {
	base := `C:\Users\u\AppData\Roaming\Microsoft\Windows\Start Menu\Programs`
	cases := []struct {
		path string
		dir  string
		want bool
	}{
		{base + `\个人记账 v1.1`, base, true},
		{base + `\个人记账 v1.1\卸载 个人记账 v1.1.lnk`, base, true},
		{`c:\users\U\appdata\roaming\microsoft\windows\start menu\programs\x`, base, true},
		{`C:\Users\u\AppData\Roaming\Microsoft\Windows\Start Menu\ProgramsOther\x`, base, false},
		{`C:\Windows\System32`, base, false},
		{``, base, false},
		{base, ``, false},
	}
	for _, c := range cases {
		if got := underDir(c.path, c.dir); got != c.want {
			t.Errorf("underDir(%q, %q) = %v, want %v", c.path, c.dir, got, c.want)
		}
	}
}

// TestResolveProfile 校验两个 profile 的差异项互不冲突（可共存）。
func TestResolveProfile(t *testing.T) {
	v10 := resolveProfile("v1.0")
	v11 := resolveProfile("v1.1")

	if v10.AppExeName != "ledger.exe" || v10.LauncherName != "启动.bat" {
		t.Fatalf("v1.0 主程序/启动脚本不符：%+v", v10)
	}
	if v11.AppExeName != "ledger-desktop.exe" || v11.LauncherName != "" {
		t.Fatalf("v1.1 主程序/启动脚本不符：%+v", v11)
	}
	if v11.ShortcutTarget != v11.AppExeName {
		t.Fatalf("v1.1 快捷方式应指向桌面版主程序：%s", v11.ShortcutTarget)
	}
	if v10.Version == v11.Version {
		t.Fatalf("两个 profile 版本号不应相同：%s", v10.Version)
	}
	// 卸载注册表键与互斥量必须区分，否则两版本无法共存。
	if v10.UninstallKeyName == v11.UninstallKeyName {
		t.Fatalf("卸载注册表键重名：%s", v10.UninstallKeyName)
	}
	if v10.MutexName == v11.MutexName {
		t.Fatalf("互斥量重名：%s", v10.MutexName)
	}
	if v10.DefaultDirName == v11.DefaultDirName {
		t.Fatalf("默认安装目录重名：%s", v10.DefaultDirName)
	}
	// 未知 profile 必须安全回退到 v1.0。
	if got := resolveProfile("v9.9"); got.Name != v10.Name {
		t.Fatalf("未知 profile 应回退到 v1.0，实际 %s", got.Name)
	}
}

// TestProfileDataDir 校验数据目录位置：v1.0 在安装目录内，v1.1 在 %LOCALAPPDATA% 下。
func TestProfileDataDir(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `D:\TestLocalAppData`)
	installDir := `D:\Apps\个人记账v1.1`

	got10 := profileV10.DataDir(installDir)
	want10 := filepath.Join(installDir, DataDirName)
	if got10 != want10 {
		t.Fatalf("v1.0 数据目录应为 %s，实际 %s", want10, got10)
	}

	got11 := profileV11.DataDir(installDir)
	want11 := filepath.Join(`D:\TestLocalAppData`, ProductName, DataDirName)
	if got11 != want11 {
		t.Fatalf("v1.1 数据目录应为 %s，实际 %s", want11, got11)
	}
	// 关键约束：v1.1 的用户数据不得位于安装目录内，否则卸载/重装会误删账本。
	if strings.HasPrefix(strings.ToLower(got11), strings.ToLower(installDir)) {
		t.Fatalf("v1.1 数据目录不应位于安装目录内：%s", got11)
	}
}

// TestProfileV12 v1.2 profile：与 v1.1 **共用**数据目录（升级即延续），卸载默认不删数据。
func TestProfileV12(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `D:/TestLocalAppData`)
	installDir := `D:/Apps/记账v1.2`

	p := resolveProfile("v1.2")
	if p.Name != "v1.2" || p.Version != "1.2.0" || p.DisplayVersion != "v1.2" {
		t.Fatalf("v1.2 元信息错误：%+v", p)
	}
	if p.AppExeName != "ledger-desktop.exe" || p.ShortcutTarget != "ledger-desktop.exe" {
		t.Errorf("主程序/快捷方式目标错误：%s / %s", p.AppExeName, p.ShortcutTarget)
	}
	if p.DefaultDirName != "记账v1.2" {
		t.Errorf("默认目录名 = %q，want 记账v1.2", p.DefaultDirName)
	}
	if p.UninstallKeyName != "个人记账v1.2" || p.UninstallDisplayName != "个人记账 v1.2" {
		t.Errorf("卸载注册表键/显示名错误：%s / %s", p.UninstallKeyName, p.UninstallDisplayName)
	}
	// 安装器互斥量必须与 v1.0 / v1.1 不同，否则两个版本的安装包不能同时打开。
	if p.MutexName == profileV10.MutexName || p.MutexName == profileV11.MutexName {
		t.Errorf("v1.2 安装器互斥量与其他版本重复：%s", p.MutexName)
	}
	// 关键：数据目录不在安装目录内，且与 v1.1 **完全相同**（升级即延续账本）。
	got := p.DataDir(installDir)
	want := filepath.Join(`D:/TestLocalAppData`, ProductName, DataDirName)
	if got != want {
		t.Fatalf("v1.2 数据目录 = %s，want %s", got, want)
	}
	if got != profileV11.DataDir(`D:/Apps/个人记账v1.1`) {
		t.Error("v1.2 必须与 v1.1 共用同一数据目录")
	}
	if strings.HasPrefix(strings.ToLower(got), strings.ToLower(installDir)) {
		t.Fatalf("v1.2 数据目录不应位于安装目录内：%s", got)
	}
	if p.DataInInstallDir {
		t.Error("v1.2 的 DataInInstallDir 必须为 false（卸载默认不删数据）")
	}
	// 卸载只结束本安装目录内的 desktop 进程，不得波及 v1.0 的 ledger.exe。
	if len(p.KillProcesses) != 1 || p.KillProcesses[0] != "ledger-desktop.exe" {
		t.Errorf("KillProcesses = %v，want [ledger-desktop.exe]", p.KillProcesses)
	}
	// 未注入 profileName 时默认仍是 v1.0（保证历史构建不受影响）。
	if resolveProfile("").Name != "v1.0" {
		t.Error("空 profile 必须回退 v1.0")
	}
}
