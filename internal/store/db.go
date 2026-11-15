// Package store 数据层：连接管理、迁移、种子、视图与全部 SQL 存取。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Store 聚合 GORM 句柄（模型 CRUD）与底层 *sql.DB（手写热点 SQL / 事务）。
type Store struct {
	G   *gorm.DB
	SQL *sql.DB
	dsn string
}

// DSN 返回数据库文件路径（备份/恢复使用）。
func (s *Store) DSN() string { return s.dsn }

// Open 打开数据库（WAL + 单写连接池）。dsn 为文件路径（不含参数）。
func Open(dsn string) (*Store, error) {
	s, err := openDB(dsn)
	if err != nil {
		return nil, err
	}
	s.dsn = dsn
	return s, nil
}

// Reopen 关闭当前连接并按同一路径重新打开（备份恢复后调用；调用方须保证无并发请求）。
func (s *Store) Reopen() error {
	n, err := openDB(s.dsn)
	if err != nil {
		return err
	}
	old := s.SQL
	s.G, s.SQL = n.G, n.SQL
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func openDB(dsn string) (*Store, error) {
	full := dsn + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(full), &gorm.Config{
		Logger:                 gormlogger.Default.LogMode(gormlogger.Silent),
		NowFunc:                func() time.Time { return time.Now().UTC() },
		SkipDefaultTransaction: true, // 我们自己控事务
	})
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取底层连接失败: %w", err)
	}
	// 纯 Go 驱动 + 单写者，避免 SQLITE_BUSY
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)
	if _, err = sqlDB.Exec(`PRAGMA wal_autocheckpoint=1000;`); err != nil {
		return nil, fmt.Errorf("设置 wal_autocheckpoint 失败: %w", err)
	}
	return &Store{G: db, SQL: sqlDB}, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error {
	if s.SQL != nil {
		return s.SQL.Close()
	}
	return nil
}

// WriteTx 多语句写操作统一入口（§6.1）。
// MaxOpenConns=1 保证串行；_txlock=immediate 等价 BEGIN IMMEDIATE。
// 注意：fn 内必须使用传入的 tx，不得再经 s.G / s.SQL 访问数据库（单连接会自锁）。
func (s *Store) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	conn, err := s.SQL.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
