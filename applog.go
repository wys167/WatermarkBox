package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// 本地日志：写入用户缓存目录 logs/app-YYYYMMDD.log，保留最近 7 天。
// 上传：仅当环境变量配齐腾讯云 CLS 密钥时启用（见 cls.go），失败静默忽略。

var logger *appLogger

type appLogger struct {
	mu       sync.Mutex
	file     *os.File
	day      string
	uploader *clsUploader
}

func logDirectory() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return "logs"
	}
	return filepath.Join(base, "WatermarkBox", "logs")
}

// initLogger 必须在 initRuntime 之前调用，runtime 初始化过程中的事件才能被记录。
func initLogger() {
	logger = &appLogger{uploader: newClsUploader()}
}

// logEvent 记录一条结构化事件；logger 未初始化时静默丢弃。
func logEvent(level, event string, fields map[string]string) {
	if logger == nil {
		return
	}
	now := time.Now()
	line := now.Format(time.RFC3339) + " " + level + " " + event
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		line += " " + key + "=" + sanitizeLogValue(fields[key])
	}
	logger.writeLine(line)
	logger.uploader.enqueue(clsLog{
		time:   now,
		level:  level,
		event:  event,
		fields: fields,
		line:   line,
	})
}

// sanitizeLogValue 避免含空格的值破坏 k=v 格式。
func sanitizeLogValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\r") {
		return fmt.Sprintf("%q", value)
	}
	return value
}

func (l *appLogger) writeLine(line string) {
	day := time.Now().Format("20060102")
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil || l.day != day {
		if l.file != nil {
			l.file.Close()
		}
		_ = os.MkdirAll(logDirectory(), 0o755)
		file, err := os.OpenFile(filepath.Join(logDirectory(), "app-"+day+".log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			l.file = nil
			return
		}
		l.file = file
		l.day = day
		l.cleanupLocked()
	}
	fmt.Fprintln(l.file, line)
}

// cleanupLocked 删除超过 7 天的旧日志，调用方需持有锁。
func (l *appLogger) cleanupLocked() {
	entries, err := os.ReadDir(logDirectory())
	if err != nil {
		return
	}
	deadline := time.Now().Add(-7 * 24 * time.Hour)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), "app-") && info.ModTime().Before(deadline) {
			_ = os.Remove(filepath.Join(logDirectory(), entry.Name()))
		}
	}
}

// logHostname 每条上传日志附带的机器标识，便于在 CLS 里区分来源。
var logHostname = func() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return runtime.GOOS
	}
	return host
}()
