package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// macOS 的 ImageMagick 以模块形式构建，coders/*.la 文件内嵌构建机器的绝对路径，
// 应用一旦离开构建目录（拖入 /Applications、从 DMG 运行）模块就加载失败，
// 表现为 "no decode delegate"。因此在首次运行时把整个 runtime 复制到用户缓存目录，
// 就地把 .la 改写为该副本的真实路径，之后始终使用副本。
// 副本同时剥离扩展属性，避开从只读 DMG 运行或 Gatekeeper 转置带来的限制。

// relocateRuntime 把 sourceRoot 复制到缓存目录并修正 .la；失败时原样返回 sourceRoot。
func relocateRuntime(sourceRoot string) string {
	if runtime.GOOS != "darwin" {
		return sourceRoot
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return sourceRoot
	}
	target := filepath.Join(base, "WatermarkBox", "runtime")
	marker := filepath.Join(base, "WatermarkBox", "runtime-source.txt")

	stamp, err := runtimeStamp(sourceRoot)
	if err != nil {
		return sourceRoot
	}
	if data, err := os.ReadFile(marker); err == nil && string(data) == stamp {
		if info, err := os.Stat(filepath.Join(target, "bin", magickBinaryName())); err == nil && !info.IsDir() {
			return target
		}
	}

	logEvent("info", "runtime_relocate_start", map[string]string{
		"source": sourceRoot,
		"target": target,
	})
	if err := os.RemoveAll(target); err != nil {
		logEvent("error", "runtime_relocate_failed", map[string]string{"error": err.Error()})
		return sourceRoot
	}
	if err := copyTree(sourceRoot, target); err != nil {
		logEvent("error", "runtime_relocate_failed", map[string]string{"error": err.Error()})
		return sourceRoot
	}
	if err := rewriteLaFiles(target); err != nil {
		logEvent("error", "runtime_relocate_failed", map[string]string{"error": err.Error()})
		return sourceRoot
	}
	if err := os.WriteFile(marker, []byte(stamp), 0o644); err != nil {
		logEvent("error", "runtime_relocate_failed", map[string]string{"error": err.Error()})
		return sourceRoot
	}
	logEvent("info", "runtime_relocate_done", nil)
	return target
}

// runtimeStamp 用 magick 二进制的大小与修改时间作为内容指纹，
// 应用更新后指纹变化触发重新复制。
func runtimeStamp(root string) (string, error) {
	info, err := os.Stat(filepath.Join(root, "bin", magickBinaryName()))
	if err != nil {
		return "", err
	}
	return root + "\n" + strconv.FormatInt(info.Size(), 10) + "\n" +
		strconv.FormatInt(info.ModTime().UnixNano(), 10), nil
}

func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return copyFile(path, destination, info.Mode().Perm()|0o400)
	})
}

func copyFile(source, destination string, perm os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// rewriteLaFiles 把副本内所有 .la 的 libdir 改写为文件实际所在目录。
func rewriteLaFiles(root string) error {
	modules := filepath.Join(root, "lib", "ImageMagick", "modules-Q16HDRI")
	return filepath.WalkDir(modules, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".la") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		directory := filepath.Dir(path)
		lines := strings.Split(content, "\n")
		for index, line := range lines {
			if strings.HasPrefix(line, "libdir=") {
				lines[index] = "libdir='" + directory + "'"
			}
		}
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	})
}
