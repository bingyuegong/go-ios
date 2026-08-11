package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// tunnelPortRegistry 管理 udid -> tunnelInfoPort 的本地持久化映射
type tunnelPortRegistry struct {
	mu   sync.Mutex
	path string
}

// tunnelPortRecord 是注册表文件中的单条记录
type tunnelPortRecord struct {
	// Ports 是 udid -> tunnelInfoPort 的映射
	Ports map[string]int `json:"ports"`
}

var globalTunnelPortRegistry = &tunnelPortRegistry{
	path: defaultTunnelPortRegistryPath(),
}

func defaultTunnelPortRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".go-ios-tunnel-ports.json"
	}
	return filepath.Join(home, ".go-ios-tunnel-ports.json")
}

// load 从文件加载注册表，文件不存在时返回空记录
func (r *tunnelPortRegistry) load() tunnelPortRecord {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return tunnelPortRecord{Ports: map[string]int{}}
	}
	var rec tunnelPortRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return tunnelPortRecord{Ports: map[string]int{}}
	}
	if rec.Ports == nil {
		rec.Ports = map[string]int{}
	}
	return rec
}

// save 将注册表写入文件，并在 sudo 环境下将文件 owner 改回实际登录用户，
// 避免 tunnel start（需要 sudo）写入的文件归 root 所有，导致普通用户无法写入。
func (r *tunnelPortRegistry) save(rec tunnelPortRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.path, data, 0644); err != nil {
		return err
	}
	// 若在 sudo 环境下运行，将文件 owner 改回实际登录用户
	if uidStr := os.Getenv("SUDO_UID"); uidStr != "" {
		uid, uidErr := strconv.Atoi(uidStr)
		gid := -1
		if gidStr := os.Getenv("SUDO_GID"); gidStr != "" {
			gid, _ = strconv.Atoi(gidStr)
		}
		if uidErr == nil {
			_ = os.Chown(r.path, uid, gid)
		}
	}
	return nil
}

// Register 注册 udid 对应的 tunnelInfoPort
func (r *tunnelPortRegistry) Register(udid string, port int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.load()
	rec.Ports[udid] = port
	return r.save(rec)
}

// Unregister 删除 udid 的端口记录
func (r *tunnelPortRegistry) Unregister(udid string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.load()
	delete(rec.Ports, udid)
	return r.save(rec)
}

// Lookup 查询 udid 对应的 tunnelInfoPort，未找到时返回 0, false
func (r *tunnelPortRegistry) Lookup(udid string) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.load()
	port, ok := rec.Ports[udid]
	return port, ok
}

// All 返回所有 udid -> port 的映射副本
func (r *tunnelPortRegistry) All() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.load()
	result := make(map[string]int, len(rec.Ports))
	for k, v := range rec.Ports {
		result[k] = v
	}
	return result
}

// Clear 清空所有记录
func (r *tunnelPortRegistry) Clear() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.save(tunnelPortRecord{Ports: map[string]int{}})
}
