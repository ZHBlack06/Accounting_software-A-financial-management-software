package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxInstallPathLen 限制安装路径长度，避免触碰 MAX_PATH 边界。
const maxInstallPathLen = 200

// defaultInstallDir 返回默认安装位置：%LOCALAPPDATA%\Programs\个人记账v1.0（或 v1.1）。
// 故意不使用 Program Files，从而避免 UAC 提权与目录写权限问题。
func defaultInstallDir() string {
	return filepath.Join(localAppDataDir(), "Programs", prof.DefaultDirName)
}

// desktopDir 返回当前用户的桌面目录。
// 优先读取注册表 User Shell Folders（兼容 OneDrive 重定向），失败时回退到 %USERPROFILE%\Desktop。
func desktopDir() string {
	if p, err := readUserShellFolder("Desktop"); err == nil && p != "" {
		return p
	}
	if up := os.Getenv("USERPROFILE"); up != "" {
		return filepath.Join(up, "Desktop")
	}
	return filepath.Join(os.TempDir(), "Desktop")
}

// startMenuProgramsDir 返回开始菜单「程序」目录。
func startMenuProgramsDir() string {
	if p, err := readUserShellFolder("Programs"); err == nil && p != "" {
		return p
	}
	if ad := os.Getenv("APPDATA"); ad != "" {
		return filepath.Join(ad, "Microsoft", "Windows", "Start Menu", "Programs")
	}
	return ""
}

// desktopShortcutPath 返回桌面快捷方式的完整路径。
func desktopShortcutPath() string {
	return filepath.Join(desktopDir(), ProductName+" "+DisplayVersion+".lnk")
}

// startMenuFolder 返回开始菜单中的产品文件夹。
func startMenuFolder() string {
	base := startMenuProgramsDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, ProductName+" "+DisplayVersion)
}

// startMenuShortcutPath 返回开始菜单中主快捷方式的完整路径。
func startMenuShortcutPath() string {
	folder := startMenuFolder()
	if folder == "" {
		return ""
	}
	return filepath.Join(folder, ProductName+" "+DisplayVersion+".lnk")
}

// startMenuUninstallShortcutPath 返回开始菜单中卸载快捷方式的完整路径。
func startMenuUninstallShortcutPath() string {
	folder := startMenuFolder()
	if folder == "" {
		return ""
	}
	return filepath.Join(folder, "卸载 "+ProductName+" "+DisplayVersion+".lnk")
}

// isAbsWindowsPath 判断是否为 Windows 绝对路径（盘符或 UNC）。
func isAbsWindowsPath(p string) bool {
	if len(p) >= 3 {
		c := p[0]
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if isLetter && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
			return true
		}
	}
	return strings.HasPrefix(p, `\\`)
}

