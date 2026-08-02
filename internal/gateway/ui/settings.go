package ui

import (
	"encoding/json"
	"net/http"
	"strings"

	"cata/internal/cata/config"
	"cata/internal/gateway"
)

type appSettingsResponse struct {
	Path   string                     `json:"path"`
	Config config.AppConfig           `json:"config"`
	Extras map[string]json.RawMessage `json:"extras"`
}

type appSettingsRequest struct {
	Config config.AppConfig           `json:"config"`
	Extras map[string]json.RawMessage `json:"extras"`
}

type gatewaySettingsResponse struct {
	Path   string                     `json:"path"`
	Config gateway.Config             `json:"config"`
	Extras map[string]json.RawMessage `json:"extras"`
	Note   string                     `json:"note,omitempty"`
}

type gatewaySettingsRequest struct {
	Config gateway.Config             `json:"config"`
	Extras map[string]json.RawMessage `json:"extras"`
}

func (s *Server) handleSettingsApp(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, extras, path, err := config.LoadAppConfigDocument()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if extras == nil {
			extras = map[string]json.RawMessage{}
		}
		writeJSON(w, appSettingsResponse{Path: path, Config: config.RedactConfig(&cfg), Extras: extras})
	case http.MethodPut:
		var body appSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		disk, diskExtras, _, err := config.LoadAppConfigDocument()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		body.Config.LLM.APIKey = config.ApplySecretPreserve(body.Config.LLM.APIKey, disk.LLM.APIKey)
		if body.Extras == nil {
			body.Extras = diskExtras
			if body.Extras == nil {
				body.Extras = map[string]json.RawMessage{}
			}
		}
		if err := config.SaveAppConfigDocument(&body.Config, body.Extras); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = config.LoadConfig()
		cfg, extras, path, err := config.LoadAppConfigDocument()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, appSettingsResponse{Path: path, Config: config.RedactConfig(&cfg), Extras: extras})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleSettingsGateway(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, extras, err := gateway.LoadGatewayDocument()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if extras == nil {
			extras = map[string]json.RawMessage{}
		}
		writeJSON(w, gatewaySettingsResponse{
			Path:   gateway.ConfigPath(),
			Config: redactGatewayConfig(cfg),
			Extras: extras,
		})
	case http.MethodPut:
		var body gatewaySettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		disk, diskExtras, err := gateway.LoadGatewayDocument()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		body.Config.TelegramBotToken = config.ApplySecretPreserve(body.Config.TelegramBotToken, disk.TelegramBotToken)
		body.Config.QQAppSecret = config.ApplySecretPreserve(body.Config.QQAppSecret, disk.QQAppSecret)
		if body.Extras == nil {
			body.Extras = diskExtras
			if body.Extras == nil {
				body.Extras = map[string]json.RawMessage{}
			}
		}
		if err := gateway.SaveGatewayDocument(body.Config, body.Extras); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if live, err := gateway.LoadConfig(); err == nil {
			s.setCfg(live)
		}
		cfg, extras, err := gateway.LoadGatewayDocument()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, gatewaySettingsResponse{
			Path:   gateway.ConfigPath(),
			Config: redactGatewayConfig(cfg),
			Extras: extras,
			Note:   "渠道密钥变更需重启 cata-gateway 后生效；本页 UI 相关配置已热更新。",
		})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func redactGatewayConfig(cfg gateway.Config) gateway.Config {
	out := cfg
	if strings.TrimSpace(out.TelegramBotToken) != "" {
		out.TelegramBotToken = config.SecretRedacted
	}
	if strings.TrimSpace(out.QQAppSecret) != "" {
		out.QQAppSecret = config.SecretRedacted
	}
	return out
}
