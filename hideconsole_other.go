//go:build !windows

package main

import "os/exec"

func hideConsole(_ *exec.Cmd) {}
