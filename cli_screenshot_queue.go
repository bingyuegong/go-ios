package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// 同一台设备一次只允许一个 screenshot 访问 instruments 服务。并发建立多条连接时，
// 设备会拒绝其中大部分（实测 3 路并发 24 次仅 1 次成功），串行后 9 次全部成功。
//
// 锁由实际执行截图的 child 持有，因此锁的存续恰好等于它访问设备的时间；进程以任何方式
// 结束（正常退出、超时 124、被杀）都由操作系统释放，不需要清理死锁文件。
// 等待只受整体截止时间限制，等不到锁时返回 false（调用方退出 124）。
//
// 内核唤醒等待者的顺序不保证严格先来先到；预算兜住了最坏等待时间。
func acquireScreenshotQueue(dir, key string, deadline time.Time) (release func(), ok bool) {
	noop := func() {}
	if dir == "" || key == "" {
		diagnosticWarn("screenshot queue disabled", "reason", "missing lock directory or device key")
		return noop, true
	}
	sum := sha256.Sum256([]byte(key))
	path := filepath.Join(dir, "go-ios-screenshot-"+hex.EncodeToString(sum[:8])+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// 锁文件不可用时不能让截图整体失败，退回到无队列的旧行为并留下诊断。
		diagnosticWarn("screenshot queue unavailable", "error", err)
		return noop, true
	}
	started := time.Now()
	diagnosticInfo("screenshot queue wait started", "udid", key)
	result := make(chan error, 1)
	// 阻塞式加锁无法被取消；超时后进程会立即退出，由操作系统回收该协程与文件句柄。
	go func() { result <- lockFileExclusive(file) }()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case err := <-result:
		if err != nil {
			_ = file.Close()
			diagnosticWarn("screenshot queue unavailable", "error", err)
			return noop, true
		}
		diagnosticInfo("screenshot queue wait completed", "udid", key, "duration_ms", time.Since(started).Milliseconds())
		return func() {
			_ = unlockFile(file)
			_ = file.Close()
		}, true
	case <-timer.C:
		diagnosticError("screenshot queue wait timed out", "udid", key, "duration_ms", time.Since(started).Milliseconds())
		return noop, false
	}
}
