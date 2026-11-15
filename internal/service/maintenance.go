package service

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// CleanupResult 清理统计。
type CleanupResult struct {
	ScreenshotOriginals int `json:"screenshot_originals_dirs"`
	ScreenshotCompressed int `json:"screenshot_compressed_dirs"`
	OCRResults          int `json:"ocr_results_dirs"`
	Backups             int `json:"backups_dirs"`
	ImportTmp           int `json:"import_tmp_files"`
}

// MaintenanceService 截图/备份/导入临时文件清理（§9.4 保留期）。
type MaintenanceService struct {
	dataDir string
	s       *store.Store
}

// NewMaintenanceService 构造。
func NewMaintenanceService(dataDir string, s *store.Store) *MaintenanceService {
	return &MaintenanceService{dataDir: dataDir, s: s}
}

// removeDatedDirs 删除 sub 下日期目录中早于 retentionDays 的目录，返回删除数。
func (m *MaintenanceService) removeDatedDirs(sub string, retentionDays int, now time.Time) (int, error) {
	root := filepath.Join(m.dataDir, sub)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := now.In(timeutil.Local()).AddDate(0, 0, -retentionDays)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", e.Name(), timeutil.Local())
		if err != nil {
			continue // 非日期目录不动
		}
		if day.Before(cutoff) {
			if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

// Cleanup 全量清理（§9.4）：原图 30 天、压缩图 150 天、OCR 结果 30 天、备份 150 天、导入临时 24h。
func (m *MaintenanceService) Cleanup(ctx context.Context, now time.Time) (*CleanupResult, error) {
	res := &CleanupResult{}
	var err error
	if res.ScreenshotOriginals, err = m.removeDatedDirs(filepath.Join("screenshots", "originals"), 30, now); err != nil {
		return res, err
	}
	if res.ScreenshotCompressed, err = m.removeDatedDirs(filepath.Join("screenshots", "compressed"), 150, now); err != nil {
		return res, err
	}
	if res.OCRResults, err = m.removeDatedDirs("ocr-results", 30, now); err != nil {
		return res, err
	}
	if res.Backups, err = m.removeDatedDirs("backups", 150, now); err != nil {
		return res, err
	}
	// 导入临时文件：mtime > 24h 删除
	tmpDir := filepath.Join(m.dataDir, "tmp", "import")
	if entries, err := os.ReadDir(tmpDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if now.Sub(info.ModTime()) > 24*time.Hour {
				if err := os.Remove(filepath.Join(tmpDir, e.Name())); err == nil {
					res.ImportTmp++
				}
			}
		}
	}
	// 记录清理时间
	_ = store.NewMetaRepo(m.s).Set("last_cleanup_at",
		strconv.FormatInt(now.Unix(), 10))
	return res, ctx.Err()
}
