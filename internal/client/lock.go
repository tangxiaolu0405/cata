package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/brain"
	"cata/internal/clock"
)

type lockMeta struct {
	PID       int    `json:"pid"`
	OutputCwd string `json:"output_cwd"`
	StartedAt string `json:"started_at"`
}

// AcquireOutputLock ensures one chat per output directory.
func AcquireOutputLock(outputCwd string) (func(), error) {
	abs, err := filepath.Abs(strings.TrimSpace(outputCwd))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(brain.CataHome(), "locks"), 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(brain.CataHome(), "locks", "out_"+hex.EncodeToString(sha256Sum(abs)[:8])+".lock")

	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0644)
		if err == nil {
			b, _ := json.Marshal(lockMeta{PID: os.Getpid(), OutputCwd: abs, StartedAt: clock.RFC3339()})
			_, _ = f.Write(b)
			return func() { _ = f.Close(); _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		var m lockMeta
		if b, e := os.ReadFile(path); e == nil {
			_ = json.Unmarshal(b, &m)
		}
		if m.PID > 0 && processAlive(m.PID) {
			return nil, fmt.Errorf("cata: output area busy (%s, pid %d)", abs, m.PID)
		}
		_ = os.Remove(path)
	}
	return nil, fmt.Errorf("cata: could not lock %s", abs)
}

func sha256Sum(s string) []byte {
	h := sha256.Sum256([]byte(filepath.Clean(s)))
	return h[:]
}
