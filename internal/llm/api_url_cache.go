package llm

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cata/internal/cata/config"
)

const apiURLCacheFileName = "api_url_resolved.json"

var (
	apiURLCacheMu   sync.Mutex
	apiURLCacheMem  map[string]string
	apiURLCacheOnce sync.Once
)

func apiURLCachePath() string {
	return filepath.Join(config.CataHome(), apiURLCacheFileName)
}

func loadAPIURLCache() map[string]string {
	apiURLCacheOnce.Do(func() {
		apiURLCacheMem = make(map[string]string)
		b, err := os.ReadFile(apiURLCachePath())
		if err != nil {
			return
		}
		_ = json.Unmarshal(b, &apiURLCacheMem)
		if apiURLCacheMem == nil {
			apiURLCacheMem = make(map[string]string)
		}
	})
	return apiURLCacheMem
}

// LookupResolvedAPIURL 返回此前探测成功并记住的完整 endpoint（按配置 URL 键）。
func LookupResolvedAPIURL(configured string) string {
	key := TrimAPIURL(configured)
	if key == "" {
		return ""
	}
	apiURLCacheMu.Lock()
	defer apiURLCacheMu.Unlock()
	return loadAPIURLCache()[key]
}

// RememberResolvedAPIURL 记住配置 URL → 实际可用 endpoint。
func RememberResolvedAPIURL(configured, resolved string) {
	key := TrimAPIURL(configured)
	val := TrimAPIURL(resolved)
	if key == "" || val == "" {
		return
	}
	apiURLCacheMu.Lock()
	defer apiURLCacheMu.Unlock()
	m := loadAPIURLCache()
	if m[key] == val {
		return
	}
	m[key] = val
	_ = os.MkdirAll(config.CataHome(), 0755)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(apiURLCachePath(), b, 0644); err != nil {
		log.Printf("WARNING: failed to persist API URL resolve cache: %v", err)
		return
	}
	log.Printf("LLM: remembered API URL %s → %s", key, val)
}

// looksLikeWrongEndpointPath 路径类错误才值得试备用 URL；地区/鉴权类 400 不重试。
func looksLikeWrongEndpointPath(status int) bool {
	return status == 404 || status == 405 || status == 501
}

// shouldTryAlternateAPIURL 路径错误，或 Responses/Completions 字段模型不匹配时的 400。
func shouldTryAlternateAPIURL(status int, body string) bool {
	if looksLikeWrongEndpointPath(status) {
		return true
	}
	if status != 400 {
		return false
	}
	b := strings.ToLower(body)
	return strings.Contains(b, "max_output_tokens") ||
		strings.Contains(b, "not supported on /v1/responses") ||
		strings.Contains(b, "use 'input'") ||
		strings.Contains(b, `"input"`) && strings.Contains(b, "required") ||
		(strings.Contains(b, "max_tokens") && strings.Contains(b, "responses"))
}
