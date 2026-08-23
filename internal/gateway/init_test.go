package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cata/internal/cata/config"
)

func TestInitConfig_base(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	if err := config.InitBrainPath(); err != nil {
		t.Fatal(err)
	}

	path, err := InitConfig(InitOptions{Edition: EditionBase})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Edition != EditionBase {
		t.Fatalf("edition=%s", cfg.Edition)
	}
	if !cfg.CataServer.AutoStart {
		t.Fatal("base should auto_start")
	}
}

func TestInitConfig_existsNoForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	if err := config.InitBrainPath(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, "gateway.json")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := InitConfig(InitOptions{})
	if err == nil {
		t.Fatal("expected error when exists")
	}
}

func TestInitConfig_channelForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	if err := config.InitBrainPath(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, "gateway.json")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := InitConfig(InitOptions{Edition: EditionChannel, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("path=%s", got)
	}
	data, _ := os.ReadFile(path)
	var cfg Config
	_ = json.Unmarshal(data, &cfg)
	if cfg.Edition != EditionChannel {
		t.Fatalf("edition=%s", cfg.Edition)
	}
}

func TestInitConfig_remote(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	if err := config.InitBrainPath(); err != nil {
		t.Fatal(err)
	}

	path, err := InitConfig(InitOptions{Edition: EditionRemote})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Edition != EditionRemote {
		t.Fatalf("edition=%s", cfg.Edition)
	}
	if cfg.CataServer.Mode != ServerModeRemote {
		t.Fatalf("mode=%s", cfg.CataServer.Mode)
	}
	if cfg.GatewayToken != "" {
		t.Fatal("remote template should not include gateway_token (removed)")
	}
}
