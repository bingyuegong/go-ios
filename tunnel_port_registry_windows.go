//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// isPidAlive 检查指定 PID 的进程在 Windows 上是否存活。
// Windows 不支持 Signal(0)，改用 OpenProcess + GetExitCodeProcess。
// exitCode == 259 (STILL_ACTIVE) 表示进程仍在运行。
func isPidAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// 进程不存在或无权限访问，视为已消亡
		return false
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	// 259 = STILL_ACTIVE，表示进程仍在运行
	return exitCode == 259
}
