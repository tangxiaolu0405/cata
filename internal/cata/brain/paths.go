// Package brain 提供与 brain/core.md 一致的核心路径与资源定义，确保代码与文档单一数据源。
package brain

import (
	"path/filepath"
)

// 现行 workspace 布局的相对路径（与 layout.go 的 Rel* 保持一致，勿在此重复定义新值）。
const (
	RelPathConstraints = "constraints.md"
	RelPathBehavior    = "behavior.md"

	RelPathMemoryIndex  = "memory_index.json"
	RelPathEvolutionLog = "evolution_log.json"
)

// legacy 路径：旧版扁平 ~/.cata/brain/{hot.md,memory/short-term,…} 布局。
// 仅两处消费：
//   - MigrateLegacyBrain：一次性把旧布局搬进首个 workspace（已由 .legacy_brain_migrated_v1 标记）；
//   - NormalizeEvolveUpdatePath：把模型可能写出的旧路径归一成现行路径。
//
// 注意：这些不是现行 workspace 布局。现行值见 layout.go：
// short=RelShortCurrent("memory/short/current.md")、long=RelMemoryLong("memory/long")、
// archive=RelMemoryArchive("memory/archive")。新增代码勿引用本组常量。
const (
	RelPathHot              = "hot.md"                               // legacy → 现行 modes/_default/persona.md
	RelPathShortTermCurrent = "memory/short-term/current_session.md" // legacy → 现行 memory/short/current.md
	RelPathLongTerm         = "memory/long-term"                     // legacy → 现行 memory/long
	RelPathArchive          = "archive"                              // legacy → 现行 memory/archive
)

// Path 在活跃 workspace 下解析相对路径；无 workspace 时回退到 legacy brain 根。
func Path(rel string) string {
	if w := Active(); w != nil {
		return w.Path(rel)
	}
	return filepath.Join(brainRoot(), rel)
}

// HotPath 当前 workspace 活跃 mode 的 persona。
func HotPath() string {
	if w := Active(); w != nil {
		return w.PersonaPath()
	}
	return filepath.Join(brainRoot(), RelPathHot)
}

// ArchiveDir 当前 workspace 的 archive。
func ArchiveDir() string {
	if w := Active(); w != nil {
		return w.ArchiveDir()
	}
	return filepath.Join(brainRoot(), RelPathArchive)
}

// EvolutionLogPath 当前 workspace 的演进日志。
func EvolutionLogPath() string {
	if w := Active(); w != nil {
		return w.EvolutionLogPath()
	}
	return filepath.Join(brainRoot(), RelPathEvolutionLog)
}

// ShortTermCurrentPath 当前 workspace 短期记忆。
func ShortTermCurrentPath() string {
	if w := Active(); w != nil {
		return w.ShortTermPath()
	}
	return filepath.Join(brainRoot(), RelPathShortTermCurrent)
}

// LongTermDir 当前 workspace 长期记忆目录。
func LongTermDir() string {
	if w := Active(); w != nil {
		return w.LongTermDir()
	}
	return filepath.Join(brainRoot(), RelPathLongTerm)
}

// GlobalConstraintsPath 全局约束。
func GlobalConstraintsPath() string {
	return filepath.Join(globalDir(), FileGlobalConstraints)
}

// GlobalBehaviorPath 全局行为 SOP。
func GlobalBehaviorPath() string {
	return filepath.Join(globalDir(), FileGlobalBehavior)
}

// PersonaLocalPath 当前 workspace 项目说明。
func PersonaLocalPath() string {
	if w := Active(); w != nil {
		return w.PersonaLocalPath()
	}
	return filepath.Join(brainRoot(), RelPersonaLocal)
}

const ArchiveBackupDir = "backup"
