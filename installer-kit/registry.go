package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/registry"
)

// joinPath 是 filepath.Join 的薄封装，便于在少量位置统一处理。
func joinPath(elem ...string) string {
	return filepath.Join(elem...)
}

// dirOfPath 返回文件所在目录。
func dirOfPath(p string) string {
	return filepath.Dir(p)
}

// removePath 删除文件或目录（目录递归删除）。
func removePath(p string) error {
	if p == "" {
		return nil
	}
	return os.RemoveAll(p)
}

// WriteUninstallEntry 在 HKCU 的卸载信息位置写入「程序和功能」条目。
// 使用 golang.org/x/sys/windows/registry（纯 Go），不依赖 reg.exe。
func WriteUninstallEntry(installDir string, meta PayloadMeta, prog *Progress) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, UninstallRegPath, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("创建卸载注册表项失败：%w", err)
	}
	defer key.Close()

	exePath := joinPath(installDir, AppExeName)
	uninstaller := joinPath(installDir, UninstallExeName)

	stringValues := []struct {
		name  string
		value string
	}{
		{"DisplayName", UninstallRegDisplayName},
		{"DisplayVersion", ProductVersion},
		{"Publisher", Publisher},
		{"InstallLocation", installDir},
		{"DisplayIcon", exePath + ",0"},
		{"UninstallString", `"` + uninstaller + `" --uninstall`},
		{"QuietUninstallString", `"` + uninstaller + `" --uninstall --silent`},
		{"InstallDate", time.Now().Format("20060102")},
		{"Comments", ProductName + " " + DisplayVersion + " 本地记账工具"},
		{"URLInfoAbout", "http://127.0.0.1:8770"},
	}
	for _, kv := range stringValues {
		if err := key.SetStringValue(kv.name, kv.value); err != nil {
			return fmt.Errorf("写入注册表值 %s 失败：%w", kv.name, err)
		}
	}

	sizeKB := meta.UncompressedSize / 1024
	if sizeKB <= 0 {
		sizeKB = 1
	}
	if sizeKB > 0xFFFFFFFF {
		sizeKB = 0xFFFFFFFF
	}
	dwordValues := []struct {
		name  string
		value uint32
	}{
		{"NoModify", 1},
		{"NoRepair", 1},
		{"EstimatedSize", uint32(sizeKB)},
		{"SystemComponent", 0},
	}
	for _, kv := range dwordValues {
		if err := key.SetDWordValue(kv.name, kv.value); err != nil {
			return fmt.Errorf("写入注册表值 %s 失败：%w", kv.name, err)
		}
	}

	prog.Log(LevelInfo, `已注册卸载信息：HKCU\%s（EstimatedSize=%d KB）`, UninstallRegPath, sizeKB)
	return nil
}

// RemoveUninstallEntry 删除 HKCU 下的卸载信息注册表项（含子键）。
//
// 完全使用 Go 原生 registry API（不调用 reg.exe / PowerShell），并在删除后**校验确实消失**：
// 「调用成功但键仍在」会让用户在「程序和功能」里看到已卸载的应用，必须能被发现。
func RemoveUninstallEntry(prog *Progress) error {
	if err := deleteUninstallKey(); err != nil {
		return err
	}
	if UninstallEntryExists() {
		// 可能是句柄/权限的瞬时问题，短暂重试一次。
		time.Sleep(200 * time.Millisecond)
		if err := deleteUninstallKey(); err != nil {
			return err
		}
		if UninstallEntryExists() {
			return fmt.Errorf(`卸载注册表项仍存在：HKCU\%s`, UninstallRegPath)
		}
	}
	prog.Log(LevelInfo, `已删除卸载注册表项：HKCU\%s`, UninstallRegPath)
	return nil
}

// deleteUninstallKey 执行一次删除；键不存在视为成功（幂等）。
func deleteUninstallKey() error {
	err := registry.DeleteKey(registry.CURRENT_USER, UninstallRegPath)
	if err == nil || errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	// 存在子键时无法直接删除，先递归清空子键再重试。
	if subErr := deleteSubKeys(registry.CURRENT_USER, UninstallRegPath); subErr != nil {
		return fmt.Errorf("删除卸载注册表项失败：%w", err)
	}
	if err = registry.DeleteKey(registry.CURRENT_USER, UninstallRegPath); err != nil &&
		!errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("删除卸载注册表项失败：%w", err)
	}
	return nil
}

// deleteSubKeys 递归删除指定键下的所有子键。
func deleteSubKeys(root registry.Key, path string) error {
	key, err := registry.OpenKey(root, path, registry.READ)
	if err != nil {
		return err
	}
	names, err := key.ReadSubKeyNames(-1)
	key.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		child := path + `\` + name
		if err := deleteSubKeys(root, child); err != nil {
			return err
		}
		if err := registry.DeleteKey(root, child); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	return nil
}

// UninstallEntryExists 判断卸载注册表项是否存在。
func UninstallEntryExists() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, UninstallRegPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	return true
}
