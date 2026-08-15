// Package tunnel 网关侧逐机器 token 持久化表（machines.json）。
//
// v2 安全模型：每机器一个独立 token（替代 v1 全网共享 Bearer token）。
// token 只存 sha256 hash（0600 权限），单机泄露可单独吊销，不影响其它机器。
package tunnel

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"cata/internal/cata/config"
	"cata/internal/cata/clock"
)

// MachineRecord 单台机器的 token 记录（hash 存储，不落明文）。
type MachineRecord struct {
	TokenHash  string `json:"token_hash"`
	CreatedAt  string `json:"created_at,omitempty"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
}

// MachinesStore 逐机器 token 表（内存 + machines.json 持久化，线程安全）。
type MachinesStore struct {
	mu       sync.Mutex
	path     string
	machines map[string]MachineRecord
}

// MachinesPath 网关侧机器 token 表路径（网关机器自己的 CATA_HOME 下）。
func MachinesPath() string {
	return filepath.Join(config.CataHome(), "machines.json")
}

// NewMachinesStore 加载（或初始化）机器 token 表。
func NewMachinesStore(path string) *MachinesStore {
	s := &MachinesStore{path: path, machines: map[string]MachineRecord{}}
	if data, err := os.ReadFile(path); err == nil {
		var raw struct {
			Machines map[string]MachineRecord `json:"machines"`
		}
		if json.Unmarshal(data, &raw) == nil && raw.Machines != nil {
			s.machines = raw.Machines
		}
	}
	return s
}

// ValidateMachine 校验某机器的 token 是否匹配（对 token 取 sha256 后常量时间比较 hash）。
func (s *MachinesStore) ValidateMachine(machineID, token string) bool {
	machineID = strings.TrimSpace(machineID)
	token = strings.TrimSpace(token)
	if machineID == "" || token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.machines[machineID]
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(rec.TokenHash), []byte(hashToken(token))) == 1
}

// IssueToken 为某机器签发一个新 token，返回明文（只此一次），表内只存 hash。
// 重复签发会覆盖旧 token（旧 token 立即失效）。
func (s *MachinesStore) IssueToken(machineID string) (string, error) {
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return "", fmt.Errorf("machine_id required")
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.machines[machineID] = MachineRecord{
		TokenHash:  hashToken(token),
		CreatedAt:  clock.RFC3339(),
		LastSeenAt: clock.RFC3339(),
	}
	s.mu.Unlock()
	if err := s.save(); err != nil {
		return "", err
	}
	return token, nil
}

// TouchSeen 更新某机器最近在线时间（校验通过后调用）。
func (s *MachinesStore) TouchSeen(machineID string) {
	s.mu.Lock()
	if rec, ok := s.machines[machineID]; ok {
		rec.LastSeenAt = clock.RFC3339()
		s.machines[machineID] = rec
	}
	s.mu.Unlock()
}

// RevokeMachine 吊销某机器（删除记录；该机器 token 立即失效，不影响其它机器）。
func (s *MachinesStore) RevokeMachine(machineID string) error {
	s.mu.Lock()
	_, ok := s.machines[machineID]
	delete(s.machines, machineID)
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("machine %q not found", machineID)
	}
	return s.save()
}

// ListMachines 返回机器 id 列表（稳定排序，供 UI 展示/吊销）。
func (s *MachinesStore) ListMachines() []string {
	s.mu.Lock()
	out := make([]string, 0, len(s.machines))
	for m := range s.machines {
		out = append(out, m)
	}
	s.mu.Unlock()
	sort.Strings(out)
	return out
}

func (s *MachinesStore) save() error {
	s.mu.Lock()
	data, err := json.MarshalIndent(struct {
		Machines map[string]MachineRecord `json:"machines"`
	}{s.machines}, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// randomToken 生成 32 字节随机 token（hex 编码，64 字符）。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
