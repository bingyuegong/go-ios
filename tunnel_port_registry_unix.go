//go:build !windows

package main

import (
	"os"
	"syscall"
)

// isPidAlive 检查指定 PID 的进程在 Unix/macOS 上是否存活。
// 发送 signal(0) 不会实际发送信号，仅用于探测进程是否存在。
func isPidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// signal(0) 不发送实际信号，仅检查进程是否存在且有权限访问
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		// 进程不存在或无权限访问，视为已消亡
		return false
	}
	return true
}
