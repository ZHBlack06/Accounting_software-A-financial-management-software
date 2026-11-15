// Package webassets 内嵌前端构建产物（web/dist），供桌面版离线启动使用。
//
// go:embed 只能引用本目录（及其子目录）下的文件，因此该文件必须与 dist 同级；
// 导入方按包名 webassets 引用：webassets "ledger/web"。
package webassets

import "embed"

// Dist 是 web/dist 的只读文件系统副本（含以 . 或 _ 开头的文件，故用 all: 前缀）。
//
//go:embed all:dist
var Dist embed.FS
