// Package pet is the cata-pet desktop companion backend (socket client + Wails bindings).
// Lives under cmd/cata-pet; only used by that binary.
package pet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"cata/internal/cata/config"
)

const petConfigName = "pet.json"

// Settings persisted at ~/.cata/pet.json.
type Settings struct {
	Cwd         string `json:"cwd,omitempty"`
	AlwaysOnTop bool   `json:"always_on_top"`
	WindowX     int    `json:"window_x,omitempty"`
	WindowY     int    `json:"window_y,omitempty"`
}

func defaultSettings() Settings {
	cwd, _ := os.Getwd()
	return Settings{Cwd: cwd, AlwaysOnTop: true}
}

func settingsPath() string {
	return filepath.Join(config.CataHome(), petConfigName)
}

var (
	settingsMu sync.Mutex
)

// LoadSettings reads ~/.cata/pet.json or returns defaults.
func LoadSettings() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	s := defaultSettings()
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	if s.Cwd == "" {
		s.Cwd, _ = os.Getwd()
	}
	return s
}

// SaveSettings writes ~/.cata/pet.json（tmp+rename 原子写，避免崩溃损坏配置）。
func SaveSettings(s Settings) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if err := os.MkdirAll(config.CataHome(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := settingsPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
