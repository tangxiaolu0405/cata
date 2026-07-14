package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	tokenURL           = "https://bots.qq.com/app/getAppAccessToken"
	tokenRefreshMargin = 5 * time.Minute
)

// TokenSource 管理 QQBot access_token。
type TokenSource struct {
	appID     string
	appSecret string
	client    *http.Client

	mu      sync.RWMutex
	token   string
	expires time.Time
}

// NewTokenSource 创建 token 源。
func NewTokenSource(appID, appSecret string) *TokenSource {
	return &TokenSource{
		appID:     appID,
		appSecret: appSecret,
		client:    &http.Client{Timeout: 15 * time.Second},
	}
}

// AuthorizationHeader 返回 "QQBot {access_token}"。
func (t *TokenSource) AuthorizationHeader(ctx context.Context) (string, error) {
	tok, err := t.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	return "QQBot " + tok, nil
}

// AccessToken 返回当前可用 token（临期自动刷新）。
func (t *TokenSource) AccessToken(ctx context.Context) (string, error) {
	t.mu.RLock()
	tok := t.token
	exp := t.expires
	t.mu.RUnlock()
	if tok != "" && time.Now().Before(exp.Add(-tokenRefreshMargin)) {
		return tok, nil
	}
	if err := t.Refresh(ctx); err != nil {
		return "", err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.token, nil
}

// Refresh 强制刷新 access_token。
func (t *TokenSource) Refresh(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{
		"appId":        t.appID,
		"clientSecret": t.appSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("getAppAccessToken: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("getAppAccessToken HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   any    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("getAppAccessToken decode: %w", err)
	}
	if result.AccessToken == "" {
		return fmt.Errorf("getAppAccessToken: empty access_token")
	}
	sec := parseExpiresIn(result.ExpiresIn)
	if sec <= 0 {
		sec = 7200
	}
	t.mu.Lock()
	t.token = result.AccessToken
	t.expires = time.Now().Add(time.Duration(sec) * time.Second)
	t.mu.Unlock()
	return nil
}

func parseExpiresIn(v any) int {
	switch x := v.(type) {
	case string:
		n, _ := strconv.Atoi(x)
		return n
	case float64:
		return int(x)
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	default:
		return 0
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
