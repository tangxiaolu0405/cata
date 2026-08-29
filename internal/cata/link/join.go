package link

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// JoinResult join 成功后的结果。
type JoinResult struct {
	MachineID    string
	MachineToken string
}

// JoinProtoHeader 自定义握手协议头：区分「cata 自身的 join 报文」与随机扫描/爆破流量。
// 网关端据此在最外层直接拦截未携带该头的请求（连限流/状态机都不进入），降低爆破面。
// 配合挑战-应答（JoinChallengeHeader*）：机器先取一次性挑战，request 回显，防伪造/重放。
const (
	JoinProtoHeaderName  = "X-Cata-Join"
	JoinProtoHeaderValue = "cata-tunnel.v1"
)

// JoinChallengeHeaderName 机器回显一次性挑战用的自定义头（随 X-Cata-Join 一起带）。
const (
	JoinChallengeHeaderName    = "X-Cata-Challenge"
	JoinChallengeSigHeaderName = "X-Cata-Challenge-Sig"
)

// Join 机器首次接入网关：POST join/request 拿一次性 code → 轮询 status 等管理员批准 →
// 拿到逐机器 token 后写回 link.json。无需任何固定口令：握手靠自定义协议头 X-Cata-Join，
// 授权靠一次性 code + 管理员在 UI 批准，最终凭证为网关签发的逐机器 token（machine_token）。
// gatewayURL 形如 https://gw.example.com 或 ws://127.0.0.1:8799；会归一化到 http(s) 调 join 端点。
func Join(gatewayURL, gatewayToken string) (*JoinResult, error) {
	base, err := joinBaseURL(gatewayURL)
	if err != nil {
		return nil, err
	}
	machineID := MachineID()
	if machineID == "unknown" || machineID == "" {
		return nil, fmt.Errorf("cannot determine machine_id")
	}

	// 1. 举手拿 code
	code, err := joinRequest(base, machineID)
	if err != nil {
		return nil, err
	}
	fmt.Printf("join code: %s\n在网关 UI 输入此 code 批准（10 分钟内有效），批准后自动领取 token...\n", code)

	// 2. 轮询等批准（最多 10 分钟）
	token, err := pollJoinStatus(base, code, JoinPollTimeout)
	if err != nil {
		return nil, err
	}

	// 3. 写回 link.json
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	cfg.GatewayURL = strings.TrimSpace(gatewayURL)
	cfg.GatewayToken = strings.TrimSpace(gatewayToken)
	cfg.MachineID = machineID
	cfg.MachineToken = token
	if err := SaveConfig(cfg); err != nil {
		return nil, err
	}
	return &JoinResult{MachineID: machineID, MachineToken: token}, nil
}

// JoinPollTimeout 机器侧 join 轮询超时（略大于 code TTL，给管理员充足时间）。
const JoinPollTimeout = 11 * time.Minute

func joinBaseURL(gatewayURL string) (string, error) {
	gw := strings.TrimSpace(gatewayURL)
	if gw == "" {
		return "", fmt.Errorf("gateway url required")
	}
	u, err := url.Parse(gw)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https", "http":
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	default:
		return "", fmt.Errorf("unsupported gateway url scheme %q", u.Scheme)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// joinFetchChallenge GET /cata/v1/join/challenge 取一次性挑战（nonce + HMAC 签名）。
func joinFetchChallenge(base string) (challenge, sig string, err error) {
	req, err := http.NewRequest(http.MethodGet, base+"/cata/v1/join/challenge", nil)
	if err != nil {
		return "", "", fmt.Errorf("join challenge: %w", err)
	}
	req.Header.Set(JoinProtoHeaderName, JoinProtoHeaderValue)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("join challenge: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("join challenge: status %d", resp.StatusCode)
	}
	var out struct {
		Challenge string `json:"challenge"`
		Sig       string `json:"sig"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if out.Challenge == "" || out.Sig == "" {
		return "", "", fmt.Errorf("join challenge: empty response")
	}
	return out.Challenge, out.Sig, nil
}

func joinRequest(base, machineID string) (string, error) {
	challenge, sig, err := joinFetchChallenge(base)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]string{"machine_id": machineID})
	req, err := http.NewRequest(http.MethodPost, base+"/cata/v1/join/request", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("join request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(JoinProtoHeaderName, JoinProtoHeaderValue)
	req.Header.Set(JoinChallengeHeaderName, challenge)
	req.Header.Set(JoinChallengeSigHeaderName, sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("join request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("join request: status %d", resp.StatusCode)
	}
	var out struct {
		JoinCode string `json:"join_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.JoinCode == "" {
		return "", fmt.Errorf("empty join code")
	}
	return out.JoinCode, nil
}

func pollJoinStatus(base, code string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, base+"/cata/v1/join/status?code="+url.QueryEscape(code), nil)
		if err == nil {
			req.Header.Set(JoinProtoHeaderName, JoinProtoHeaderValue)
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				if resp.StatusCode == http.StatusOK {
					var out struct {
						Approved     bool   `json:"approved"`
						MachineToken string `json:"machine_token"`
						ApprovedAt   string `json:"approved_at"`
					}
					if json.NewDecoder(resp.Body).Decode(&out) == nil {
						resp.Body.Close()
						if out.Approved && out.MachineToken != "" {
							if ts := strings.TrimSpace(out.ApprovedAt); ts != "" {
								fmt.Printf("网关已批准（%s），领取逐机器 token 成功\n", ts)
							} else {
								fmt.Println("网关已批准，领取逐机器 token 成功")
							}
							return out.MachineToken, nil
						}
					} else {
						resp.Body.Close()
					}
				} else {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("join approval timed out")
}
