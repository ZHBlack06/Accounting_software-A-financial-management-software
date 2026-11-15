// Package config 负责后端运行配置：端口、数据目录、时区、OCR 端点、前端静态目录。
// 优先级：命令行参数 > 环境变量 > 默认值。
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Version 与二进制发布版本一致，写入 meta.app_version 并用于 /api/health。
const Version = "1.2.0"

// DefaultWebDir 是前端构建产物默认目录（相对可执行文件工作目录）。
const DefaultWebDir = "web/dist"

// Config 聚合全部运行配置。
type Config struct {
	Port        int    // HTTP 监听端口；0 表示由操作系统分配空闲端口
	Host        string // 监听地址（仅本机）
	DataDir     string // 数据目录（含 ledger.db）
	OCREndpoint string // Python OCR 服务地址
	WebDir      string // 前端静态资源目录（SPA 根）
	Version     string
	PrintToken  bool // --print-token：迁移/种子后仅输出访问令牌并退出
}

// Local 返回应用时区（固定 Asia/Shanghai）。
// Windows 无 tzdata 时 time.LoadLocation 会失败，回退到固定 +08:00，
// 否则会 panic（团队约束，必须这样处理）。
func Local() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*3600)
	}
	return loc
}

// Default 返回内置默认配置（不含环境变量与命令行参数）。
// 桌面壳自行装配配置时复用此函数，保证默认值只有一处定义。
func Default() *Config {
	return &Config{
		Port:        8770,
		Host:        "127.0.0.1",
		DataDir:     "data",
		OCREndpoint: "http://127.0.0.1:8771",
		WebDir:      DefaultWebDir,
		Version:     Version,
	}
}

// ApplyEnv 用环境变量覆盖配置（LEDGER_* 优先，OCR_BASE_URL 为兼容别名）。
// 返回自身便于链式调用。
func (c *Config) ApplyEnv() *Config {
	if v := os.Getenv("LEDGER_HOST"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("LEDGER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Port = n
		}
	}
	if v := os.Getenv("LEDGER_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("LEDGER_WEB_DIR"); v != "" {
		c.WebDir = v
	}
	// OCR 地址：OCR_BASE_URL 为主，LEDGER_OCR_ENDPOINT 兼容
	if v := os.Getenv("LEDGER_OCR_ENDPOINT"); v != "" {
		c.OCREndpoint = v
	}
	if v := os.Getenv("OCR_BASE_URL"); v != "" {
		c.OCREndpoint = v
	}
	return c
}

// Load 解析命令行与环境变量，返回最终配置。
func Load() *Config {
	cfg := Default().ApplyEnv()

	fs := flag.NewFlagSet("ledger", flag.ContinueOnError)
	fs.StringVar(&cfg.Host, "host", cfg.Host, "HTTP listen host")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "HTTP listen port (0 = OS assigned)")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	fs.StringVar(&cfg.OCREndpoint, "ocr-endpoint", cfg.OCREndpoint, "OCR service endpoint")
	fs.StringVar(&cfg.WebDir, "web-dir", cfg.WebDir, "web assets directory (SPA root)")
	fs.BoolVar(&cfg.PrintToken, "print-token", false, "print auth token (after migrate/seed) and exit")
	_ = fs.Parse(os.Args[1:])
	return cfg
}

// Addr 返回监听地址。Port==0 时返回 "host:0"，表示端口由操作系统在内核中分配，
// 真实端口需在监听成功后从 net.Listener.Addr() 取回（见 app.App.Addr）。
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// DBPath 返回数据库文件绝对路径。
func (c *Config) DBPath() string {
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return filepath.Join(c.DataDir, "ledger.db")
	}
	return filepath.Join(abs, "ledger.db")
}

// EnsureDirs 启动时创建全部数据子目录（幂等）。
func (c *Config) EnsureDirs() error {
	dirs := []string{
		c.DataDir,
		filepath.Join(c.DataDir, "screenshots", "originals"),
		filepath.Join(c.DataDir, "screenshots", "compressed"),
		filepath.Join(c.DataDir, "ocr-results"),
		filepath.Join(c.DataDir, "backups"),
		filepath.Join(c.DataDir, "tmp", "import"),
		filepath.Join(c.DataDir, "logs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("创建数据目录 %s 失败: %w", d, err)
		}
	}
	return nil
}
