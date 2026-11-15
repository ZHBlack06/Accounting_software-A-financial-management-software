package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
)

// newZipReader 基于内嵌载荷字节流创建 zip 读取器。
func newZipReader() (*zip.Reader, error) {
	return zip.NewReader(bytes.NewReader(payloadZip), int64(len(payloadZip)))
}

// writeZipEntry 把单个 zip 条目落盘为普通文件。
func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copyFile 复制文件并保留权限位。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := ensureDir(filepath.Dir(dst)); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
