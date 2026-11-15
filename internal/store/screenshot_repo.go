package store

import (
	"database/sql"

	"ledger/internal/pkg/timeutil"
)

// ScreenshotRepo screenshots 表读写（T08）。
type ScreenshotRepo struct {
	s *Store
}

// NewScreenshotRepo 构造。
func NewScreenshotRepo(s *Store) *ScreenshotRepo { return &ScreenshotRepo{s: s} }

const ssColumns = `id, transaction_id, original_path, compressed_path, ocr_result_path,
	ocr_status, ocr_source, ocr_model, ocr_elapsed_ms, need_review, file_size,
	created_at, updated_at, deleted_at`

func scanScreenshot(sc scanner) (*Screenshot, error) {
	var r Screenshot
	err := sc.Scan(&r.ID, &r.TransactionID, &r.OriginalPath, &r.CompressedPath, &r.OCRResultPath,
		&r.OCRStatus, &r.OCRSource, &r.OCRModel, &r.OCRElapsedMs, &r.NeedReview, &r.FileSize,
		&r.CreatedAt, &r.UpdatedAt, &r.DeletedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Get 按 ID 取截图记录。
func (r *ScreenshotRepo) Get(id string) (*Screenshot, error) {
	s, err := scanScreenshot(r.s.SQL.QueryRow(`SELECT `+ssColumns+` FROM screenshots WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

// Insert 插入截图记录。
func (r *ScreenshotRepo) Insert(ss *Screenshot) error {
	now := timeutil.NowUnix()
	if ss.CreatedAt == 0 {
		ss.CreatedAt = now
	}
	ss.UpdatedAt = now
	if ss.OCRStatus == "" {
		ss.OCRStatus = "pending"
	}
	if ss.OCRSource == "" {
		ss.OCRSource = "pc_local"
	}
	_, err := r.s.SQL.Exec(`INSERT INTO screenshots (`+ssColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ss.ID, ss.TransactionID, ss.OriginalPath, ss.CompressedPath, ss.OCRResultPath,
		ss.OCRStatus, ss.OCRSource, ss.OCRModel, ss.OCRElapsedMs, ss.NeedReview, ss.FileSize,
		ss.CreatedAt, ss.UpdatedAt, ss.DeletedAt)
	return err
}

// UpdateOCR 更新 OCR 相关字段。
func (r *ScreenshotRepo) UpdateOCR(id, status, model, resultPath string, elapsedMs int64, needReview int) error {
	_, err := r.s.SQL.Exec(`UPDATE screenshots SET ocr_status=?, ocr_model=?, ocr_result_path=?,
		ocr_elapsed_ms=?, need_review=?, updated_at=? WHERE id=?`,
		status, model, resultPath, elapsedMs, needReview, timeutil.NowUnix(), id)
	return err
}

// AttachTransaction 截图确认入账后关联交易。
func (r *ScreenshotRepo) AttachTransaction(id, txID string) error {
	_, err := r.s.SQL.Exec(`UPDATE screenshots SET transaction_id=?, updated_at=? WHERE id=?`,
		txID, timeutil.NowUnix(), id)
	return err
}

