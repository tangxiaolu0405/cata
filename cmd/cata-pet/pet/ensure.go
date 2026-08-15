package pet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"cata/internal/cata/config"
	"cata/internal/cata/socketclient"
)

// FindCataBinary locates the main cata binary (pet must not spawn itself).
func FindCataBinary() (string, error) {
	if p := os.Getenv("CATA_BIN"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		name := "cata"
		if runtime.GOOS == "windows" {
			name = "cata.exe"
		}
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath("cata"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("cata binary not found (set CATA_BIN or install cata on PATH)")
}

func pingServer() error {
	_ = config.InitBrainPath()
	// 复用共享 socket 客户端协议的 ping，避免第三份内联实现（与 client/socketclient 一致）。
	return socketclient.Ping(config.ResolvedSocketPath())
}

// EnsureServer pings the socket or starts `cata run --managed`.
func EnsureServer() error {
	_ = config.InitBrainPath()
	if err := pingServer(); err == nil {
		return nil
	}
	bin, err := FindCataBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "run", "--managed")
	cmd.Stdout = nil
	cmd.Stderr = nil
	detachCmd(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cata: %w", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := pingServer(); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("cata server not ready after 20s")
}
