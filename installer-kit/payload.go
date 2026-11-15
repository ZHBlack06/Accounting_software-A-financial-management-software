package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// payloadZip 是构建时嵌入的应用程序压缩包（内容为相对路径，不含顶层目录）。
//
//go:embed payload/payload.zip
var payloadZip []byte

// payloadMetaJSON 由构建脚本生成，记录体积、文件数、摘要等信息。
//
//go:embed payload/payload.json
var payloadMetaJSON []byte

// uiFS 内嵌安装器前端资源（HTML/CSS/JS）。
//
//go:embed ui
var uiFS embed.FS

// PayloadMeta 描述嵌入载荷的元信息。
type PayloadMeta struct {
	Product          string `json:"product"`
	Version          string `json:"version"`
	Files            int    `json:"files"`
	UncompressedSize int64  `json:"uncompressed_size"`
	CompressedSize   int64  `json:"compressed_size"`
	SHA256           string `json:"sha256"`
	BuiltAt          string `json:"built_at"`
}

// loadPayloadMeta 解析嵌入的 payload.json；解析失败时退化为基于 zip 的估算值。
func loadPayloadMeta() PayloadMeta {
	meta := PayloadMeta{
		Product:        ProductName,
		Version:        ProductVersion,
		CompressedSize: int64(len(payloadZip)),
	}
	if len(payloadMetaJSON) > 0 {
		dec := json.NewDecoder(bytes.NewReader(payloadMetaJSON))
		var parsed PayloadMeta
		if err := dec.Decode(&parsed); err == nil {
			if parsed.Product == "" {
				parsed.Product = ProductName
			}
			if parsed.Version == "" {
				parsed.Version = ProductVersion
			}
			if parsed.CompressedSize == 0 {
				parsed.CompressedSize = int64(len(payloadZip))
			}
			meta = parsed
		}
	}
	if meta.SHA256 == "" && len(payloadZip) > 0 {
		sum := sha256.Sum256(payloadZip)
		meta.SHA256 = hex.EncodeToString(sum[:])
	}
	if meta.UncompressedSize == 0 && len(payloadZip) > 0 {
		if zr, err := newZipReader(); err == nil {
			var total int64
			count := 0
			for _, f := range zr.File {
				if f.FileInfo().IsDir() {
					continue
				}
				total += int64(f.UncompressedSize64)
				count++
			}
			meta.UncompressedSize = total
			meta.Files = count
		}
	}
	return meta
}

// payloadFileNames 返回载荷内的全部文件相对路径（调试与自检使用）。
func payloadFileNames() ([]string, error) {
	zr, err := newZipReader()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names, nil
}

// extractPayload 把内嵌载荷解压到目标目录。
// 返回解压出的文件数量；进度按文件数推进（10% -> 70%）。
func extractPayload(dir string, prog *Progress) (int, error) {
	if len(payloadZip) == 0 {
		return 0, errors.New("安装包损坏：内嵌载荷为空")
	}
	zr, err := newZipReader()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() {
			total++
		}
	}
	if total == 0 {
		return 0, errors.New("安装包损坏：内嵌载荷中没有文件")
	}

	written := 0
	for _, f := range zr.File {
		target, err := safeJoin(dir, f.Name)
		if err != nil {
			return written, err
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := ensureDir(target); err != nil {
				return written, err
			}
			continue
		}
		if err := ensureDir(filepath.Dir(target)); err != nil {
			return written, err
		}
		if err := writeZipEntry(f, target); err != nil {
			return written, fmt.Errorf("解压 %s 失败：%w", f.Name, err)
		}
		written++
		// 10% -> 70% 区间按文件数线性推进。
		prog.SetPercent(10 + int(float64(written)/float64(total)*60))
	}
	return written, nil
}

// InstallInfo 写入安装目录的 install.json 内容。
type InstallInfo struct {
	Product       string `json:"product"`
	Version       string `json:"version"`
	InstallDir    string `json:"install_dir"`
	InstalledAt   string `json:"installed_at"`
	PayloadSHA256 string `json:"payload_sha256"`
	Files         int    `json:"files"`
	// DataDir 是用户数据目录的绝对路径。
	// 仅当数据目录位于安装目录之外（v1.1 及以后）时写入，v1.0 该字段缺省。
	DataDir string `json:"data_dir,omitempty"`
}

// ReadInstallInfo 读取指定目录下的 install.json；不存在或损坏时返回 nil。
func ReadInstallInfo(dir string) *InstallInfo {
	path := filepath.Join(dir, InstallInfoName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var info InstallInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil
	}
	return &info
}
