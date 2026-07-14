package gateway

import "testing"

func TestConfig_ResolvedUIListen(t *testing.T) {
	if (Config{}).ResolvedUIListen() != DefaultUIListen {
		t.Fatal("empty should default")
	}
	if (Config{UIListen: "off"}).ResolvedUIListen() != "" {
		t.Fatal("off should disable")
	}
	if (Config{UIListen: "127.0.0.1:9000"}).ResolvedUIListen() != "127.0.0.1:9000" {
		t.Fatal("custom listen")
	}
	if !(Config{}).UIEnabled() || (Config{UIListen: "0"}).UIEnabled() {
		t.Fatal("UIEnabled mismatch")
	}
}

func TestConfig_editionBase(t *testing.T) {
	cfg := Config{Edition: EditionBase}
	cfg.normalize()
	if !cfg.ShouldAutoStartServer() {
		t.Fatal("base should auto start")
	}
	if cfg.CataServer.Mode != ServerModeSocket {
		t.Fatalf("mode=%s", cfg.CataServer.Mode)
	}
}

func TestConfig_editionChannel(t *testing.T) {
	cfg := Config{Edition: EditionChannel}
	cfg.normalize()
	if cfg.ShouldAutoStartServer() {
		t.Fatal("channel should not auto start by default")
	}
	cfg.CataServer.AutoStart = true
	if !cfg.ShouldAutoStartServer() {
		t.Fatal("channel with auto_start")
	}
}

func TestConfig_remoteMode(t *testing.T) {
	cfg := Config{Edition: EditionBase, CataURL: "https://worker.example"}
	cfg.normalize()
	if cfg.ShouldAutoStartServer() {
		t.Fatal("remote should not auto start local server")
	}
	if cfg.CataServer.Mode != ServerModeRemote {
		t.Fatalf("mode=%s", cfg.CataServer.Mode)
	}
}
