// Command build 负责组装「个人记账」Windows 安装包（支持多版本 profile）。
//
// 流程：收集发布文件 -> 打包 payload.zip + payload.json -> 编译 setup.exe。
// 该命令可重复执行：每次都会重新生成 payload 并重新编译，产物覆盖同名文件。
//
// 用法（在 installer-kit 目录下）：
//
//	go run ./build                                  # 默认 v1.0
//	go run ./build -profile v1.1                     # 构建 v1.1 桌面版安装包
//	go run ./build -profile v1.2                     # 构建 v1.2 桌面版安装包
//	go run ./build -profile v1.1 -payload-only       # 只重建 payload
//	go run ./build -profile v1.1 -skip-payload       # 复用已有 payload 只编译
//
// 实现要点：安装器二进制通过 -ldflags "-X main.profileName=<profile>" 在编译期
// 确定版本差异（主程序名、默认目录、注册表键、快捷方式目标、数据目录位置等），
// 因此两个版本共享同一份安装器源码，v1.0 不传该参数即为历史行为。
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// buildProfile 描述一个可构建的发行版本的差异项。
type buildProfile struct {
	// Name 是 profile 标识，同时作为 -X main.profileName 的注入值。
	Name string
	// ProductVersion 是语义化版本号。
	ProductVersion string
	// DisplayVersion 是展示用短版本号（v1.0 / v1.1）。
	DisplayVersion string
	// DefaultSrc 是默认发布目录（相对 installer-kit 目录）。
	DefaultSrc string
	// OutputName 是安装包文件名（写入发布目录）。
	OutputName string
	// AppExe 是必须存在的主程序文件名。
	AppExe string
	// Files 是必须打包的单个文件（相对发布目录）。
	Files []string
	// OptionalFiles 是存在则打包、缺失则告警跳过的单个文件。
	OptionalFiles []string
	// Dirs 是必须打包的子目录（相对发布目录，正斜杠分隔）。
	Dirs []string
	// Skipped 是打包过程中按**文件名精确匹配**过滤掉的文件（过程性文档）。
	Skipped []string
	// SkipPrefixes 是按**文件名前缀**过滤（如 QA-REPORT 覆盖多份报告）；留空不影响老版本。
	SkipPrefixes []string
	// GenerateLauncher 为 true 时，缺失 启动.bat 会自动生成。
	GenerateLauncher bool
	// AllowPlan 为 true 时支持 -with-plan 把项目计划书.html 打入包内。
	AllowPlan bool
}

// profiles 是支持的构建配置表。
var profiles = map[string]buildProfile{
	"v1.0": {
		Name:             "v1.0",
		ProductVersion:   "1.0.0",
		DisplayVersion:   "v1.0",
		DefaultSrc:       "../记账v1.0",
		OutputName:       "记账v1.0-安装包.exe",
		AppExe:           "ledger.exe",
		Files:            []string{"ledger.exe", "README.md", "启动.bat"},
		OptionalFiles:    nil,
		Dirs:             []string{"web/dist", "docs", "scripts"},
		Skipped:          []string{"INSTALL-VERIFY.md"},
		GenerateLauncher: true,
		AllowPlan:        true,
	},
	"v1.1": {
		Name:           "v1.1",
		ProductVersion: "1.1.0",
		DisplayVersion: "v1.1",
		DefaultSrc:     "../记账v1.1",
		OutputName:     "记账v1.1-安装包.exe",
		AppExe:         "ledger-desktop.exe",
		Files:          []string{"ledger-desktop.exe"},
		// README-DESKTOP.md 由桌面版工程师提供，缺失时不阻塞出包（会告警）。
		OptionalFiles: []string{"README-DESKTOP.md"},
		Dirs:          []string{"docs"},
		// 过程性文档不打入用户安装目录：验收报告、状态说明等属于项目内部产物。
		Skipped:          []string{"INSTALL-VERIFY.md", "STATUS.md", "QA-REPORT-V11.md"},
		GenerateLauncher: false,
		AllowPlan:        false,
	},
	"v1.2": {
		Name:           "v1.2",
		ProductVersion: "1.2.0",
		DisplayVersion: "v1.2",
		DefaultSrc:     "../记账v1.2",
		OutputName:     "记账v1.2-安装包.exe",
		AppExe:         "ledger-desktop.exe",
		Files:          []string{"ledger-desktop.exe", "使用手册.html"},
		// README-DESKTOP.md 是面向用户的桌面版说明，缺失时不阻塞出包（会告警）。
		OptionalFiles: []string{"README-DESKTOP.md"},
		Dirs:          []string{"docs"},
		// 过程性/内部文档不打入用户安装目录（v1.2 的报告与设计稿都在这两类里）。
		Skipped: []string{
			"INSTALL-VERIFY.md", "STATUS.md", "FINAL-VERIFY.md",
			"TEST-REPORT.md", "TASKS.md", "ARCHITECTURE-V12.md",
		},
		// QA-REPORT-V11.md / QA-REPORT-V12.md … 统一按前缀过滤，避免每加一份报告就要改表。
		SkipPrefixes:     []string{"QA-REPORT"},
		GenerateLauncher: false,
		AllowPlan:        false,
	},
}