// isDriveRoot 判断是否为盘符根目录（例如 D:\）。
func isDriveRoot(p string) bool {
	cleaned := strings.TrimRight(p, `\/`)
	if len(cleaned) == 2 && cleaned[1] == ':' {
		return true
	}
	return len(p) == 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// protectedRoots 返回禁止安装的系统目录列表（统一大小写后用于前缀比较）。
func protectedRoots() []string {
	roots := []string{`C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)`}
	if v := os.Getenv("SystemRoot"); v != "" {
		roots = append(roots, v)
	}
	if v := os.Getenv("windir"); v != "" {
		roots = append(roots, v)
	}
	if v := os.Getenv("ProgramFiles"); v != "" {
		roots = append(roots, v)
	}
	if v := os.Getenv("ProgramW6432"); v != "" {
		roots = append(roots, v)
	}
	if v := os.Getenv("ProgramFiles(x86)"); v != "" {
		roots = append(roots, v)
	}
	return roots
}

// ValidateInstallDir 校验用户输入的安装位置，返回清理后的绝对路径。
// 校验规则：必须是绝对路径、不含非法字符、不能是系统保护目录、不能是盘符根目录。
func ValidateInstallDir(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, `"`)
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("安装位置不能为空")
	}
	// 允许用户输入环境变量写法（%LOCALAPPDATA%、$LOCALAPPDATA、$env:LOCALAPPDATA）。
	s = expandPathEnv(s)
	if len(s) > maxInstallPathLen {
		return "", fmt.Errorf("安装位置过长（最多 %d 个字符），请换一个更短的目录", maxInstallPathLen)
	}
	if !isAbsWindowsPath(s) {
		return "", fmt.Errorf("请输入绝对路径，例如 D:\\Apps\\%s", prof.DefaultDirName)
	}
	// 非法字符检查：'C:' 之后的冒号一律不允许。
	for i, r := range s {
		if r < 32 {
			return "", errors.New(`安装位置包含不可见控制字符`)
		}
		if strings.ContainsRune(`<>"|?*`, r) {
			return "", fmt.Errorf(`安装位置不能包含字符 %q`, r)
		}
		if r == ':' && i != 1 {
			return "", errors.New(`安装位置中的冒号只能出现在盘符后面`)
		}
	}
	cleaned := filepath.Clean(s)
	if isDriveRoot(cleaned) {
		return "", errors.New(`不能直接安装到磁盘根目录，请指定一个子目录`)
	}
	// UNC 路径至少要有 服务器\共享名\子目录 三段。
	if strings.HasPrefix(cleaned, `\\`) && strings.Count(cleaned, `\`) < 4 {
		return "", fmt.Errorf(`网络路径至少需要指定一个子目录，例如 \\server\share\%s`, prof.DefaultDirName)
	}
	lower := strings.ToLower(cleaned)
	for _, root := range protectedRoots() {
		r := strings.ToLower(strings.TrimRight(filepath.Clean(root), `\/`))
		if r == "" {
			continue
		}
		if lower == r || strings.HasPrefix(lower, r+`\`) {
			return "", fmt.Errorf("该位置受系统保护（%s），请换一个目录", filepath.Clean(root))
		}
	}
	return cleaned, nil
}

// expandPathEnv 展开安装路径中的环境变量。
//
// 必须同时支持三种写法，因为用户来源不同：
//   - Windows 原生写法 %LOCALAPPDATA%\Programs\记账v1.1（cmd、系统属性面板、文档里最常见）
//   - Go/shell 风格 $LOCALAPPDATA 与 ${LOCALAPPDATA}（由 os.ExpandEnv 处理）
//   - PowerShell 风格 $env:LOCALAPPDATA（必须最先处理：os.ExpandEnv 会把 `$env` 当成
//     名为 env 的变量展开成空串，反而把路径破坏成「:LOCALAPPDATA\...」）
//
// 顺序很关键：$env: -> $VAR -> %VAR%。少任何一步，对应写法都会被原样保留，
// 随后在「必须是绝对路径」校验里被拒绝。
func expandPathEnv(s string) string {
	return expandWinEnv(os.ExpandEnv(expandPowerShellEnv(s)))
}

// expandPowerShellEnv 展开 PowerShell 风格的 $env:NAME 写法。
// 未定义的变量原样保留（交由后续校验给出明确错误），不做静默清空。
func expandPowerShellEnv(s string) string {
	const prefix = "$env:"
	if !strings.Contains(s, prefix) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], prefix) {
			j := i + len(prefix)
			k := j
			for k < len(s) && isEnvNameByte(s[k]) {
				k++
			}
			if name := s[j:k]; name != "" {
				if v, ok := os.LookupEnv(name); ok {
					b.WriteString(v)
					i = k
					continue
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// isEnvNameByte 判断字节是否属于环境变量名（含 ProgramFiles(x86) 里的括号）。
func isEnvNameByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_', c == '(', c == ')':
		return true
	}
	return false
}

// ensureDir 逐级创建目录。仅接受绝对路径，避免因环境变量未展开而在当前目录误建目录。
func ensureDir(dir string) error {
	if dir == "" {
		return errors.New("目录为空")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("目录必须是绝对路径：%s", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录 %s 失败：%w", dir, err)
	}
	return nil
}

// fileExists 判断文件（或目录）是否存在。
func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// dirExists 判断目录是否存在。
func dirExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// dirSize 递归统计目录占用字节数。
func dirSize(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// existingVolumePath 从目标路径向上找到第一个已存在的目录，用于查询磁盘可用空间。
func existingVolumePath(target string) string {
	p := filepath.Clean(target)
	for {
		if dirExists(p) {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return parent
		}
		p = parent
	}
}

// humanSize 把字节数格式化为人类可读字符串。
func humanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), units[exp])
}
