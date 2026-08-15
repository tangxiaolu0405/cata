package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"cata/internal/cata/brain"
)

// InitOptions gateway.json 初始化选项。
type InitOptions struct {
	Edition string // base | channel；空则 base
	Force   bool   // 已存在时覆盖
}

// InitConfig 写入 ~/.cata/gateway.json 模板（不存在时创建；Force 时覆盖）。
func InitConfig(opts InitOptions) (string, error) {
	if err := brain.EnsureCataLayout(); err != nil {
		return "", fmt.Errorf("cata layout: %w", err)
	}
	edition := opts.Edition
	if edition == "" {
		edition = EditionBase
	}
	cfg := defaultConfigForEdition(edition)
	cfg.normalize()

	path := filepath.Join(brain.CataHome(), "gateway.json")
	if _, err := os.Stat(path); err == nil && !opts.Force {
		return path, fmt.Errorf("%s already exists (use --force to overwrite)", path)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func defaultConfigForEdition(edition string) Config {
	switch edition {
	case EditionRemote:
		return Config{
			Edition: EditionRemote,
			CataServer: CataServerConfig{
				Mode: ServerModeRemote,
			},
			GatewayToken: "YOUR_GATEWAY_TOKEN",
			TunnelListen: DefaultTunnelListen,
			UIListen:     DefaultUIListen,
			Projects:     []Project{},
		}
	case EditionChannel:
		return Config{
			Edition: EditionChannel,
			CataServer: CataServerConfig{
				Mode:      ServerModeExternal,
				AutoStart: false,
			},
			TelegramBotToken: "YOUR_BOT_TOKEN",
			QQAppID:          "",
			QQAppSecret:      "",
			UIListen:         DefaultUIListen,
			Projects:         []Project{},
		}
	default:
		return Config{
			Edition: EditionBase,
			CataServer: CataServerConfig{
				Mode:       ServerModeSocket,
				AutoStart:  true,
				Managed:    false,
				StopOnExit: false,
			},
			TelegramBotToken: "YOUR_BOT_TOKEN",
			QQAppID:          "",
			QQAppSecret:      "",
			UIListen:         DefaultUIListen,
			Projects:         []Project{},
		}
	}
}