// payloadEntry 描述一个待打包文件。
type payloadEntry struct {
	// Source 是源文件绝对路径。
	Source string
	// Name 是压缩包内的相对路径（统一使用正斜杠）。
	Name string
}

// payloadMeta 与安装器读取的 payload.json 结构一致。
type payloadMeta struct {
	Product          string `json:"product"`
	Version          string `json:"version"`
	Files            int    `json:"files"`
	UncompressedSize int64  `json:"uncompressed_size"`
	CompressedSize   int64  `json:"compressed_size"`
	SHA256           string `json:"sha256"`
	BuiltAt          string `json:"built_at"`
}

// productName 与安装器主程序保持一致（两个版本共用）。
const productName = "个人记账"

// launcherContent 是 v1.0 缺少 启动.bat 时自动生成的内容。
const launcherContent = "@echo off\r\n" +
	"chcp 65001 >nul\r\n" +
	"cd /d \"%~dp0\"\r\n" +
	"title 个人记账 v1.0\r\n" +
	"echo ================================================\r\n" +
	"echo   个人记账 v1.0  正在启动，请稍候...\r\n" +
	"echo   浏览器将自动打开 http://127.0.0.1:8770\r\n" +
	"echo   使用期间请不要关闭本窗口；关闭窗口即退出程序。\r\n" +
	"echo ================================================\r\n" +
	"start \"\" http://127.0.0.1:8770\r\n" +
	"ledger.exe\r\n" +
	"echo.\r\n" +
	"echo 程序已退出。\r\n" +
	"pause\r\n"

// buildOptions 汇总命令行参数。
type buildOptions struct {
	profileName string
	profile     buildProfile
	srcDir      string
	outPath     string
	payloadOnly bool
	skipPayload bool
	withPlan    bool
}

