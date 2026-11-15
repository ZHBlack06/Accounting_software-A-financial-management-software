package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	_ "github.com/glebarez/sqlite"
)

func main() {
	// 与后端一致：优先 LEDGER_DATA_DIR，默认 ./data
	dataDir := os.Getenv("LEDGER_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	dbPath := filepath.Join(dataDir, "ledger.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开数据库失败(%s): %v\n", dbPath, err)
		os.Exit(1)
	}
	defer db.Close()
	var tok string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='auth_token'`).Scan(&tok); err != nil {
		fmt.Fprintf(os.Stderr, "读取 auth_token 失败(%s): %v\n（数据库尚未初始化？先启动一次后端）", dbPath, err)
		os.Exit(1)
	}
	fmt.Print(tok)
}
