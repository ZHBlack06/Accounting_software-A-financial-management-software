package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// ScreenshotStore 截图/导入临时文件目录工具（T08 复用；T03 仅提供目录函数）。
type ScreenshotStore struct {
	dataDir string
}

// NewScreenshotStore 构造。
func NewScreenshotStore(dataDir string) *ScreenshotStore {
	return &ScreenshotStore{dataDir: dataDir}
}

// DatedDir 返回子目录下按日期分片的目录路径（不存在则创建）。
// sub 形如 "screenshots/originals"、"screenshots/compressed"、"ocr-results"。
func (s *ScreenshotStore) DatedDir(sub string, day time.Time) (string, error) {
	dir := filepath.Join(s.dataDir, sub, day.In(timeutil.Local()).Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ImportTmpDir 返回 CSV 导入批次临时目录（不存在则创建）。
func (s *ScreenshotStore) ImportTmpDir() (string, error) {
	dir := filepath.Join(s.dataDir, "tmp", "import")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Path 拼接数据目录内相对路径。
func (s *ScreenshotStore) Path(rel ...string) string {
	parts := append([]string{s.dataDir}, rel...)
	return filepath.Join(parts...)
}

// Abs 把 DB 中存的相对路径解析为绝对路径（转发 OCR 时必须是容器内可读绝对路径）。
// DataDir 为相对路径（默认 ./data）时，必须再经 filepath.Abs 才能得到绝对路径。
func (s *ScreenshotStore) Abs(rel string) string {
	p := rel
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.dataDir, filepath.FromSlash(rel))
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// Save 保存上传图片：分配 19 位 ID、原图+compressed 双份落盘、写入 screenshots 记录。
func (s *ScreenshotStore) Save(ctx context.Context, st *store.Store, repo *store.ScreenshotRepo,
	data []byte, ext string) (*store.Screenshot, error) {
	if ext == "" {
		ext = ".jpg"
	}
	now := time.Now()
	dateStr := now.In(timeutil.Local()).Format("2006-01-02")
	origDir, err := s.DatedDir(filepath.Join("screenshots", "originals"), now)
	if err != nil {
		return nil, err
	}
	compDir, err := s.DatedDir(filepath.Join("screenshots", "compressed"), now)
	if err != nil {
		return nil, err
	}

	ss := &store.Screenshot{}
	err = st.WriteTx(ctx, func(tx *sql.Tx) error {
		id, err := ids.NewTransactionID(tx, now, timeutil.Local())
		if err != nil {
			return err
		}
		ss.ID = id
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(origDir, ss.ID+ext), data, 0o644); err != nil {
		return nil, fmt.Errorf("写入原图失败: %w", err)
	}
	// 基础版不做压缩：字节复制到 compressed/（§11 假设 4）
	if err = os.WriteFile(filepath.Join(compDir, ss.ID+ext), data, 0o644); err != nil {
		return nil, fmt.Errorf("写入压缩副本失败: %w", err)
	}
	ss.OriginalPath = filepath.ToSlash(filepath.Join("screenshots", "originals", dateStr, ss.ID+ext))
	ss.CompressedPath = filepath.ToSlash(filepath.Join("screenshots", "compressed", dateStr, ss.ID+ext))
	ss.OCRStatus = "pending"
	ss.OCRSource = "pc_local"
	ss.FileSize = int64(len(data))
	if err = repo.Insert(ss); err != nil {
		return nil, err
	}
	return ss, nil
}
