package store

import (
	"database/sql"

	"ledger/internal/pkg/timeutil"
)

// MetaRepo meta 表读写（含事务内变体，供物化/ID 分配复用）。
type MetaRepo struct {
	s *Store
}

// NewMetaRepo 构造。
func NewMetaRepo(s *Store) *MetaRepo { return &MetaRepo{s: s} }

// Get 读取 key；不存在返回 ("", nil)。
func (r *MetaRepo) Get(key string) (string, error) {
	var v string
	err := r.s.SQL.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// GetTx 事务内读取。
func (r *MetaRepo) GetTx(tx *sql.Tx, key string) (string, error) {
	var v string
	err := tx.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// Set 写入（upsert）。
func (r *MetaRepo) Set(key, value string) error {
	_, err := r.s.SQL.Exec(
		`INSERT INTO meta(key,value,updated_at) VALUES(?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, timeutil.NowUnix())
	return err
}

// SetTx 事务内写入（upsert）。
func (r *MetaRepo) SetTx(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(
		`INSERT INTO meta(key,value,updated_at) VALUES(?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, timeutil.NowUnix())
	return err
}

// All 读取全部键值。
func (r *MetaRepo) All() (map[string]string, error) {
	rows, err := r.s.SQL.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
