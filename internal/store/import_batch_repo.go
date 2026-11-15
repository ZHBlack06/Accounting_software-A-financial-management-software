package store

import (
	"database/sql"

	"ledger/internal/pkg/timeutil"
)

// ImportBatchRepo 导入批次审计表存取（v1.2）。
type ImportBatchRepo struct {
	s *Store
}

// NewImportBatchRepo 构造。
func NewImportBatchRepo(s *Store) *ImportBatchRepo { return &ImportBatchRepo{s: s} }

// Create 写入一个新批次（status 默认 previewed）。
func (r *ImportBatchRepo) Create(b *ImportBatch) error {
	now := timeutil.NowUnix()
	if b.ID == "" {
		return sql.ErrNoRows
	}
	if b.Kind == "" {
		b.Kind = "sheet"
	}
	if b.Status == "" {
		b.Status = "previewed"
	}
	if b.CreatedAt == 0 {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
	_, err := r.s.SQL.Exec(`INSERT INTO import_batches
		(id, kind, status, file_count, sheet_count, total_rows, created_rows, skipped_rows,
		 error_rows, encoding, manifest_path, created_at, committed_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.Kind, b.Status, b.FileCount, b.SheetCount, b.TotalRows, b.CreatedRows,
		b.SkippedRows, b.ErrorRows, b.Encoding, b.ManifestPath, b.CreatedAt, b.CommittedAt, b.UpdatedAt)
	return err
}

// Get 按 ID 取批次；不存在返回 (nil, nil)。
func (r *ImportBatchRepo) Get(id string) (*ImportBatch, error) {
	row := r.s.SQL.QueryRow(`SELECT id, kind, status, file_count, sheet_count, total_rows,
		created_rows, skipped_rows, error_rows, encoding, manifest_path, created_at,
		committed_at, updated_at FROM import_batches WHERE id = ?`, id)
	return scanImportBatch(row)
}

// scanImportBatch 单行扫描（兼容 *sql.Row）。
func scanImportBatch(row *sql.Row) (*ImportBatch, error) {
	var b ImportBatch
	err := row.Scan(&b.ID, &b.Kind, &b.Status, &b.FileCount, &b.SheetCount, &b.TotalRows,
		&b.CreatedRows, &b.SkippedRows, &b.ErrorRows, &b.Encoding, &b.ManifestPath,
		&b.CreatedAt, &b.CommittedAt, &b.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// Counts 批次统计增量（preview 阶段回写）。
type BatchCounts struct {
	SheetCount  int
	TotalRows   int64
	CreatedRows int64
	SkippedRows int64
	ErrorRows   int64
}

// UpdateCounts 回写批次统计（preview 后调用，status 不变）。
func (r *ImportBatchRepo) UpdateCounts(id string, c BatchCounts) error {
	_, err := r.s.SQL.Exec(`UPDATE import_batches SET sheet_count = ?, total_rows = ?,
		created_rows = ?, skipped_rows = ?, error_rows = ?, updated_at = ? WHERE id = ?`,
		c.SheetCount, c.TotalRows, c.CreatedRows, c.SkippedRows, c.ErrorRows,
		timeutil.NowUnix(), id)
	return err
}

// MarkCommitted 标记批次已提交（幂等守卫：仅 previewed 可转 committed）。
// 返回受影响行数；0 表示批次状态已不是 previewed（已被提交/中止）。
func (r *ImportBatchRepo) MarkCommitted(id string, created, skipped, errRows int64) (int64, error) {
	now := timeutil.NowUnix()
	res, err := r.s.SQL.Exec(`UPDATE import_batches SET status = 'committed', committed_at = ?,
		created_rows = ?, skipped_rows = ?, error_rows = ?, updated_at = ?
		WHERE id = ? AND status = 'previewed'`, now, created, skipped, errRows, now, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// List 批次列表（按创建时间倒序）。
func (r *ImportBatchRepo) List(limit int) ([]ImportBatch, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := r.s.SQL.Query(`SELECT id, kind, status, file_count, sheet_count, total_rows,
		created_rows, skipped_rows, error_rows, encoding, manifest_path, created_at,
		committed_at, updated_at FROM import_batches ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ImportBatch, 0, limit)
	for rows.Next() {
		var b ImportBatch
		if err = rows.Scan(&b.ID, &b.Kind, &b.Status, &b.FileCount, &b.SheetCount, &b.TotalRows,
			&b.CreatedRows, &b.SkippedRows, &b.ErrorRows, &b.Encoding, &b.ManifestPath,
			&b.CreatedAt, &b.CommittedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
