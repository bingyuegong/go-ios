package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// tunnelPortRegistry 管理 udid -> tunnelInfoPort 的本地持久化映射。
//
// 每台设备使用独立文件 ~/.go-ios-tunnels/<udid>.port，内容为端口号字符串。
// 文件名即 udid，每台设备的操作完全独立，天然无竞争，无需任何锁。
type tunnelPortRegistry struct {
	dir string // 存储目录，如 ~/.go-ios-tunnels
}

var globalTunnelPortRegistry = &tunnelPortRegistry{
	dir: defaultTunnelPortRegistryDir(),
}

func defaultTunnelPortRegistryDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".go-ios-tunnels"
	}
	return filepath.Join(home, ".go-ios-tunnels")
}

// portFilePath 返回指定 udid 的端口文件路径
func (r *tunnelPortRegistry) portFilePath(udid string) string {
	return filepath.Join(r.dir, udid+".port")
}

// ensureDir 确保存储目录存在
func (r *tunnelPortRegistry) ensureDir() error {
	return os.MkdirAll(r.dir, 0755)
}

// Register 注册 udid 对应的 tunnelInfoPort
func (r *tunnelPortRegistry) Register(udid string, port int) error {
	if err := r.ensureDir(); err != nil {
		return fmt.Errorf("Register: mkdir: %w", err)
	}
	data := []byte(strconv.Itoa(port))
	if err := os.WriteFile(r.portFilePath(udid), data, 0644); err != nil {
		return fmt.Errorf("Register: write: %w", err)
	}
	// sudo 环境下将文件 owner 改回实际登录用户
	r.chownToRealUser(r.portFilePath(udid))
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

// chownToRealUser 在 sudo 环境下将文件 owner 改回实际登录用户
func (r *tunnelPortRegistry) chownToRealUser(path string) {
	uidStr := os.Getenv("SUDO_UID")
	if uidStr == "" {
		return
	}
	uid, err := strconv.Atoi(uidStr)
	if err != nil {
		return
	}
	gid := -1
	if gidStr := os.Getenv("SUDO_GID"); gidStr != "" {
		gid, _ = strconv.Atoi(gidStr)
	}
	_ = os.Chown(path, uid, gid)
}
