//go:build windows

package main

import (
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

func watchExecutionLogParent(parentPID int) func() {
	parent, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(parentPID))
	if err != nil {
		// 父进程可能在 OpenProcess 前已退出，不能让内部 child 无监测地继续。
		os.Exit(124)
		return func() {}
	}
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		defer windows.CloseHandle(parent)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// Windows preserves a terminated process's parent PID. Wait on
				// the actual process handle rather than relying on Getppid.
				state, waitErr := windows.WaitForSingleObject(parent, 0)
				if waitErr != nil || state == windows.WAIT_OBJECT_0 {
					// 不先同步写 stderr，管道背压不能阻止孤儿进程退出。
					once.Do(func() { os.Exit(124) })
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
