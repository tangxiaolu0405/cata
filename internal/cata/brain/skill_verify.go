package brain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cata/internal/cata/clock"
)

// SkillVerifyResult 固化后自动跑验证的结果。
type SkillVerifyResult struct {
	SkillID string
	OK      bool
	Mode    string // structural | verify_entry | entry
	Output  string
	Err     string
}

// VerifySkill 结构检查 + 按 manifest 声明自动跑（不推断语言、不默认 Python）。
//
// 规则：
// 1. manifest + entry 必须存在，且 runner 必须显式声明（禁止默认 python）
// 2. 若有 verify_entry：用同一 runner 跑 verify_entry
// 3. 否则用 manifest.runner + entry 跑一次（params 含 mode=verify）
func VerifySkill(ctx context.Context, skillID string) SkillVerifyResult {
	res := SkillVerifyResult{SkillID: skillID, Mode: "structural"}
	dir, err := ResolveSkillDir(skillID)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	manifest, err := LoadSkillManifest(dir)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	runner := strings.TrimSpace(manifest.Runner)
	if runner == "" {
		res.Err = "manifest runner is required (do not assume a language)"
		return res
	}
	entry := filepath.Join(dir, manifest.Entry)
	if _, err := os.Stat(entry); err != nil {
		res.Err = fmt.Sprintf("entry missing: %v", err)
		return res
	}

	params := map[string]interface{}{"mode": "verify"}
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	verifyRel := strings.TrimSpace(manifest.VerifyEntry)
	if verifyRel != "" {
		res.Mode = "verify_entry"
		out, err := runSkillEntry(runCtx, dir, &SkillManifest{
			Runner: runner,
			Entry:  verifyRel,
		}, params)
		res.Output = out
		if err != nil {
			res.Err = err.Error()
			return res
		}
		res.OK = true
		return res
	}

	res.Mode = "entry"
	out, err := RunSkill(runCtx, RunSkillArgs{Skill: skillID, Params: params})
	res.Output = out
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.OK = true
	return res
}

func runSkillEntry(ctx context.Context, dir string, manifest *SkillManifest, params map[string]interface{}) (string, error) {
	entry := filepath.Join(dir, manifest.Entry)
	if _, err := os.Stat(entry); err != nil {
		return "", fmt.Errorf("verify entry: %w", err)
	}
	wd, err := ExecWorkingDir()
	if err != nil {
		return "", err
	}
	argv, err := buildSkillArgv(manifest.Runner, entry, params)
	if err != nil {
		return "", err
	}
	text, err := runSkillCmd(ctx, wd, argv)
	if err != nil {
		return text, err
	}
	return text, nil
}

// QuarantineSkill 验证失败：移出 capabilities，目录迁到 skills/.failed/，留 VERIFY_FAILED.md 供重写。
func QuarantineSkill(w *Workspace, skillID, reason, output string) error {
	if w == nil {
		return fmt.Errorf("no workspace")
	}
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return fmt.Errorf("empty skill id")
	}
	_ = RemoveSkillFromCapabilities(w, skillID)

	src := w.SkillDir(skillID)
	if _, err := os.Stat(src); err != nil {
		return err
	}
	failedRoot := filepath.Join(w.ProjectCataRoot(), DirSkills, ".failed")
	if err := os.MkdirAll(failedRoot, 0755); err != nil {
		return err
	}
	stamp := clock.Now().Format("20060102-150405")
	dest := filepath.Join(failedRoot, fmt.Sprintf("%s-%s", skillID, stamp))
	note := fmt.Sprintf("# verify failed\n\nskill: %s\nreason: %s\n\n## output\n\n```\n%s\n```\n\nRewrite this skill (crystallize again) after fixing the failure.\n",
		skillID, reason, truncateRunes(output, 4000))
	_ = os.WriteFile(filepath.Join(src, "VERIFY_FAILED.md"), []byte(note), 0644)
	if err := os.Rename(src, dest); err != nil {
		return fmt.Errorf("quarantine rename: %w", err)
	}
	return nil
}

// EnableAndVerifySkill 追加 capabilities 后立刻验证；失败则回退隔离。
func EnableAndVerifySkill(ctx context.Context, w *Workspace, skillID string) SkillVerifyResult {
	if err := AppendSkillToCapabilities(w, skillID); err != nil {
		return SkillVerifyResult{SkillID: skillID, Err: err.Error()}
	}
	prev := Active()
	SetActive(w)
	defer SetActive(prev)

	res := VerifySkill(ctx, skillID)
	if res.OK {
		return res
	}
	reason := res.Err
	if reason == "" {
		reason = "verify failed"
	}
	if err := QuarantineSkill(w, skillID, reason, res.Output); err != nil {
		res.Err = fmt.Sprintf("%s; quarantine: %v", reason, err)
	}
	return res
}
