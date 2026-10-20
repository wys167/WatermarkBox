//go:build windows

package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Windows 版把整个 runtime（magick/gs 及依赖）嵌进 exe，
// 首次运行解压到 %LocalAppData%\WatermarkBox\runtime，实现单文件分发。
//
//go:embed all:embedded-runtime
var embeddedRuntime embed.FS

// runtime 内容变化时递增此版本号，触发重新解压。
const embeddedRuntimeVersion = "1"

// prepareEmbeddedRuntime 返回解压后的 runtime 目录；失败返回空字符串。
func prepareEmbeddedRuntime() string {
	base, err := os.UserCacheDir() // Windows 上即 %LocalAppData%
	if err != nil {
		return ""
	}
	target := filepath.Join(base, "WatermarkBox", "runtime")
	versionFile := filepath.Join(base, "WatermarkBox", "runtime-version.txt")

	if data, err := os.ReadFile(versionFile); err == nil &&
		string(data) == embeddedRuntimeVersion {
		if _, err := os.Stat(filepath.Join(target, "bin", "magick.exe")); err == nil {
			return target
		}
	}

	// 排他锁：防止双开时两个实例同时解压，互相删改对方文件。
	unlock := acquireRuntimeLock(target, 10*time.Minute)
	if unlock == nil {
		return ""
	}
	defer unlock()

	if err := os.RemoveAll(target); err != nil {
		logEvent("error", "embedded_runtime_cleanup_failed", map[string]string{
			"error": err.Error(),
		})
		return ""
	}
	if err := extractEmbedded(embeddedRuntime, "embedded-runtime", target); err != nil {
		logEvent("error", "embedded_runtime_extract_failed", map[string]string{
			"error": err.Error(),
		})
		return ""
	}
	if err := os.WriteFile(versionFile, []byte(embeddedRuntimeVersion), 0o644); err != nil {
		return ""
	}
	return target
}

func extractEmbedded(source embed.FS, root, target string) error {
	return fs.WalkDir(source, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := source.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
}

// embeddedRuntimeHint 供初始化失败时给出可读的错误提示。
const embeddedRuntimeHint = "内置组件解压失败，请删除 %LocalAppData%\\WatermarkBox 后重试"
