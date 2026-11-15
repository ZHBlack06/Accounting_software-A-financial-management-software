package service

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ledger/internal/config"
	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// BackupService 备份/恢复（§6.11、§5.9 端点 61–64）。
type BackupService struct {
	s       *store.Store
	meta    *store.MetaRepo
	balance *BalanceService
	mat     *Materializer
	dataDir string
	dbPath  string
	mu      sync.Mutex
}

// NewBackupService 构造。
func NewBackupService(s *store.Store, meta *store.MetaRepo, balance *BalanceService,
	mat *Materializer, dataDir, dbPath string) *BackupService {
	return &BackupService{s: s, meta: meta, balance: balance, mat: mat, dataDir: dataDir, dbPath: dbPath}
}

// BackupInfo 备份文件信息。
type BackupInfo struct {
	File      string `json:"file"`
	Path      string `json:"path,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt string `json:"created_at"`
	Checksum  string `json:"checksum"`
}

// manifest 备份清单。
type manifest struct {
	AppVersion    string `json:"app_version"`
	SchemaVersion int64  `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
	SHA256        string `json:"sha256"`
	Rows          int64  `json:"rows"`
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Create 创建备份：wal_checkpoint → 快照 → zip(ledger.db + manifest.json)。
func (b *BackupService) Create(ctx context.Context) (*BackupInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.createLocked("")
}

// createLocked 生成备份压缩包；prefix 用于 pre-restore 安全网（直接生成目标名，避免 Windows 重命名锁）。
func (b *BackupService) createLocked(prefix string) (*BackupInfo, error) {
	if _, err := b.s.SQL.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return nil, apierr.Newf(apierr.CodeBackupFailed, "WAL checkpoint 失败: %v", err)
	}
	tmpDir := filepath.Join(b.dataDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	snapshot := filepath.Join(tmpDir, "ledger.db.snapshot")
	if err := copyFile(b.dbPath, snapshot); err != nil {
		return nil, apierr.Newf(apierr.CodeBackupFailed, "数据库快照失败: %v", err)
	}
	defer os.Remove(snapshot)

	dbSHA, err := sha256File(snapshot)
	if err != nil {
		return nil, err
	}
	var rows int64
	if err := b.s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions`).Scan(&rows); err != nil {
		return nil, err
	}
	schemaVersion, _ := strconv.ParseInt(mustMeta(b.meta, "schema_version", "1"), 10, 64)
	now := time.Now()
	man := manifest{
		AppVersion:    config.Version,
		SchemaVersion: schemaVersion,
		CreatedAt:     timeutil.FormatLocal(now.Unix()),
		SHA256:        dbSHA,
		Rows:          rows,
	}
	dateDir := filepath.Join(b.dataDir, "backups", now.In(timeutil.Local()).Format("2006-01-02"))
	if err = os.MkdirAll(dateDir, 0o755); err != nil {
		return nil, err
	}
	name := prefix + "ledger-" + now.In(timeutil.Local()).Format("20060102-1504") + ".zip"
	dst := filepath.Join(dateDir, name)
	if err = writeBackupZip(dst, snapshot, man); err != nil {
		return nil, apierr.Newf(apierr.CodeBackupFailed, "打包失败: %v", err)
	}
	zipSHA, err := sha256File(dst)
	if err != nil {
		return nil, err
	}
	info, _ := os.Stat(dst)
	_ = b.meta.Set("last_backup_at", strconv.FormatInt(now.Unix(), 10))
	rel, _ := filepath.Rel(filepath.Join(b.dataDir, "backups"), dst)
	return &BackupInfo{
		File: filepath.ToSlash(rel), Path: dst, SizeBytes: info.Size(),
		CreatedAt: man.CreatedAt, Checksum: "sha256:" + zipSHA,
	}, nil
}

func mustMeta(m *store.MetaRepo, key, def string) string {
	v, err := m.Get(key)
	if err != nil || v == "" {
		return def
	}
	return v
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func writeBackupZip(dst, dbFile string, man manifest) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create("ledger.db")
	if err != nil {
		return err
	}
	src, err := os.Open(dbFile)
	if err != nil {
		return err
	}
	defer src.Close()
	if _, err = io.Copy(w, src); err != nil {
		return err
	}
	mw, err := zw.Create("manifest.json")
	if err != nil {
		return err
	}
	buf, _ := json.MarshalIndent(man, "", "  ")
	if _, err = mw.Write(buf); err != nil {
		return err
	}
	return zw.Close()
}

// List 备份列表（按创建时间倒序）。
func (b *BackupService) List() ([]BackupInfo, error) {
	root := filepath.Join(b.dataDir, "backups")
	var out []BackupInfo
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".zip") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		checksum, _ := sha256File(path)
		out = append(out, BackupInfo{
			File: filepath.ToSlash(rel), SizeBytes: info.Size(),
			CreatedAt: timeutil.FormatLocal(info.ModTime().Unix()),
			Checksum:  "sha256:" + checksum,
		})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if out == nil {
		out = []BackupInfo{}
	}
	return out, nil
}

// resolve 备份文件定位（支持 "date/name.zip" 或 "name.zip"）。
func (b *BackupService) resolve(file string) (string, error) {
	root := filepath.Join(b.dataDir, "backups")
	clean := filepath.Clean(filepath.FromSlash(file))
	if strings.Contains(clean, "..") {
		return "", apierr.New(apierr.CodeValidation, "非法的备份文件名")
	}
	candidate := filepath.Join(root, clean)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// 仅文件名 → 全局查找最新匹配
	var found string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() == filepath.Base(clean) {
			found = path
		}
		return nil
	})
	if found == "" {
		return "", apierr.New(apierr.CodeNotFound, "备份文件不存在")
	}
	return found, nil
}

// RestoreResult 恢复结果。
type RestoreResult struct {
	Restored   bool          `json:"restored"`
	From       string        `json:"from"`
	Verify     *VerifyResult `json:"verify"`
	PreRestore string        `json:"pre_restore"`
}

// Restore 恢复备份：校验 → 安全网备份 → 关闭连接 → 覆盖 → 重开 → 全量重物化 + 校验。
// 调用方（handler）负责在此期间置 503 RESTORING。
func (b *BackupService) Restore(ctx context.Context, file string) (*RestoreResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	src, err := b.resolve(file)
	if err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, apierr.Newf(apierr.CodeBackupFailed, "备份包损坏: %v", err)
	}
	defer zr.Close()

	var man manifest
	hasDB := false
	for _, zf := range zr.File {
		switch zf.Name {
		case "manifest.json":
			rc, err := zf.Open()
			if err != nil {
				return nil, err
			}
			err = json.NewDecoder(rc).Decode(&man)
			rc.Close()
			if err != nil {
				return nil, apierr.New(apierr.CodeBackupFailed, "manifest.json 解析失败")
			}
		case "ledger.db":
			hasDB = true
		}
	}
	if !hasDB || man.SHA256 == "" {
		return nil, apierr.New(apierr.CodeBackupFailed, "备份包缺少 ledger.db 或 manifest")
	}

	// 安全网：先备份当前库（直接生成 pre-restore-*.zip）
	pre, err := b.createLocked("pre-restore-")
	if err != nil {
		return nil, apierr.Newf(apierr.CodeBackupFailed, "恢复前备份当前库失败: %v", err)
	}

	// 解压到临时文件并校验 sha256
	tmp := filepath.Join(b.dataDir, "tmp", "restore.db")
	if err := extractZipEntry(zr, "ledger.db", tmp); err != nil {
		return nil, apierr.Newf(apierr.CodeBackupFailed, "解压失败: %v", err)
	}
	gotSHA, err := sha256File(tmp)
	if err != nil {
		return nil, err
	}
	if gotSHA != man.SHA256 {
		os.Remove(tmp)
		return nil, apierr.New(apierr.CodeBackupFailed, "备份包校验失败（sha256 不一致）")
	}

	// 关闭连接 → 覆盖 → 清理 WAL → 重开
	_ = b.s.Close()
	if err := copyFile(tmp, b.dbPath); err != nil {
		os.Remove(tmp)
		return nil, apierr.Newf(apierr.CodeBackupFailed, "覆盖数据库失败: %v", err)
	}
	os.Remove(tmp)
	os.Remove(b.dbPath + "-wal")
	os.Remove(b.dbPath + "-shm")
	if err := b.s.Reopen(); err != nil {
		return nil, apierr.Newf(apierr.CodeBackupFailed, "重新打开数据库失败: %v", err)
	}
	if err := b.s.Migrate(); err != nil {
		return nil, err
	}
	// 恢复旧备份后计数器可能落后于 max(created_seq) → 自愈，否则新建交易会撞唯一约束。
	if fixed, from, seqTo, serr := b.s.EnsureGlobalSeq(); serr != nil {
		slog.Warn("恢复后检查 seq:global 失败", "err", serr)
	} else if fixed {
		slog.Warn("恢复后已修正 seq:global", "from", from, "to", seqTo)
	}
	if _, err := b.mat.ForceFull(ctx, time.Now()); err != nil {
		return nil, err
	}
	verify, err := b.balance.Verify()
	if err != nil {
		return nil, err
	}
	return &RestoreResult{Restored: true, From: filepath.Base(src), Verify: verify,
		PreRestore: pre.File}, nil
}

// extractZipEntry 解压指定条目。
func extractZipEntry(zr *zip.ReadCloser, name, dst string) error {
	for _, zf := range zr.File {
		if zf.Name != name {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, rc)
		return err
	}
	return fmt.Errorf("备份包缺少 %s", name)
}

// Delete 删除备份文件。
func (b *BackupService) Delete(file string) error {
	path, err := b.resolve(file)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// EnsureWeekly 每周一检测：本周未备份则创建（§6.12 步骤 5）。
func (b *BackupService) EnsureWeekly(ctx context.Context) {
	now := time.Now().In(timeutil.Local())
	if now.Weekday() != time.Monday {
		return
	}
	last, _ := b.meta.Get("last_backup_at")
	lastUnix, _ := strconv.ParseInt(last, 10, 64)
	weekStart := timeutil.WeekStart(now.Unix())
	if lastUnix >= weekStart {
		return
	}
	if _, err := b.Create(ctx); err != nil {
		// 失败仅记录（由调用方 slog）
		_ = err
	}
}
