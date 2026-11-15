// 个人记账软件 v1.1 后端入口（浏览器模式）：装配 config/app，监听端口并优雅退出。
//
// 全部装配逻辑在 internal/app 中，本文件只负责：日志路由、--print-token 快捷路径、
// 信号处理与退出码。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ledger/internal/app"
	"ledger/internal/config"
)

// version 允许通过 -ldflags "-X main.version=..." 覆盖。
var version = config.Version

func main() {
	cfg := config.Load()
	cfg.Version = version

	// --print-token 模式：日志走 stderr，保证 stdout 只有令牌本身（便于脚本 $(...) 捕获）
	logOut := os.Stdout
	if cfg.PrintToken {
		logOut = os.Stderr
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(logOut, &slog.HandlerOptions{Level: slog.LevelInfo})))

	application, err := app.New(cfg)
	if err != nil {
		slog.Error("启动失败", "err", err)
		os.Exit(1)
	}

	// --print-token：迁移/种子完成后只输出令牌（单行、无前缀），供脚本与首次登录使用
	if cfg.PrintToken {
		tok, tokErr := application.Meta.Get("auth_token")
		if tokErr != nil || tok == "" {
			fmt.Fprintln(os.Stderr, "读取访问令牌失败（数据库尚未初始化？）:", tokErr)
			_ = application.Shutdown(context.Background())
			os.Exit(1)
		}
		fmt.Println(tok) // 单行输出（含结尾换行），脚本可直接追加到变量
		_ = application.Shutdown(context.Background())
		os.Exit(0)
	}

	if startErr := application.Start(); startErr != nil {
		slog.Error("HTTP 服务启动失败", "err", startErr)
		_ = application.Shutdown(context.Background())
		os.Exit(1)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	exitCode := 0
	select {
	case <-quit:
		slog.Info("收到退出信号，正在关闭")
	case serveErr := <-application.ServeErr():
		slog.Error("HTTP 服务异常退出，正在关闭", "err", serveErr)
		exitCode = 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := application.Shutdown(ctx); err != nil {
		slog.Error("关闭时出现错误", "err", err)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
