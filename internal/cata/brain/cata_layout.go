package brain

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

//go:embed guidance/constraints.md guidance/behavior.md guidance/delegate-guide.md guidance/delegate-task-tool.json
var embeddedGuidanceFS embed.FS

// guidanceTemplateVersion 递增后，下次 EnsureCataLayout 会从嵌入模板覆盖 ~/.cata/global 引导文件。
const guidanceTemplateVersion = 7

const fileGuidanceVersion = ".guidance_version"

var globalMemoryMigrateOnce sync.Once

// ChatBrainToolPathNote 文件工具 schema 中 brain/ 前缀说明。
const ChatBrainToolPathNote = "brain/persona.local+modes+skills → focus_path/.cata/; brain/memory/ → ~/.cata/brain/workspaces/<id>/ (skills NEVER in home cell)"

// EnsureCataLayout 创建 ~/.cata 顶层目录与 global 模板。
func EnsureCataLayout() error {
	home := CataHome()
	for _, d := range []string{
		home,
		registryDir(),
		globalDir(),
		brainRoot(),
		workspacesRoot(),
	} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	if err := seedGlobalFromRepo(); err != nil {
		return err
	}
	// 自动迁移：删除旧的角色身份文件（已迁到 internal/llm/rolecards/，运行时覆盖在 global/roles/）。
	removeDeprecatedGlobalIdentityFiles()
	if err := MigrateWorkspaceNaming(); err != nil {
		return fmt.Errorf("migrate workspace naming: %w", err)
	}
	if err := MigrateLegacyBrain(); err != nil {
		return err
	}
	globalMemoryMigrateOnce.Do(func() {
		MigrateAllLearningFragments()
		MigrateAllLongMemoryCompact()
	})
	return nil
}

func seedGlobalFromRepo() error {
	mapping := map[string]string{
		FileGlobalConstraints:   "guidance/constraints.md",
		FileGlobalBehavior:      "guidance/behavior.md",
		FileGlobalDelegateGuide: "guidance/delegate-guide.md",
	}
	force := shouldForceSyncGlobalGuidance()
	for dstName, srcPath := range mapping {
		data, err := embeddedGuidanceFS.ReadFile(srcPath)
		if err != nil {
			continue
		}
		dst := filepath.Join(globalDir(), dstName)
		if !force && !FileNeedsEvolveFill(dst) {
			continue
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return err
		}
	}
	if force {
		if err := writeGuidanceVersion(); err != nil {
			return err
		}
	}
	if err := seedDelegateTaskToolJSON(force); err != nil {
		return err
	}
	return seedGlobalDefaults()
}

// removeDeprecatedGlobalIdentityFiles 删除旧版角色身份文件（迁到角色卡片后不再读）。
// boot-assembler/minimal-boot/worker-contract 曾是 ~/.cata/global 下的身份文件，
// 现由 internal/llm/rolecards/（内置默认 + global/roles/ 运行时覆盖）取代。删除是安全幂等的。
func removeDeprecatedGlobalIdentityFiles() {
	for _, name := range []string{
		"boot-assembler.md",  // 旧身份文件，已迁到角色卡片
		"minimal-boot.md",    // 旧身份文件，已迁到 worker 角色卡片
		"worker-contract.md", // 旧身份文件，已迁到 worker 角色卡片
	} {
		_ = os.Remove(filepath.Join(globalDir(), name))
	}
}

func seedDelegateTaskToolJSON(force bool) error {
	toolsDir := filepath.Join(globalDir(), "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		return err
	}
	dst := filepath.Join(toolsDir, FileGlobalDelegateTaskTool)
	if !force {
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
	}
	data, err := embeddedGuidanceFS.ReadFile("guidance/delegate-task-tool.json")
	if err != nil {
		return nil
	}
	return os.WriteFile(dst, data, 0644)
}

func shouldForceSyncGlobalGuidance() bool {
	p := filepath.Join(globalDir(), fileGuidanceVersion)
	data, err := os.ReadFile(p)
	if err != nil {
		return true
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return err != nil || v < guidanceTemplateVersion
}

func writeGuidanceVersion() error {
	p := filepath.Join(globalDir(), fileGuidanceVersion)
	return os.WriteFile(p, []byte(fmt.Sprintf("%d\n", guidanceTemplateVersion)), 0644)
}

func seedGlobalDefaults() error {
	if err := ensureFile(filepath.Join(globalDir(), FileGlobalConstraints), "# Global constraints\n\n"); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(globalDir(), FileGlobalBehavior), "# Global behavior\n\n"); err != nil {
		return err
	}
	return nil
}

// InitDirectory 创建 ~/.cata 布局、global 模板，并迁移旧版扁平 brain（若有）。
func InitDirectory() error {
	if err := EnsureCataLayout(); err != nil {
		return fmt.Errorf("cata layout: %w", err)
	}
	if wd, err := os.Getwd(); err == nil {
		if _, err := ResolveWorkspace(wd); err != nil {
			return fmt.Errorf("brain: %w", err)
		}
	}
	return nil
}
