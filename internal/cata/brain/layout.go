package brain

import (
	"path/filepath"

	"cata/internal/cata/config"
)

// ~/.cata 目录布局（CATA_HOME）。
const (
	DirRegistry        = "registry"
	FileWorkspacesJSON = "workspaces.json"

	DirGlobal                  = "global"
	FileGlobalConstraints      = "constraints.md"
	FileGlobalBehavior         = "behavior.md"
	FileGlobalBoot             = "boot-assembler.md"
	FileGlobalMinimalBoot      = "minimal-boot.md"
	FileGlobalDelegateGuide    = "delegate-guide.md"
	FileGlobalWorkerContract   = "worker-contract.md"
	FileGlobalDelegateTaskTool = "delegate-task-tool.json"

	DirBrain      = "brain"
	DirWorkspaces = "workspaces"

	RelPersonaLocal           = "persona.local.md"
	RelMetaJSON               = "meta.json"
	RelEvolutionLog           = "evolution_log.json"
	RelMemoryIndex            = "memory/index.json"
	RelShortCurrent           = "memory/short/current.md"
	RelMemoryLong             = "memory/long"
	RelMemoryLongLearnings    = "memory/long/learnings.md"     // 审计滚动账本（long-term 槽）；可复用事实走 updates→persona
	RelMemoryLongSessionNotes = "memory/long/session-notes.md" // 合并的日/会话摘要
	RelMemoryArchive          = "memory/archive"

	fileLearningPlaybookMigrate = ".learnings_playbook_v1"  // 一次性 learnings 碎片迁移
	fileLongMemoryCompactV1     = ".long_memory_compact_v1" // 一次性 long 目录压缩归档

	DirModes = "modes"
	// ModeDefaultID 默认前台 mode（对用户说话的那张卡）。
	ModeDefaultID = "_default"
	// ModeAliasOrchestratorID 曾短暂误用的默认 mode 名；NormalizeModeID 归一到 ModeDefaultID。
	ModeAliasOrchestratorID = "_orchestrator"
	FilePersona             = "persona.md"
	FileBehavior            = "behavior.md"
	FileConstraints         = "constraints.md"
	FileCapabilities        = "capabilities.yaml"

	DirSkills         = "skills"
	FileSkillManifest = "manifest.yaml"
	FileSkillMD       = "SKILL.md"

	ProjectCataDir    = ".cata"
	FileWorkspaceYAML = "workspace.yaml"
	FileWorkspaceLink = "workspace.link"
	DirSubagentRuns   = "subagent_runs"
	DirCases          = "cases"
)

// CataHome 状态根（~/.cata）。
func CataHome() string {
	return config.CataHome()
}

func registryDir() string { return filepath.Join(CataHome(), DirRegistry) }
func workspacesRegistryPath() string {
	return filepath.Join(registryDir(), FileWorkspacesJSON)
}
func globalDir() string { return filepath.Join(CataHome(), DirGlobal) }
func brainRoot() string { return filepath.Join(CataHome(), DirBrain) }
func workspacesRoot() string {
	return filepath.Join(brainRoot(), DirWorkspaces)
}
