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

	"cata/internal/cata/clock"
	"cata/internal/cata/config"
)

// MachineRecord 单台机器的 token 记录（hash 存储，不落明文）。
type MachineRecord struct {
	TokenHash  string `json:"token_hash"`
	CreatedAt  string `json:"created_at,omitempty"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
}

// AgentTokenRecord per-agent token 记录：哈希存储；关联所属机器（吊销/审计用）。
type AgentTokenRecord struct {
	TokenHash  string `json:"token_hash"`
	MachineID  string `json:"machine_id,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
}

// MachinesStore 逐机器 + per-agent token 表（内存 + machines.json 持久化，线程安全）。
type MachinesStore struct {
	mu       sync.Mutex
	path     string
	machines map[string]MachineRecord
	agents   map[string]AgentTokenRecord
}

// MachinesPath 网关侧机器 token 表路径（网关机器自己的 CATA_HOME 下）。
func MachinesPath() string {
	return filepath.Join(config.CataHome(), "machines.json")
}

// NewMachinesStore 加载（或初始化）机器 + per-agent token 表。
func NewMachinesStore(path string) *MachinesStore {
	s := &MachinesStore{path: path, machines: map[string]MachineRecord{}, agents: map[string]AgentTokenRecord{}}
	if data, err := os.ReadFile(path); err == nil {
		var raw struct {
			Machines map[string]MachineRecord    `json:"machines"`
			Agents   map[string]AgentTokenRecord `json:"agents"`
		}
		if json.Unmarshal(data, &raw) == nil {
			if raw.Machines != nil {
				s.machines = raw.Machines
			}
			if raw.Agents != nil {
				s.agents = raw.Agents
			}
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

// ValidateAgent 校验某 agent 的 per-agent token（对 token 取 sha256 后常量时间比较 hash）。
func (s *MachinesStore) ValidateAgent(agentID, token string) bool {
	agentID = strings.TrimSpace(agentID)
	token = strings.TrimSpace(token)
	if agentID == "" || token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.agents[agentID]
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(rec.TokenHash), []byte(hashToken(token))) == 1
}

// IssueAgentToken 为某 agent 首次注册签发 per-agent token（归属 machineID）。
// 返回明文（只此一次），表内只存 hash。已存在的记录不覆盖（保持稳定）。
func (s *MachinesStore) IssueAgentToken(agentID, machineID string) (token string, existing bool, err error) {
	agentID = strings.TrimSpace(agentID)
	machineID = strings.TrimSpace(machineID)
	if agentID == "" {
		return "", false, fmt.Errorf("agent_id required")
	}
	s.mu.Lock()
	if rec, ok := s.agents[agentID]; ok && rec.TokenHash != "" {
		s.mu.Unlock()
		return "", true, nil // 已存在：worker 应带 AgentToken 重连
	}
	tok, err := randomToken()
	if err != nil {
		s.mu.Unlock()
		return "", false, err
	}
	now := clock.RFC3339()
	s.agents[agentID] = AgentTokenRecord{
		TokenHash:  hashToken(tok),
		MachineID:  machineID,
		CreatedAt:  now,
		LastSeenAt: now,
	}
	s.mu.Unlock()
	if err := s.save(); err != nil {
		return "", false, err
	}
	return tok, false, nil
}

// AgentMachine 返回某 per-agent token 归属的机器 id（审计/吊销框架用）；无则空串。
func (s *MachinesStore) AgentMachine(agentID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.agents[agentID]; ok {
		return rec.MachineID
	}
	return ""
}

// RevokeAgent 吊销某 agent 的 per-agent token（该工作空间立即失效，不影响同机其它 agent）。
func (s *MachinesStore) RevokeAgent(agentID string) error {
	agentID = strings.TrimSpace(agentID)
	s.mu.Lock()
	_, ok := s.agents[agentID]
	delete(s.agents, agentID)
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("agent %q not found", agentID)
	}
	return s.save()
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
		Machines map[string]MachineRecord    `json:"machines"`
		Agents   map[string]AgentTokenRecord `json:"agents,omitempty"`
	}{s.machines, s.agents}, "", "  ")
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
