package desktop

import (
	"log"
	"sync"
	"time"

	"cata/cmd/cata-desktop/terminal"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// termPanel 内嵌终端的内容提供者：管理单个 shell 的生命周期。
// 不再自带标题栏/✕/外壳——外壳统一由 panel 构造，这里只提供
// stack（放置 Terminal 实例）与 start/stop。
// 运行系统 shell（macOS/Linux 用 $SHELL，Windows 用 PowerShell）。
type termPanel struct {
	mu      sync.Mutex
	term    *terminal.Terminal
	running bool
	stack   *fyne.Container // 内层：放置 Terminal 实例
}

func newTermPanel() *termPanel {
	return &termPanel{stack: container.NewStack()}
}

// start 在指定目录启动 shell。若正在运行，先结束（用于重新启动）。
// 每次启动新建 Terminal 实例，避免新旧 shell 共享内部状态。
func (p *termPanel) start(dir string) {
	p.stop()

	t := terminal.New()
	p.mu.Lock()
	p.term = t
	p.running = true
	p.mu.Unlock()

	// 把新终端放进内容区（主线程）。
	p.stack.Objects = []fyne.CanvasObject{t}
	p.stack.Refresh()

	t.SetStartDir(dir)
	go func() {
		if err := t.RunLocalShell(); err != nil {
			log.Printf("cata-desktop: 终端已退出: %v", err)
		}
		p.mu.Lock()
		if p.term == t {
			p.running = false
		}
		p.mu.Unlock()
	}()
}

// stop 结束当前 shell（发送 ^D；超时后强杀 PTY 避免遗留孤儿进程）。
func (p *termPanel) stop() {
	p.mu.Lock()
	t := p.term
	p.term = nil
	p.running = false
	p.mu.Unlock()
	if t != nil {
		t.Exit()
		// 等待 shell 因 ^D 自行退出；前台有子进程（vim/sleep）时 ^D 不会让它退出，
		// 超时后 ForceClose（关闭 PTY 让内核 SIGHUP 前台进程组），避免遗留孤儿进程。
		go func(term *terminal.Terminal) {
			time.Sleep(2 * time.Second)
			term.ForceClose()
		}(t)
	}
}

func (p *termPanel) isRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}
