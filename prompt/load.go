package prompt

import (
	"embed"
	"path/filepath"
	"strings"
)

//go:embed evolve/*.md
var embedded embed.FS

// 路径常量（相对 prompt/ 根，编译时 embed）。
const (
	FileEvolveSystem               = "evolve/system.md"
	FileEvolveSessionCompressExtra = "evolve/session_compress_extra.md"
	FileEvolveCrystallize          = "evolve/crystallize.md"
	FileEvolveDecisionScope        = "evolve/decision_scope.md"
	FileEvolveDecisionFooter       = "evolve/decision_footer.md"
)

// Load 读取 embed 中的提示词；修改 prompt/ 下文件后须重新 go build。
func Load(rel string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return ""
	}
	b, err := embedded.ReadFile(rel)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\r\n") + "\n"
}

// EvolveSystemPrompt 常规演进 system。
func EvolveSystemPrompt() string {
	return Load(FileEvolveSystem)
}

// EvolveSessionCompressPrompt 会话压缩演进 system。
func EvolveSessionCompressPrompt() string {
	base := strings.TrimSpace(EvolveSystemPrompt())
	extra := strings.TrimSpace(Load(FileEvolveSessionCompressExtra))
	if extra == "" {
		return base
	}
	if base == "" {
		return extra
	}
	return base + "\n\n" + extra
}

// EvolveCrystallizePrompt 固化 skill 演进 system。
func EvolveCrystallizePrompt() string {
	return Load(FileEvolveCrystallize)
}

// EvolveDecisionScopeNotice buildDecisionPrompt 中的 workspace 隔离提醒。
func EvolveDecisionScopeNotice() string {
	return strings.TrimSpace(Load(FileEvolveDecisionScope))
}

// EvolveDecisionFooter buildDecisionPrompt 结尾。
func EvolveDecisionFooter() string {
	return strings.TrimSpace(Load(FileEvolveDecisionFooter))
}
