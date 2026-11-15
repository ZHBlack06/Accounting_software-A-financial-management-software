package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// assetsDirName 是数据目录下存放解压后前端资源的子目录名。
const assetsDirName = "web-assets"

// readyMarkerSuffix 是"该版本目录已完整解压"的标记文件后缀。
//
// 之所以不用"临时目录 + rename"的常见做法：Windows 上对目录做 rename 时，
// 若杀毒软件/索引服务正持有目录内某个新写入文件的句柄，会返回 Access denied
// （v1.1 联调实测：同一份资源第一次能改名成功、换个数据目录就失败）。
// 改为"直接在目标目录落盘 + 最后写标记文件"，标记存在才认为该版本可用。
const readyMarkerSuffix = ".ready"

// contentHash 计算前缀下全部文件（含路径名）的 SHA-256，作为资源版本标识。
// 路径与内容一起参与哈希，保证文件增删改名都会得到新版本目录。
func contentHash(fsys fs.FS, prefix string) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, prefix, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			_, _ = io.WriteString(h, "D:"+p+"\n")
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("读取内嵌资源 %s 失败: %w", p, err)
		}
		_, _ = io.WriteString(h, "F:"+p+"\n")
		_, _ = h.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ExtractAssets 把内嵌前端资源解压到 <dataRoot>/web-assets/v<hash>/ 并返回该目录。
// 该版本已完整解压（标记文件存在）时直接复用，不重复覆盖。
func ExtractAssets(fsys fs.FS, prefix, dataRoot string) (string, error) {
	hash, err := contentHash(fsys, prefix)
	if err != nil {
		return "", err
	}
	version := "v" + hash[:16]
	root := filepath.Join(dataRoot, assetsDirName)
	target := filepath.Join(root, version)
	marker := filepath.Join(root, version+readyMarkerSuffix)

	if isReadyDir(target, marker) {
		slog.Info("复用已解压的前端资源", "dir", target, "hash", version)
		return target, nil
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("创建资源目录 %s 失败: %w", target, err)
	}
	if err := extractTree(fsys, prefix, target); err != nil {
		// 失败时清掉半成品，避免下次启动误用不完整资源。
		_ = os.RemoveAll(target)
		return "", err
	}
	if err := os.WriteFile(marker, []byte(version), 0o644); err != nil {
		return "", fmt.Errorf("写入资源标记 %s 失败: %w", marker, err)
	}
	slog.Info("前端资源已解压", "dir", target, "hash", version)
	return target, nil
}

// isReadyDir 判断版本目录与其标记文件是否同时存在。
func isReadyDir(target, marker string) bool {
	if st, err := os.Stat(marker); err != nil || st.IsDir() {
		return false
	}
	st, err := os.Stat(target)
	return err == nil && st.IsDir()
}

// extractTree 把 fsys 中 prefix 下的内容写到 dst（覆盖同名文件）。
func extractTree(fsys fs.FS, prefix, dst string) error {
	return fs.WalkDir(fsys, prefix, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(prefix, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// 内嵌路径始终使用 "/"，转成系统分隔符后落盘。
		target := filepath.Join(dst, filepath.FromSlash(path.Clean(rel)))
		if !filepath.IsLocal(filepath.FromSlash(path.Clean(rel))) {
			return fmt.Errorf("内嵌资源路径非法: %s", p)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("读取内嵌资源 %s 失败: %w", p, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", target, err)
		}
		return nil
	})
}

// CleanupOldAssets 删除除 keepDir 之外的旧版本资源目录与对应标记（尽力而为，失败只记日志）。
func CleanupOldAssets(dataRoot, keepDir string) {
	root := filepath.Join(dataRoot, assetsDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	keep := filepath.Base(keepDir)
	for _, e := range entries {
		if e.Name() == keep || strings.HasPrefix(e.Name(), keep) || strings.HasPrefix(e.Name(), ".extract-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			slog.Warn("清理旧前端资源失败", "name", e.Name(), "err", err)
		}
	}
}
