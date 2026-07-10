package brain

import (
	"path/filepath"

	"cata/internal/config"
)

// ~/.cata 目录布局（CATA_HOME）。
const (
	DirRegistry      = "registry"
	FileWorkspacesJSON = "workspaces.json"

	DirGlobal = "global"
	FileGlobalConstraints = "constraints.md"
	FileGlobalBehavior    = "behavior.md"
	FileGlobalBoot        = "boot-assembler.md"
	FileGlobalMinimalBoot = "minimal-boot.md"
	FileGlobalDelegateGuide = "delegate-guide.md"
	FileGlobalWorkerContract = "worker-contract.md"
	FileGlobalDelegateTaskTool = "delegate-task-tool.json"

	DirBrain      = "brain"
	DirWorkspaces = "workspaces"

	RelPersonaLocal       = "persona.local.md"
	RelMetaJSON           = "meta.json"
	RelEvolutionLog       = "evolution_log.json"
	RelMemoryIndex        = "memory/index.json"
	RelShortCurrent       = "memory/short/current.md"
	RelMemoryLong         = "memory/long"
	RelMemoryLongLearnings = "memory/long/learnings.md" // 单文件 playbook（B）；不再写 learnings/*.md
	RelMemoryLongSessionNotes = "memory/long/session-notes.md" // 合并的日/会话摘要
	RelMemoryArchive      = "memory/archive"

	fileLearningPlaybookMigrate = ".learnings_playbook_v1" // 一次性 learnings 碎片迁移
	fileLongMemoryCompactV1     = ".long_memory_compact_v1" // 一次性 long 目录压缩归档

	DirModes        = "modes"
	ModeDefaultID   = "_default"
	FilePersona     = "persona.md"
	FileBehavior    = "behavior.md"
	FileConstraints = "constraints.md"
	FileCapabilities = "capabilities.yaml"

	DirSkills         = "skills"
	FileSkillManifest = "manifest.yaml"
	FileSkillMD       = "SKILL.md"

	ProjectCataDir      = ".cata"
	FileWorkspaceYAML   = "workspace.yaml"
	FileWorkspaceLink   = "workspace.link"
	DirSubagentRuns     = "subagent_runs"
)

// CataHome 状态根（~/.cata）。
func CataHome() string {
	return config.CataHome()
}

func registryDir() string { return filepath.Join(CataHome(), DirRegistry) }
func workspacesRegistryPath() string {
	return filepath.Join(registryDir(), FileWorkspacesJSON)
}
func globalDir() string    { return filepath.Join(CataHome(), DirGlobal) }
func brainRoot() string    { return filepath.Join(CataHome(), DirBrain) }
func workspacesRoot() string {
	return filepath.Join(brainRoot(), DirWorkspaces)
}
