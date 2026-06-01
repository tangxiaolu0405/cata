package brain

import (
	"fmt"
	"os"
	"path/filepath"

	"cata/internal/config"
)

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
	if err := MigrateWorkspaceNaming(); err != nil {
		return fmt.Errorf("migrate workspace naming: %w", err)
	}
	return MigrateLegacyBrain()
}

func seedGlobalFromRepo() error {
	repoRoot := config.FindProjectRoot()
	if repoRoot == "" {
		return seedGlobalDefaults()
	}
	src := filepath.Join(repoRoot, "brain")
	mapping := map[string]string{
		FileGlobalConstraints: RelPathConstraints,
		FileGlobalBehavior:    RelPathBehavior,
		FileGlobalBoot:        RelPathBootAssembler,
	}
	for dstName, srcName := range mapping {
		dst := filepath.Join(globalDir(), dstName)
		if !FileNeedsEvolveFill(dst) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, srcName))
		if err != nil {
			continue
		}
		_ = os.WriteFile(dst, data, 0644)
	}
	return seedGlobalDefaults()
}

func seedGlobalDefaults() error {
	if err := ensureFile(filepath.Join(globalDir(), FileGlobalConstraints), "# Global constraints\n\n"); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(globalDir(), FileGlobalBehavior), "# Global behavior\n\n"); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(globalDir(), FileGlobalBoot), defaultBootAssembler); err != nil {
		return err
	}
	return nil
}

const defaultBootAssembler = `# Boot 组装顺序

1. 本文件（boot-assembler）
2. 动态注入：【Cata 路径：脑子与产出区】（每轮含 brain_dir、focus_path、output_cwd）
3. 动态注入：【Cata 脑子节选】（global + mode persona）
4. 用户消息与 history

**脑子** = CATA_HOME（~/.cata/）。**产出区** = 当前 cwd；run_command 只在产出区执行。
`
