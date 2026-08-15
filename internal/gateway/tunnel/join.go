// Package tunnel 网关侧 join 流程：机器先「举手」拿一次性 code，管理员在 UI 批准后签发
// 逐机器 token。code 短时有效（10 分钟）、一次性、内存态（网关重启即失效，机器重新 join 即可）。
//
// 状态机：pending → approved（管理员批准，暂存明文 token 供机器轮询领取）→ 过期清除。
// token 明文仅在内存暂存到 code 过期，落盘 machines.json 只存 hash。
package tunnel

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// JoinCodeTTL 一次性 join code 的有效期。
const JoinCodeTTL = 10 * time.Minute

// joinState 单个 join code 的状态。
type joinState struct {
	MachineID string
	ExpiresAt time.Time
	Approved  bool
	Token     string // 批准后暂存明文，供机器轮询领取
}

// JoinManager 管理 join code 的签发与批准（内存态，线程安全）。
type JoinManager struct {
	mu      sync.Mutex
	pending map[string]*joinState
	store   *MachinesStore
}

// NewJoinManager 创建 join 管理器。
func NewJoinManager(store *MachinesStore) *JoinManager {
	return &JoinManager{pending: map[string]*joinState{}, store: store}
}

// RequestJoin 机器举手：登记 machine_id，返回一次性 join code。
func (j *JoinManager) RequestJoin(machineID string) (string, error) {
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return "", fmt.Errorf("machine_id required")
	}
	code, err := randomCode()
	if err != nil {
		return "", err
	}
	j.mu.Lock()
	j.purgeExpiredLocked()
	j.pending[code] = &joinState{MachineID: machineID, ExpiresAt: time.Now().Add(JoinCodeTTL)}
	j.mu.Unlock()
	return code, nil
}

// ApproveJoin 管理员批准：签发逐机器 token，状态改为 approved（暂存 token 供机器领取）。
// 返回 machineID 供确认；重复批准同一 code 幂等（返回同一 token）。
func (j *JoinManager) ApproveJoin(code string) (machineID string, err error) {
	code = strings.TrimSpace(code)
	j.mu.Lock()
	st, ok := j.pending[code]
	if !ok {
		j.mu.Unlock()
		return "", fmt.Errorf("invalid or expired join code")
	}
	if time.Now().After(st.ExpiresAt) {
		delete(j.pending, code)
		j.mu.Unlock()
		return "", fmt.Errorf("join code expired")
	}
	if st.Approved {
		j.mu.Unlock()
		return st.MachineID, nil // 幂等：已批准，返回已签发的 machineID
	}
	machineID = st.MachineID
	j.mu.Unlock()

	token, err := j.store.IssueToken(machineID)
	if err != nil {
		return "", err
	}

	j.mu.Lock()
	if cur, ok := j.pending[code]; ok {
		cur.Approved = true
		cur.Token = token
	}
	j.mu.Unlock()
	return machineID, nil
}

// Status 机器轮询：查询 code 是否已批准，已批准则返回签发的 token。
func (j *JoinManager) Status(code string) (approved bool, token string, err error) {
	code = strings.TrimSpace(code)
	j.mu.Lock()
	defer j.mu.Unlock()
	st, ok := j.pending[code]
	if !ok {
		return false, "", fmt.Errorf("invalid or expired join code")
	}
	if time.Now().After(st.ExpiresAt) {
		delete(j.pending, code)
		return false, "", fmt.Errorf("join code expired")
	}
	return st.Approved, st.Token, nil
}

func (j *JoinManager) purgeExpiredLocked() {
	now := time.Now()
	for code, st := range j.pending {
		if now.After(st.ExpiresAt) {
			delete(j.pending, code)
		}
	}
}

func randomCode() (string, error) {
	b := make([]byte, 6) // 6 字节 → 12 hex 字符，够短又防碰撞
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
