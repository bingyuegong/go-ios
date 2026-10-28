package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExecutionLogKey(t *testing.T) {
	cases := []struct {
		args []string
		env  string
		want string
	}{
		{[]string{"screenshot", "-u", "00008110-0018541A1485401E"}, "", "00008110-0018541A1485401E"},
		{[]string{"-u=abc", "info"}, "env-udid", "abc"},
		{[]string{"info"}, "env-udid", "env-udid"},
		{[]string{"version"}, "", executionLogDefaultKey},
		{[]string{"-u", "../../etc/x y"}, "", ".._.._etc_x_y"},
		{[]string{"--", "-u", "late"}, "", executionLogDefaultKey},
	}
	for _, c := range cases {
		if got := executionLogKey(c.args, c.env); got != c.want {
			t.Errorf("executionLogKey(%q, %q) = %q, want %q", c.args, c.env, got, c.want)
		}
	}
}

func TestExecutionLogFileAppendsPerDeviceAndDay(t *testing.T) {
	dir := t.TempDir()
	day := time.Now().Format("20060102")
	// 两个 writer 模拟同一设备的两个并发进程，共享同一个按天文件。
	a, b := newExecutionLogFile(dir, "dev1"), newExecutionLogFile(dir, "dev1")
	var wg sync.WaitGroup
	for _, w := range []*executionLogFile{a, b} {
		wg.Add(1)
		go func(w *executionLogFile) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = w.Write([]byte("record line\n"))
			}
		}(w)
	}
	wg.Wait()
	a.closeLocked()
	b.closeLocked()
	content, err := os.ReadFile(filepath.Join(dir, "go-ios-dev1-"+day+".log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(content), "record line\n"); got != 400 {
		t.Fatalf("records = %d, want 400", got)
	}
}

func TestExecutionLogFileSkipsFullSegment(t *testing.T) {
	dir := t.TempDir()
	day := time.Now().Format("20060102")
	full := filepath.Join(dir, "go-ios-dev2-"+day+".log")
	f, err := os.Create(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(executionLogMaxSize); err != nil { // 稀疏文件，不真正占用 64MB
		t.Fatal(err)
	}
	_ = f.Close()
	w := newExecutionLogFile(dir, "dev2")
	if _, err := w.Write([]byte("next\n")); err != nil {
		t.Fatal(err)
	}
	w.closeLocked()
	content, err := os.ReadFile(filepath.Join(dir, "go-ios-dev2-"+day+"-1.log"))
	if err != nil || string(content) != "next\n" {
		t.Fatalf("second segment = %q, %v", content, err)
	}
}

func TestCleanupExecutionLogsRemovesOnlyExpiredOwnFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-executionLogLifetime - time.Hour)
	files := map[string]bool{ // name -> 应被删除
		"go-ios-dev1-20261020.log":                                   true,
		"go-ios-dev1-20261020-2.log":                                 true,
		"go-ios-20261020-123-0123456789abcdef-diagnostic-000001.log": true,
		"go-ios-dev1-20261028.log":                                   false, // 未过期
		"other-old.log":                                              false, // 不是本功能生成
		"go-ios-screenshot-0123456789abcdef.lock":                    false,
	}
	for name, expired := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		mtime := now
		if expired || name == "other-old.log" || strings.HasSuffix(name, ".lock") {
			mtime = old
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	cleanupExecutionLogs(dir, now)
	for name, expired := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		if expired && err == nil {
			t.Errorf("%s should be removed", name)
		}
		if !expired && err != nil {
			t.Errorf("%s should be kept: %v", name, err)
		}
	}
}

func TestMaybeCleanupExecutionLogsIsThrottled(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	maybeCleanupExecutionLogs(dir, now)
	info, err := os.Stat(filepath.Join(dir, executionLogCleanupStamp))
	if err != nil {
		t.Fatal(err)
	}
	first := info.ModTime()
	// 1 小时内再次调用不刷新时间戳（即不会再次扫描）。
	maybeCleanupExecutionLogs(dir, now.Add(30*time.Minute))
	info, _ = os.Stat(filepath.Join(dir, executionLogCleanupStamp))
	if !info.ModTime().Equal(first) {
		t.Fatalf("stamp refreshed within interval")
	}
	later := now.Add(2 * time.Hour)
	maybeCleanupExecutionLogs(dir, later)
	info, _ = os.Stat(filepath.Join(dir, executionLogCleanupStamp))
	if info.ModTime().Before(later.Add(-time.Second)) {
		t.Fatalf("stamp not refreshed after interval: %v", info.ModTime())
	}
}
