package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// tunnelPortRegistry 管理 udid -> tunnelInfoPort 的本地持久化映射。
//
// 每台设备使用独立文件 <tmpdir>/go-ios-tunnels/<udid>.port，内容为端口号字符串。
// 文件名即 udid，每台设备的操作完全独立，天然无竞争，无需任何锁。
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

// Register 注册 udid 对应的 tunnelInfoPort
func (r *tunnelPortRegistry) Register(udid string, port int) error {
	if err := r.ensureDir(); err != nil {
		return fmt.Errorf("Register: mkdir: %w", err)
	}
	data := []byte(strconv.Itoa(port))
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

// Lookup 查询 udid 对应的 tunnelInfoPort，未找到时返回 0, false
func (r *tunnelPortRegistry) Lookup(udid string) (int, bool) {
	data, err := os.ReadFile(r.portFilePath(udid))
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return port, true
}

// All 返回所有 udid -> port 的映射副本
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
		port, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}
		result[udid] = port
	}
	return result
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
