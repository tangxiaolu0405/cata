//go:build !windows
// +build !windows

package terminal

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"github.com/creack/pty"
)

func (t *Terminal) updatePTYSize() {
	if t.pty == nil { // SSH or other direct connection?
		return
	}
	scale := float32(1.0)
	c := fyne.CurrentApp().Driver().CanvasForObject(t)
	if c != nil {
		scale = c.Scale()
	}
	_ = pty.Setsize(t.pty.(*os.File), &pty.Winsize{
		Rows: uint16(t.config.Rows), Cols: uint16(t.config.Columns),
		X: uint16(t.Size().Width * scale), Y: uint16(t.Size().Height * scale),
	})
}

func (t *Terminal) startPTY() (io.WriteCloser, io.Reader, io.Closer, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}

	env := os.Environ()
	env = append(env, "TERM=xterm-256color")
	env = append(env, "COLORTERM=truecolor")
	c := exec.Command(shell)
	c.Dir = t.startingDir()
	c.Env = env
	t.cmd = c
	t.config.PWD = c.Dir

	// Start the command with a pty.
	f, err := pty.Start(c)
	if err != nil {
		return nil, nil, nil, err
	}
	// pty.Start 成功后才启动 PWD 轮询 goroutine（此前 c.Process 为 nil，会 panic），
	// 并通过 done channel 随 Terminal close 停止（否则 shell 退出后永久泄漏）。
	if runtime.GOOS == "linux" && t.done != nil {
		t.startPWDWatcher(c)
	}
	return f, f, f, nil
}

// startPWDWatcher 轮询 shell 的 /proc/<pid>/cwd，变化时通知配置。
// 仅 Linux 可用（/proc）；macOS/BSD 无 /proc，不启动（PWD 追踪静默关闭）。
// 由 t.done 随 close() 关闭，避免 goroutine 泄漏。
func (t *Terminal) startPWDWatcher(c *exec.Cmd) {
	go func() {
		procPath := "/proc/" + strconv.Itoa(c.Process.Pid) + "/cwd"
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-ticker.C:
				if t.recentKeyActivity() {
					continue
				}
				wd, err := os.Readlink(procPath)
				if err != nil {
					continue
				}
				if wd != t.config.PWD {
					t.config.PWD = wd
					fyne.Do(t.onConfigure)
				}
			}
		}
	}()
}