func main() {
	var opts buildOptions
	flag.StringVar(&opts.profileName, "profile", "v1.0", "发行版本 profile：v1.0 / v1.1 / v1.2")
	flag.StringVar(&opts.srcDir, "src", "", "发布目录（默认按 profile 取值：../记账v1.0 或 ../记账v1.1）")
	flag.StringVar(&opts.outPath, "out", "", "安装包输出路径（默认 <src>/<profile 输出名>）")
	flag.BoolVar(&opts.payloadOnly, "payload-only", false, "只重新生成 payload，不编译")
	flag.BoolVar(&opts.skipPayload, "skip-payload", false, "复用已有 payload，只编译")
	flag.BoolVar(&opts.withPlan, "with-plan", false, "是否把项目根目录的项目计划书.html 打入安装包（仅 v1.0 支持，默认关闭）")
	flag.Parse()

	p, ok := profiles[opts.profileName]
	if !ok {
		must(fmt.Errorf("未知的 profile %q（支持：v1.0、v1.1、v1.2）", opts.profileName))
	}
	opts.profile = p
	fmt.Printf("[profile] %s（版本 %s，主程序 %s）\n", p.Name, p.ProductVersion, p.AppExe)

	workDir, err := os.Getwd()
	must(err)

	src := opts.srcDir
	if src == "" {
		src = p.DefaultSrc
	}
	srcAbs, err := filepath.Abs(src)
	must(err)
	payloadDir := filepath.Join(workDir, "payload")

	if !opts.skipPayload {
		meta, err := buildPayload(p, srcAbs, payloadDir, opts.withPlan)
		must(err)
		fmt.Printf("[payload] %d 个文件，原始 %s，压缩后 %s\n",
			meta.Files, human(meta.UncompressedSize), human(meta.CompressedSize))
		fmt.Printf("[payload] sha256=%s\n", meta.SHA256)
	} else {
		fmt.Println("[payload] 跳过重新打包（-skip-payload）")
		if err := checkPayloadConsistency(p, payloadDir); err != nil {
			must(err)
		}
	}

	if opts.payloadOnly {
		fmt.Println("[done] 仅重建 payload")
		return
	}

	out := opts.outPath
	if out == "" {
		// 产物命名与发布目录保持一致：记账v1.1-安装包.exe
		out = filepath.Join(srcAbs, p.OutputName)
	}
	out, err = filepath.Abs(out)
	must(err)
	must(ensureParent(out))

	fmt.Println("[build] 编译安装包…")
	ldflags := "-s -w -H=windowsgui"
	if p.Name != "v1.0" {
		// v1.0 不注入 profileName，保持与历史构建完全一致（默认值即 v1.0）。
		ldflags = fmt.Sprintf("-s -w -H=windowsgui -X main.profileName=%s", p.Name)
	}
	args := []string{
		"build",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags", ldflags,
		"-o", out,
		".",
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if err := cmd.Run(); err != nil {
		must(fmt.Errorf("编译失败：%w", err))
	}

	info, err := os.Stat(out)
	must(err)
	sum, err := fileSHA256(out)
	must(err)
	fmt.Printf("[build] 安装包已生成：%s（%s，%d 字节）\n", out, human(info.Size()), info.Size())
	fmt.Printf("[build] sha256=%s\n", sum)
}

// ensureParent 确保输出文件的父目录存在。
func ensureParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// checkPayloadConsistency 在 -skip-payload 时校验已有 payload.json 的版本与 profile 一致，
// 避免误用上一版本残留的 payload 出包。
func checkPayloadConsistency(p buildProfile, payloadDir string) error {
	path := filepath.Join(payloadDir, "payload.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("无法读取 %s（请先去掉了 -skip-payload 重新打包）：%w", path, err)
	}
	var meta payloadMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("解析 %s 失败：%w", path, err)
	}
	if meta.Version != p.ProductVersion {
		return fmt.Errorf("payload 版本不匹配：文件内为 %s，当前 profile 为 %s", meta.Version, p.ProductVersion)
	}
	fmt.Printf("[payload] 复用已有载荷：%d 个文件，版本 %s\n", meta.Files, meta.Version)
	return nil
}

// buildPayload 收集文件并生成 payload.zip 与 payload.json。
func buildPayload(p buildProfile, srcDir, payloadDir string, withPlan bool) (payloadMeta, error) {
	var meta payloadMeta
	exePath := filepath.Join(srcDir, p.AppExe)
	if _, err := os.Stat(exePath); err != nil {
		return meta, fmt.Errorf("未找到主程序 %s（profile %s），请检查 -src 参数：%w",
			exePath, p.Name, err)
	}

	// 确保启动脚本存在（仅 v1.0，缺失时自动生成，保持安装包开箱可用）。
	if p.GenerateLauncher {
		launcher := filepath.Join(srcDir, "启动.bat")
		if _, err := os.Stat(launcher); err != nil {
			if err := os.WriteFile(launcher, []byte(launcherContent), 0o644); err != nil {
				return meta, fmt.Errorf("生成 启动.bat 失败：%w", err)
			}
			fmt.Printf("[payload] 已生成缺失的 %s\n", launcher)
		}
	}

	entries, err := collect(p, srcDir, withPlan)
	if err != nil {
		return meta, err
	}
	if len(entries) == 0 {
		return meta, errors.New("没有收集到任何文件")
	}

	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		return meta, err
	}
	zipPath := filepath.Join(payloadDir, "payload.zip")
	if err := writeZip(zipPath, entries); err != nil {
		return meta, err
	}

	var uncompressed int64
	for _, e := range entries {
		info, err := os.Stat(e.Source)
		if err != nil {
			return meta, err
		}
		uncompressed += info.Size()
	}

	zipped, err := os.Stat(zipPath)
	if err != nil {
		return meta, err
	}
	sum, err := fileSHA256(zipPath)
	if err != nil {
		return meta, err
	}

	meta = payloadMeta{
		Product:          productName,
		Version:          p.ProductVersion,
		Files:            len(entries),
		UncompressedSize: uncompressed,
		CompressedSize:   zipped.Size(),
		SHA256:           sum,
		BuiltAt:          time.Now().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return meta, err
	}
	if err := os.WriteFile(filepath.Join(payloadDir, "payload.json"), data, 0o644); err != nil {
		return meta, err
	}
	return meta, nil
}

