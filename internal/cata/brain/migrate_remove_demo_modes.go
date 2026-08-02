package brain

import (
	"os"
	"path/filepath"
	"strings"
)

// scaffoldDemoModeIDs 曾被 EnsureScaffold 误种的演示专职 mode（非项目演化）。
var scaffoldDemoModeIDs = []string{"coder", "architect", "qa", "script", "producer"}

const removedScaffoldDemoModesV1 = ".removed_scaffold_demo_modes_v1"

// 与曾写入磁盘的演示 persona 对齐（仅用于识别未改写的种子）。
var scaffoldDemoPersonaByID = map[string]string{
	"coder": `# Persona — coder

## Who I am

实现工程师：按 accepted 的需求/设计在产出区改代码，写清 impl 笔记。

## Preferences & taboos

- 只做实现与最小自检；不改 playbook / 其它 mode 文件
- 输出写到 Case artifact（若委托方指定）或产出区相对路径

`,
	"architect": `# Persona — architect

## Who I am

拆解与整体设计：把 requirements 变成可实现的 spec。

`,
	"qa": `# Persona — qa

## Who I am

测试与验收报告：对照 requirements/spec/impl 写 test_report。

`,
	"script": `# Persona — script

## Who I am

编剧：把 brief 细化成可拍的 script。

`,
	"producer": `# Persona — producer

## Who I am

成片制作：按 script 产出 cut / 成片路径说明。

`,
}

// maybeRemoveScaffoldDemoModesV1 幂等删除仍为演示种子的 mode。
// 不删用户已改写过的专职 mode。
func (w *Workspace) maybeRemoveScaffoldDemoModesV1() error {
	root := w.ProjectCataRoot()
	if root == "" {
		return nil
	}
	marker := filepath.Join(root, removedScaffoldDemoModesV1)
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	for _, id := range scaffoldDemoModeIDs {
		dir := w.ModeDir(id)
		if !isUnmodifiedScaffoldDemoMode(dir, id) {
			continue
		}
		_ = os.RemoveAll(dir)
	}

	// 若回迁前留下空壳 _orchestrator stub，一并清掉。
	orchStub := filepath.Join(root, DirModes, ModeAliasOrchestratorID)
	if isOrchestratorMigrateStub(orchStub) {
		_ = os.RemoveAll(orchStub)
	}

	return os.WriteFile(marker, []byte("v1\n"), 0644)
}

func isUnmodifiedScaffoldDemoMode(dir, id string) bool {
	want, ok := scaffoldDemoPersonaByID[id]
	if !ok {
		return false
	}
	personaPath := filepath.Join(dir, FilePersona)
	data, err := os.ReadFile(personaPath)
	if err != nil {
		return false
	}
	// 只认字节级未改写的演示种子；任何项目演化/手改都保留。
	return strings.TrimSpace(string(data)) == strings.TrimSpace(want)
}

func isOrchestratorMigrateStub(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, FilePersona))
	if err != nil {
		return false
	}
	body := string(data)
	return strings.Contains(body, "This mode directory is a stub") ||
		strings.Contains(body, "Use `modes/_default/`")
}
