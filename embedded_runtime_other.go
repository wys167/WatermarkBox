//go:build !windows

package main

// 非 Windows 平台使用 .app 内的 Resources/runtime，无需内嵌解压。
func prepareEmbeddedRuntime() string { return "" }
