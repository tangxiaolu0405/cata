package brain

import (
	"fmt"
	"path/filepath"
	"strings"
)

// EvolvePathRole 描述脑子内路径在自主演进中的角色。
type EvolvePathRole struct {
	Rel           string // 相对 workspace 根，或 global/* 虚拟路径
	WrittenBy     string // server | evolve | init | server+evolve
	EvolveObserve bool   // Observe 阶段是否读元数据/计数
	EvolveInput   bool   // 决策 prompt 是否可能读全文节选
	EvolvePatch   bool   // LLM updates[] 是否允许写入
	ContextInject bool   // 终端 chat 是否注入 LLM system
	Notes         string
}

// EvolvePathCatalog 脑子路径能力表（directory-plan / brain-files.md 与此对齐）。
func EvolvePathCatalog() []EvolvePathRole {
	return []EvolvePathRole{
		{
			Rel: "memory/short/current.md", WrittenBy: "server（每轮）+ evolve",
			EvolveObserve: true, EvolveInput: true, EvolvePatch: true, ContextInject: false,
			Notes: "对话流水；evolve 可 write/overwrite/delete 归档；server 每轮 append",
		},
		{
			Rel: "modes/<mode>/persona.md", WrittenBy: "evolve",
			EvolveObserve: true, EvolveInput: true, EvolvePatch: true, ContextInject: true,
			Notes: "身份结晶；路径 focus_path/.cata/modes/<mode>/persona.md",
		},
		{
			Rel: "persona.local.md", WrittenBy: "evolve",
			EvolveObserve: false, EvolveInput: true, EvolvePatch: true, ContextInject: true,
			Notes: "focus_path/.cata/persona.local.md",
		},
		{
			Rel: "modes/<mode>/behavior.md", WrittenBy: "evolve",
			EvolveObserve: false, EvolveInput: false, EvolvePatch: true, ContextInject: true,
			Notes: "项目主要内容 SOP；focus_path/.cata/modes/<mode>/behavior.md",
		},
		{
			Rel: "modes/<mode>/constraints.md", WrittenBy: "evolve",
			EvolveObserve: false, EvolveInput: false, EvolvePatch: true, ContextInject: true,
			Notes: "项目主要内容约束；focus_path/.cata/modes/<mode>/constraints.md",
		},
		{
			Rel: "modes/<mode>/capabilities.yaml", WrittenBy: "init + evolve（skill 名）",
			EvolveObserve: false, EvolveInput: false, EvolvePatch: true, ContextInject: false,
			Notes: "append 仍由 server 追加 skill；write/overwrite 须保留 mcp:",
		},
		{
			Rel: "memory/long/*.md", WrittenBy: "evolve",
			EvolveObserve: true, EvolveInput: false, EvolvePatch: true, ContextInject: false,
			Notes: "长期细节；经 memory/index.json 按需展开",
		},
		{
			Rel: "memory/archive/*.md", WrittenBy: "evolve（summarize 移入）",
			EvolveObserve: true, EvolveInput: false, EvolvePatch: true, ContextInject: false,
			Notes: "冷存储；写入后不再参与 evolve 输入与 context",
		},
		{
			Rel: "memory/index.json", WrittenBy: "evolve（补丁后同步）",
			EvolveObserve: false, EvolveInput: false, EvolvePatch: true, ContextInject: true,
			Notes: "摘要索引；≤2800B 注入 chat",
		},
		{
			Rel: "skills/<id>/SKILL.md|manifest.yaml|script.*", WrittenBy: "evolve（crystallize）",
			EvolveObserve: false, EvolveInput: false, EvolvePatch: true, ContextInject: true,
			Notes: "SKILL.md 经 SkillsIndexBlockCached 注入索引；全文 read_skill、执行 run_skill",
		},
		{
			Rel: "meta.json", WrittenBy: "server + evolve",
			EvolveObserve: false, EvolveInput: false, EvolvePatch: true, ContextInject: false,
			Notes: "active_mode 等",
		},
		{
			Rel: "evolution_log.json", WrittenBy: "evolve（审计）",
			EvolveObserve: true, EvolveInput: false, EvolvePatch: true, ContextInject: false,
			Notes: "演进审计",
		},
		{
			Rel: "global/*", WrittenBy: "init + 用户",
			EvolveObserve: false, EvolveInput: false, EvolvePatch: false, ContextInject: true,
			Notes: "引导型提示词（~/.cata/global）；per-workspace evolve 禁止 patch",
		},
	}
}

// NormalizeEvolveUpdatePath 校验并规范化演进补丁路径（相对 workspace 根或 global/*）。
// 允许 workspace 脑子内任意相对路径；仅拒绝路径穿越。
func NormalizeEvolveUpdatePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "brain/")
	p = filepath.ToSlash(filepath.Clean(p))
	if p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
		return "", fmt.Errorf("path not allowed: %s", p)
	}
	if p == "." || p == "" {
		return "", fmt.Errorf("path required")
	}

	legacy := map[string]string{
		RelPathHot:              DirModes + "/" + ModeDefaultID + "/" + FilePersona,
		RelPathShortTermCurrent: RelShortCurrent,
	}
	if mapped, ok := legacy[p]; ok {
		return mapped, nil
	}
	return normalizeModePathRel(p), nil
}

// IsEvolveSharedGlobalPath 判断是否为全机共享的 global/* 路径。
func IsEvolveSharedGlobalPath(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	return rel == "global" || strings.HasPrefix(rel, "global/")
}

// RejectEvolveSharedGlobalPatch per-workspace 演进禁止 patch global/*（全 workspace 共用，会交叉污染）。
func RejectEvolveSharedGlobalPatch(rel string) error {
	if IsEvolveSharedGlobalPath(rel) {
		return fmt.Errorf("global path not patchable in per-workspace evolution: %s", rel)
	}
	return nil
}

// ResolveEvolveUpdateAbs 将规范化相对路径解析为磁盘绝对路径。
func ResolveEvolveUpdateAbs(w *Workspace, rel string) (abs, storeRel string, err error) {
	if strings.HasPrefix(rel, "global/") {
		sub := strings.TrimPrefix(rel, "global/")
		abs, err := PathUnderBase(globalDir(), sub)
		return abs, rel, err
	}
	abs, err = ResolveBrainDocAbs(w, rel)
	return abs, rel, err
}
