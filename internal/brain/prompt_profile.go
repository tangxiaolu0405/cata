package brain

import "sync"

// PromptProfile 控制出站 system 注入体积：minimal < task < full。
// minimal 主要用于 delegate worker 子 Agent；主 chat 首轮从 task 起。
type PromptProfile string

const (
	PromptProfileMinimal PromptProfile = "minimal"
	PromptProfileTask    PromptProfile = "task"
	PromptProfileFull    PromptProfile = "full"

	// PromptProfileLight 已废弃，等同 minimal（兼容旧引用）。
	PromptProfileLight = PromptProfileMinimal
)

// MinimalBootPrompt minimal 档固定 boot（不读完整 boot-assembler.md）。
const MinimalBootPrompt = `你是 Cata 终端助手。遵守：不虚构路径；产出区=当前 output_cwd；读写改文件须用工具，禁止只写代码块假装已执行；run_command 等敏感操作须用户确认；脑子在 CATA_HOME，交付物写在产出区。`

var promptProfileMu sync.RWMutex
var activePromptProfile PromptProfile = PromptProfileFull

// ProfileRank 档位序（越大越完整）。
func ProfileRank(p PromptProfile) int {
	switch p {
	case PromptProfileMinimal:
		return 0
	case PromptProfileTask:
		return 1
	default:
		return 2
	}
}

// PromptProfileMax 取较高档位（用于会话 sticky）。
func PromptProfileMax(a, b PromptProfile) PromptProfile {
	if ProfileRank(a) >= ProfileRank(b) {
		return a
	}
	return b
}

// SetPromptProfile 设置当前 chat 轮次的 system 注入档位（server 每轮设置并 defer Clear）。
func SetPromptProfile(p PromptProfile) {
	promptProfileMu.Lock()
	activePromptProfile = p
	promptProfileMu.Unlock()
}

// ActivePromptProfile 返回当前档位；未设置时为 full。
func ActivePromptProfile() PromptProfile {
	promptProfileMu.RLock()
	defer promptProfileMu.RUnlock()
	if activePromptProfile == "" {
		return PromptProfileFull
	}
	return activePromptProfile
}

// ClearPromptProfile 恢复默认 full。
func ClearPromptProfile() {
	SetPromptProfile(PromptProfileFull)
}

func IsMinimalPromptProfile() bool {
	return ProfileRank(ActivePromptProfile()) == 0
}

func IsTaskPromptProfile() bool {
	return ActivePromptProfile() == PromptProfileTask
}

// IsLightPromptProfile 兼容旧名。
func IsLightPromptProfile() bool {
	return IsMinimalPromptProfile()
}
