//go:build !windows

package main

import (
	"os"
	"sync"
	"time"
)

// A subprocess timeout typically kills only the outer logger process. Stop
// the actual device command when its parent disappears instead of letting an
// orphaned screenshot continue writing after the automation run has failed.
func watchExecutionLogParent(parentPID int) func() {
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if os.Getppid() != parentPID {
					// stderr 也可能阻塞，孤儿进程退出不能依赖日志写出。
					once.Do(func() { os.Exit(124) })
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
