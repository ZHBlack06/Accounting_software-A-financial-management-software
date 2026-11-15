package store

import (
	_ "embed"
	"fmt"
	"log/slog"

	"ledger/internal/config"
	"ledger/internal/pkg/timeutil"
)

//go:embed schema.sql
var schemaSQL string

// SchemaVersion 当前 schema 版本（v1.2 → "2"）。启动时校验该值。
const SchemaVersion = "3"

// Migrate 幂等执行全量 DDL（CREATE IF NOT EXISTS），补齐 v1.2 列补丁与新 meta 键。
//
// v1.2 沿用「幂等」哲学：schema.sql 保证新库直接建好全部表/列，
// ensureV12Columns 负责给老库补列（PRAGMA table_info 判断后 ALTER，幂等）。
// 补列后重新执行一次 schema.sql，让依赖 `SELECT t.*` 的基础视图重新展开（含新列）。
func (s *Store) Migrate() error {
	if _, err := s.SQL.Exec(schemaSQL); err != nil {
		return fmt.Errorf("执行 schema 失败: %w", err)
	}
	added, err := s.ensureV12Columns()
	if err != nil {
		return err
	}
	if added {
		// 视图 v_transactions_signed 使用 t.*，补列后必须重建才能看到新列；
		// 同时补建依赖新列的部分索引（schema.sql 里的 CREATE IF NOT EXISTS 会对
		// 缺列的旧表直接报错，所以只能补列之后再建）。
		if _, err = s.SQL.Exec(schemaSQL); err != nil {
			return fmt.Errorf("补列后重建视图失败: %w", err)
		}
	}
	if _, err = s.SQL.Exec(v9Indexes); err != nil {
		return fmt.Errorf("创建 v1.2 部分索引失败: %w", err)
	}
	if err = s.initMetaDefaults(); err != nil {
		return err
	}
	if err = s.meta().Set("schema_version", SchemaVersion); err != nil {
		return fmt.Errorf("写入 schema_version 失败: %w", err)
	}
	// app_version 必须**显式覆盖**：initMetaDefaults 用 INSERT OR IGNORE，
	// 老库（v1.0/v1.1 建库）里的 app_version 永远是建库时的版本，导致「关于页 /
	// 备份 manifest 显示旧版本」，同样会让用户以为没升级。只在变化时写，避免无谓写盘。
	if cur, gerr := s.meta().Get("app_version"); gerr == nil && cur != config.Version {
		if serr := s.meta().Set("app_version", config.Version); serr != nil {
			return fmt.Errorf("更新 app_version 失败: %w", serr)
		}
		slog.Info("meta.app_version 已更新", "from", cur, "to", config.Version)
	}
	return nil
}

// meta 便捷构造（migrate 阶段其它仓储尚未初始化）。
func (s *Store) meta() *MetaRepo { return NewMetaRepo(s) }

// columnPatch 老库列级补丁。
type columnPatch struct {
	table  string
	column string
	ddl    string
}

// v12Columns v1.2 新增列（与 schema.sql 中对应表的 CREATE 语句保持一致）。
var v12Columns = []columnPatch{
	{"transactions", "time_precision",
		"ALTER TABLE transactions ADD COLUMN time_precision TEXT NOT NULL DEFAULT 'second'"},
	// §9：延迟冻结三列。DEFAULT 1 让全部既有行保持「已冻结」语义（零 UPDATE）。
	{"transactions", "freeze_confirmed",
		"ALTER TABLE transactions ADD COLUMN freeze_confirmed INTEGER NOT NULL DEFAULT 1"},
	{"transactions", "freeze_confirmed_at",
		"ALTER TABLE transactions ADD COLUMN freeze_confirmed_at INTEGER"},
	{"transactions", "import_batch_id",
		"ALTER TABLE transactions ADD COLUMN import_batch_id TEXT NOT NULL DEFAULT ''"},
}

// v9Indexes 依赖新列的部分索引（schema.sql 已含 CREATE IF NOT EXISTS，
// 老库补列后需再跑一次；幂等）。
const v9Indexes = `
CREATE INDEX IF NOT EXISTS ix_tx_unconfirmed ON transactions(transaction_time)
  WHERE freeze_confirmed = 0 AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_tx_import_batch ON transactions(import_batch_id)
  WHERE import_batch_id <> '';`

// ensureV12Columns 逐列判断后 ALTER，返回是否实际补过列。
func (s *Store) ensureV12Columns() (bool, error) {
	added := false
	for _, p := range v12Columns {
		ok, err := s.hasColumn(p.table, p.column)
		if err != nil {
			return added, err
		}
		if ok {
			continue
		}
		if _, err = s.SQL.Exec(p.ddl); err != nil {
			return added, fmt.Errorf("v1.2 补列 %s.%s 失败: %w", p.table, p.column, err)
		}
		added = true
	}
	return added, nil
}

// hasColumn 判断表是否存在指定列（PRAGMA table_info）。
func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.SQL.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("读取表结构失败 %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notNull int
			dflt    any
			pk      int
		)
		if err = rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// initMetaDefaults 初始化 meta 表默认键（INSERT OR IGNORE，幂等）。
// auth_token 在 seed 阶段生成（需要区分"全新库"与"已有库"），这里不覆盖已有值。
func (s *Store) initMetaDefaults() error {
	now := timeutil.NowUnix()
	defaults := map[string]string{
		"schema_version":              SchemaVersion,
		"app_version":                 config.Version,
		"freeze_days":                 "30",
		"last_frozen_at":              "0",
		"last_materialize_at":         "0",
		"seq:global":                  "1",
		"default_txn_type":            "expense",
		"high_amount_threshold_cents": "50000",
		"quick_templates":             "[]",
		"ui_prefs":                    `{"theme":"light","trend_chart_type":"line","pie_chart_type":"doughnut","time_preset":"last_30d"}`,
		"last_backup_at":              "0",
		"last_cleanup_at":             "0",
		"frozen_write_flag":           "0",
		// v1.2 新增：全局时间精度（'second' | 'day'），只影响展示不影响数据
		"time_precision": "second",
		// v1.2 新增：账户合并迁移是否已完成（幂等守卫）
		"account_merge_v12_done": "0",
	}
	for k, v := range defaults {
		if _, err := s.SQL.Exec(
			`INSERT OR IGNORE INTO meta(key,value,updated_at) VALUES(?,?,?)`, k, v, now); err != nil {
			return fmt.Errorf("初始化 meta.%s 失败: %w", k, err)
		}
	}
	return nil
}
