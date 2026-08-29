// Package tunnel 网关侧 join 流程：机器先「举手」拿一次性 code，管理员在 UI 批准后签发
// 逐机器 token。code 短时有效（10 分钟）、一次性、内存态（网关重启即失效，机器重新 join 即可）。
//
// 状态机：pending → approved（管理员批准，暂存明文 token 供机器轮询领取）→ 过期清除。
// token 明文仅在内存暂存到 code 过期，落盘 machines.json 只存 hash。
//
// 防伪造（挑战-应答）：机器 POST /cata/v1/join/request 前必须先 GET
// /cata/v1/join/challenge 拿到一次性 nonce + HMAC 签名（网关随机 secret），
// request 需回显二者；网关校验签名有效且 nonce 未使用过。即使固定协议头被抓包，
// 没有网关本轮的挑战签名也无法伪造 join；nonce 一次性防重放。
package tunnel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// JoinCodeTTL 一次性 join code 的有效期。
const JoinCodeTTL = 10 * time.Minute

// joinState 单个 join code 的状态。
type joinState struct {
	MachineID  string
	ExpiresAt  time.Time
	Approved   bool
	Token      string // 批准后暂存明文，供机器轮询领取
	ApprovedAt time.Time
}

// JoinManager 管理 join code 的签发与批准（内存态，线程安全）。
type JoinManager struct {
	mu      sync.Mutex
	pending map[string]*joinState
	store   *MachinesStore

	// 挑战-应答防伪造：随机 secret（仅内存）+ 已签发 nonce → 过期时间。
	secret   []byte
	nonces   map[string]time.Time
	nonceTTL time.Duration
}

// NewJoinManager 创建 join 管理器。
func NewJoinManager(store *MachinesStore) *JoinManager {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &JoinManager{
		pending:  map[string]*joinState{},
		store:    store,
		secret:   secret,
		nonces:   map[string]time.Time{},
		nonceTTL: 60 * time.Second,
	}
}

// NewChallenge 签发一次性挑战：返回 nonce 与其 HMAC 签名（网关 secret 派生）。
// 机器在 join/request 回显 nonce+sig；网关校验有效且 nonce 未用过才放行。
func (j *JoinManager) NewChallenge() (challenge, sig string, err error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", "", err
	}
	challenge = hex.EncodeToString(nonce)
	sig = challengeSignature(challenge, j.secret)
	j.mu.Lock()
	j.purgeNoncesLocked()
	j.nonces[challenge] = time.Now().Add(j.nonceTTL)
	j.mu.Unlock()
	return challenge, sig, nil
}

// VerifyChallenge 校验并消费一次性挑战：sig 必须与该 nonce 匹配、未过期、未使用过。
// 校验通过后 nonce 立即失效（一次性），防重放。
func (j *JoinManager) VerifyChallenge(challenge, sig string) error {
	challenge = strings.TrimSpace(challenge)
	sig = strings.TrimSpace(sig)
	if challenge == "" || sig == "" {
		return fmt.Errorf("challenge required")
	}
	if !hmac.Equal([]byte(sig), []byte(challengeSignature(challenge, j.secret))) {
		return fmt.Errorf("invalid challenge signature")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	expires, ok := j.nonces[challenge]
	if !ok {
		return fmt.Errorf("challenge used or unknown")
	}
	if time.Now().After(expires) {
		delete(j.nonces, challenge)
		return fmt.Errorf("challenge expired")
	}
	delete(j.nonces, challenge) // 一次性
	return nil
}

// purgeNoncesLocked 清理过期 nonce（持锁调用）。
func (j *JoinManager) purgeNoncesLocked() {
	now := time.Now()
	for n, exp := range j.nonces {
		if now.After(exp) {
			delete(j.nonces, n)
		}
	}
}

// challengeSignature HMAC-SHA256(nonce, secret) 的 hex。
func challengeSignature(nonce string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(nonce))
	return hex.EncodeToString(mac.Sum(nil))
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
		cur.ApprovedAt = time.Now()
	}
	j.mu.Unlock()
	return machineID, nil
}

// StatusResult 机器轮询的完整状态（含批准时间）。
type StatusResult struct {
	Approved   bool
	Token      string
	ApprovedAt time.Time
}

// Status 机器轮询：查询 code 是否已批准，已批准则返回签发的 token 与其批准时间。
func (j *JoinManager) Status(code string) (StatusResult, error) {
	code = strings.TrimSpace(code)
	j.mu.Lock()
	defer j.mu.Unlock()
	st, ok := j.pending[code]
	if !ok {
		return StatusResult{}, fmt.Errorf("invalid or expired join code")
	}
	if time.Now().After(st.ExpiresAt) {
		delete(j.pending, code)
		return StatusResult{}, fmt.Errorf("join code expired")
	}
	return StatusResult{Approved: st.Approved, Token: st.Token, ApprovedAt: st.ApprovedAt}, nil
}

// PendingJoin 待批准的 join 请求（UI 展示用）。
type PendingJoin struct {
	Code      string    `json:"code"`
	MachineID string    `json:"machine_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Pending 列出所有待批准（未过期、未批准）的 join 请求，旧→新。
// 供 UI 直接展示机器并一键批准，无需人工复制 code。
func (j *JoinManager) Pending() []PendingJoin {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.purgeExpiredLocked()
	var out []PendingJoin
	for code, st := range j.pending {
		if st.Approved {
			continue
		}
		out = append(out, PendingJoin{Code: code, MachineID: st.MachineID, ExpiresAt: st.ExpiresAt})
	}
	sort.Slice(out, func(i, k int) bool {
		return out[i].ExpiresAt.Before(out[k].ExpiresAt)
	})
	return out
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
