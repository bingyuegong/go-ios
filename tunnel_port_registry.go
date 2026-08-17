package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// tunnelPortRegistry 管理 udid -> tunnelInfoPort 的本地持久化映射。
//
// 每台设备使用独立文件 <tmpdir>/go-ios-tunnels/<udid>.port，内容格式为 "端口:PID"。
// 文件名即 udid，每台设备的操作完全独立，天然无竞争，无需任何锁。
// Lookup 时会校验 PID 是否存活且属于 go-ios 进程，失效则自动删除文件。
type tunnelPortRegistry struct {
	dir string // 存储目录，如 /tmp/go-ios-tunnels
}

var globalTunnelPortRegistry = &tunnelPortRegistry{
	dir: defaultTunnelPortRegistryDir(),
}

func defaultTunnelPortRegistryDir() string {
	if runtime.GOOS == "windows" {
		// Windows 上 os.TempDir() 所有用户一致，直接使用
		return filepath.Join(os.TempDir(), "go-ios-tunnels")
	}
	// macOS/Linux 使用固定路径 /tmp/go-ios-tunnels
	// 避免 macOS 上 os.TempDir() 因用户身份不同（sudo vs 普通用户）返回不同路径
	// sudo 运行时返回 /private/tmp，普通用户返回 /var/folders/...
	return "/tmp/go-ios-tunnels"
}

// portFilePath 返回指定 udid 的端口文件路径
func (r *tunnelPortRegistry) portFilePath(udid string) string {
	return filepath.Join(r.dir, udid+".port")
}

// ensureDir 确保存储目录存在，并设置为全用户可读写（解决 sudo 创建后普通用户无法删除的问题）
func (r *tunnelPortRegistry) ensureDir() error {
	if err := os.MkdirAll(r.dir, 0777); err != nil {
		return err
	}
	// 目录可能已存在但权限不对（如之前由 root 创建），强制修正
	_ = os.Chmod(r.dir, 0777)
	return nil
}

// Register 注册 udid 对应的 tunnelInfoPort，同时记录当前进程 PID。
// 文件内容格式为 "端口:PID"，供 Lookup 做存活校验。
func (r *tunnelPortRegistry) Register(udid string, port int) error {
	if err := r.ensureDir(); err != nil {
		return fmt.Errorf("Register: mkdir: %w", err)
	}
	data := []byte(fmt.Sprintf("%d:%d", port, os.Getpid()))
	if err := os.WriteFile(r.portFilePath(udid), data, 0666); err != nil {
		return fmt.Errorf("Register: write: %w", err)
	}
	return nil
}

// Unregister 删除 udid 的端口记录
func (r *tunnelPortRegistry) Unregister(udid string) error {
	err := os.Remove(r.portFilePath(udid))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Unregister: %w", err)
	}
	return nil
}

// Lookup 查询 udid 对应的 tunnelInfoPort，未找到或记录失效时返回 0, false。
// 失效判定：PID 不存在，或 PID 对应的进程不是 go-ios 相关进程。
// 失效时自动删除端口文件。
func (r *tunnelPortRegistry) Lookup(udid string) (int, bool) {
	filePath := r.portFilePath(udid)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0, false
	}
	port, pid, ok := parsePortPid(strings.TrimSpace(string(data)))
	if !ok {
		// 文件格式无法解析（可能是旧格式），直接删除
		_ = os.Remove(filePath)
		return 0, false
	}
	if !isPidAliveAndGoIos(pid) {
		// PID 已消亡或不属于 go-ios 进程，删除失效文件
		_ = os.Remove(filePath)
		return 0, false
	}
	return port, true
}

// All 返回所有 udid -> port 的映射副本（不做存活校验，仅解析文件内容）
func (r *tunnelPortRegistry) All() map[string]int {
	result := map[string]int{}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return result
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".port") {
			continue
		}
		udid := strings.TrimSuffix(name, ".port")
		data, err := os.ReadFile(filepath.Join(r.dir, name))
		if err != nil {
			continue
		}
		port, _, ok := parsePortPid(strings.TrimSpace(string(data)))
		if !ok {
			continue
		}
		result[udid] = port
	}
	return result
}

// parsePortPid 解析文件内容，支持新格式 "端口:PID" 和旧格式 "端口"。
// 旧格式时 pid 返回 0，ok 返回 false（视为无效，触发删除）。
func parsePortPid(s string) (port int, pid int, ok bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	port, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	pid, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return port, pid, true
}

// isPidAliveAndGoIos 检查指定 PID 的进程是否存活，且进程名包含 "go-ios"。
// 两个条件都满足才返回 true。
func isPidAliveAndGoIos(pid int) bool {
	// 第一步：检查进程是否存活（发送 signal 0，不实际发送信号）
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		// 进程不存在或无权限访问，视为已消亡
		return false
	}

	// 第二步：获取进程名，校验是否属于 go-ios
	procName := getProcName(pid)
	return strings.Contains(procName, "go-ios")
}

// getProcName 获取指定 PID 的进程名（可执行文件名）。
// macOS/Linux 使用 ps 命令，Windows 使用 tasklist 命令。
// 获取失败时返回空字符串。
func getProcName(pid int) string {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// tasklist 输出格式：映像名称  PID  ...
		cmd = exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	} else {
		// ps -p <pid> -o comm= 只输出进程名，无表头
		cmd = exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Clear 清空所有记录
func (r *tunnelPortRegistry) Clear() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("Clear: readdir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".port") {
			_ = os.Remove(filepath.Join(r.dir, entry.Name()))
		}
	}
	return nil
}
