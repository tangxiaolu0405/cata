// Package secrets 集中管理运行时已知的敏感值（API key、token、password 等），
// 在写入日志 / 发给 LLM 之前统一掩盖，避免密钥泄露进日志与模型上下文。
//
// 用法：进程启动时 Add() 已知 secret（配置项、link.json 的 machine_token 等），
// 任何要落盘/出站的内容先过 Redact()。
package secrets

import (
	"os"
	"strings"
	"sync"
)

// 掩盖占位符。
const Placeholder = "***REDACTED***"

// Redactor 按已知 secret 值做全文替换。
// 只替换「足够长」的值（>= minLen），避免把单字母/短词误伤。
type Redactor struct {
	mu     sync.RWMutex
	values map[string]struct{}
	minLen int
}

// New 创建 Redactor；minLen<=0 时用默认 8。
func New(minLen int) *Redactor {
	if minLen <= 0 {
		minLen = 8
	}
	return &Redactor{values: map[string]struct{}{}, minLen: minLen}
}

// Add 登记一个 secret 值（自动 trim；短值忽略）。
func (r *Redactor) Add(secret string) {
	secret = strings.TrimSpace(secret)
	if len(secret) < r.minLen {
		return
	}
	r.mu.Lock()
	r.values[secret] = struct{}{}
	r.mu.Unlock()
}

// Clear 清空全部已登记 secret。
func (r *Redactor) Clear() {
	r.mu.Lock()
	r.values = map[string]struct{}{}
	r.mu.Unlock()
}

// Redact 掩盖文本中出现的所有已知 secret。
func (r *Redactor) Redact(s string) string {
	if s == "" {
		return s
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.values) == 0 {
		return s
	}
	for v := range r.values {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, Placeholder)
		}
	}
	return s
}

// Count 当前登记数量（测试/观测用）。
func (r *Redactor) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.values)
}

// CollectFromEnv 收集环境变量中疑似 secret 的值（变量名含 KEY/TOKEN/SECRET/PASSWORD，
// 且排除空值与占位符），返回去重列表。调用方 Add 到 Redactor。
func CollectFromEnv() []string {
	var out []string
	seen := map[string]struct{}{}
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		name := kv[:i]
		val := kv[i+1:]
		if !isSecretEnvName(name) {
			continue
		}
		val = strings.TrimSpace(val)
		if val == "" || val == Placeholder {
			continue
		}
		if _, ok := seen[val]; ok {
			continue
		}
		seen[val] = struct{}{}
		out = append(out, val)
	}
	return out
}

// isSecretEnvName 变量名是否像 secret：含 API_KEY / _TOKEN / _SECRET / PASSWORD / PASSWD。
func isSecretEnvName(name string) bool {
	n := strings.ToUpper(name)
	for _, mark := range []string{"API_KEY", "_TOKEN", "_SECRET", "PASSWORD", "PASSWD"} {
		if strings.Contains(n, mark) {
			return true
		}
	}
	return false
}
