package store

import (
	"database/sql"
	"strconv"

	"ledger/internal/pkg/timeutil"
)

// EnsureGlobalSeq 自愈 seq:global 计数器。
//
// 语义：meta['seq:global'] = "下一个待分配的 created_seq"。因此不变式是
//
//	seq:global > max(transactions.created_seq)
//
// 恢复旧备份、外部工具改库、或历史 bug 都可能让计数器落后；此时新建交易会复用
// 已存在的 created_seq，撞上 UNIQUE(created_seq) → 全部 500。这里在启动自检时
// 把它抬到 max(created_seq)+1（只增不减，永不回退）。
//
// 返回 (是否修正, 修正前, 修正后)。
func (s *Store) EnsureGlobalSeq() (bool, int64, int64, error) {
	var cur int64
	err := s.SQL.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'seq:global'`).Scan(&cur)
	switch {
	case err == sql.ErrNoRows:
		cur = 1
	case err != nil:
		return false, 0, 0, err
	}
	var maxSeq int64
	if err = s.SQL.QueryRow(`SELECT COALESCE(MAX(created_seq), 0) FROM transactions`).Scan(&maxSeq); err != nil {
		return false, 0, 0, err
	}
	want := maxSeq + 1
	if cur >= want {
		return false, cur, cur, nil
	}
	if _, err = s.SQL.Exec(`UPDATE meta SET value = ?, updated_at = ? WHERE key = 'seq:global'`,
		strconv.FormatInt(want, 10), timeutil.NowUnix()); err != nil {
		return false, cur, want, err
	}
	return true, cur, want, nil
}