// collect 按 profile 的白名单收集待打包文件。
//
// v1.0：ledger.exe、web/dist/**、docs/**、scripts/**、README.md、启动.bat
// （withPlan 为真时再加项目根目录的 项目计划书.html）。
// v1.1：ledger-desktop.exe、docs/**、README-DESKTOP.md
// （不含 web/、scripts/、启动.bat；docs 内过滤 INSTALL-VERIFY.md 与 STATUS.md）。
// 明确排除：data/（用户数据）、node_modules、.git 等。
func collect(p buildProfile, srcDir string, withPlan bool) ([]payloadEntry, error) {
	var entries []payloadEntry
	skipped := make(map[string]bool, len(p.Skipped))
	for _, name := range p.Skipped {
		skipped[name] = true
	}
	// shouldSkip 判定是否属于「过程性/内部文档」：精确名 或 前缀命中。
	shouldSkip := func(base string) bool {
		if skipped[base] {
			return true
		}
		for _, pre := range p.SkipPrefixes {
			if pre != "" && strings.HasPrefix(base, pre) {
				return true
			}
		}
		return false
	}

	addFile := func(abs, name string) error {
		info, err := os.Stat(abs)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		entries = append(entries, payloadEntry{Source: abs, Name: filepath.ToSlash(name)})
		return nil
	}

	addDir := func(rootAbs, rootName string) error {
		if _, err := os.Stat(rootAbs); err != nil {
			return nil // 目录不存在则跳过
		}
		return filepath.Walk(rootAbs, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if info.IsDir() {
				base := info.Name()
				if base == "node_modules" || base == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if shouldSkip(info.Name()) {
				fmt.Printf("[payload] 跳过过程性文档 %s\n", info.Name())
				return nil
			}
			rel, relErr := filepath.Rel(rootAbs, path)
			if relErr != nil {
				return relErr
			}
			name := filepath.Join(rootName, rel)
			return addFile(path, name)
		})
	}

	// 单个文件（必需的缺失即报错）。
	for _, name := range p.Files {
		if err := addFile(filepath.Join(srcDir, name), name); err != nil {
			return nil, fmt.Errorf("缺少必需文件 %s（profile %s）：%w", name, p.Name, err)
		}
	}

	// 可选文件（缺失只告警）。
	for _, name := range p.OptionalFiles {
		if err := addFile(filepath.Join(srcDir, name), name); err != nil {
			fmt.Printf("[payload] 跳过缺失的可选文件 %s\n", name)
		}
	}

	// 目录。
	for _, d := range p.Dirs {
		abs := filepath.Join(srcDir, filepath.FromSlash(d))
		if err := addDir(abs, d); err != nil {
			return nil, err
		}
	}

	// 项目根目录的项目计划书（与发布目录同级）；内部文档，仅 v1.0 支持且默认关闭。
	if p.AllowPlan {
		if !withPlan {
			fmt.Println("[payload] 已按 -with-plan=false 跳过 项目计划书.html")
		} else {
			plan := filepath.Join(filepath.Dir(srcDir), "项目计划书.html")
			if _, err := os.Stat(plan); err == nil {
				if err := addFile(plan, "项目计划书.html"); err != nil {
					return nil, err
				}
			} else {
				fmt.Println("[payload] 未找到 项目计划书.html，跳过")
			}
		}
	} else if withPlan {
		fmt.Printf("[payload] profile %s 不支持内置项目计划书，已忽略 -with-plan\n", p.Name)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// writeZip 以 deflate 压缩写出 zip 包。
func writeZip(zipPath string, entries []payloadEntry) error {
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	for _, e := range entries {
		info, err := os.Stat(e.Source)
		if err != nil {
			zw.Close()
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			zw.Close()
			return err
		}
		hdr.Name = e.Name
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			zw.Close()
			return err
		}
		in, err := os.Open(e.Source)
		if err != nil {
			zw.Close()
			return err
		}
		if _, err := io.Copy(w, in); err != nil {
			in.Close()
			zw.Close()
			return err
		}
		in.Close()
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return out.Close()
}

// fileSHA256 计算文件摘要。
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// human 把字节数格式化为人类可读字符串。
func human(bytes int64) string {
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

// must 简化错误处理：出错即终止并打印可读信息。
func must(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "[") {
		fmt.Fprintf(os.Stderr, "[error] %s\n", msg)
	}
	os.Exit(1)
}
