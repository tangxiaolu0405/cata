package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// OpenTerminal 在系统终端中打开工作空间目录，并让终端停在那个目录。
//   - macOS：优先 iTerm2，否则 Terminal.app
//   - Windows：新建 cmd 窗口并 cd 到目录
//   - Linux：尝试 gnome-terminal / xfce4-terminal / konsole
func (a *App) OpenTerminal(dir string) error {
	dir = filepath.Clean(dir)
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s 不是目录", dir)
	}
	switch runtime.GOOS {
	case "darwin":
		return openMacTerminal(dir)
	case "windows":
		return openWindowsTerminal(dir)
	default:
		return openLinuxTerminal(dir)
	}
}

// openMacTerminal 用 `open -a <App> <dir>`：Terminal / iTerm 会新开窗口并进入该目录。
func openMacTerminal(dir string) error {
	app := "Terminal"
	if _, err := os.Stat("/Applications/iTerm.app"); err == nil {
		app = "iTerm"
	}
	cmd := exec.Command("open", "-a", app, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("打开 %s 失败: %v (%s)", app, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// openWindowsTerminal 新建一个 cmd 窗口并 cd 到目录（start 立即返回）。
func openWindowsTerminal(dir string) error {
	cmd := exec.Command("cmd", "/c", "start", "", "cmd", "/k", "cd", "/d", dir)
	return cmd.Start()
}

// openLinuxTerminal 尝试常见终端模拟器；都没有则报错提示。
func openLinuxTerminal(dir string) error {
	var tries []*exec.Cmd
	if _, err := exec.LookPath("gnome-terminal"); err == nil {
		tries = append(tries, exec.Command("gnome-terminal", "--working-directory="+dir))
	}
	if _, err := exec.LookPath("xfce4-terminal"); err == nil {
		tries = append(tries, exec.Command("xfce4-terminal", "--working-directory="+dir))
	}
	if _, err := exec.LookPath("konsole"); err == nil {
		tries = append(tries, exec.Command("konsole", "--workdir", dir))
	}
	if _, err := exec.LookPath("x-terminal-emulator"); err == nil {
		tries = append(tries, exec.Command("x-terminal-emulator", "-e", "cd", dir, "&&", "exec", "$SHELL"))
	}
	for _, c := range tries {
		if err := c.Start(); err == nil {
			return nil // 已启动
		}
	}
	return fmt.Errorf("未找到可用终端模拟器（gnome-terminal / xfce4-terminal / konsole）")
}
