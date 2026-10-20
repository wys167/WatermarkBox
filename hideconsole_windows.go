//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideConsole 隐藏外部命令（magick/gs 为控制台子系统程序）在 GUI 应用中
// 调用时弹出的黑色控制台窗口。
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
