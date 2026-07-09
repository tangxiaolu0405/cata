package brain

import (
	"context"
	"fmt"
	"strings"

	"cata/internal/config"
)

const defaultSkillReadMaxBytes = 512 * 1024

// ReadSkillArgs read_skill 工具参数。
type ReadSkillArgs struct {
	Skill string `json:"skill"`
}

// SkillReadMaxBytes 返回 SKILL.md 读取上限（与 read_file 配置对齐）。
func SkillReadMaxBytes() int {
	maxRead := defaultSkillReadMaxBytes
	if config.Config != nil && config.Config.WorkspaceFiles.MaxReadBytes > 0 {
		maxRead = config.Config.WorkspaceFiles.MaxReadBytes
	}
	return maxRead
}

// ReadSkill 按 skill id 加载完整 SKILL.md（workspace → ~/.cata/skills → ~/.cursor/skills-cursor）。
func ReadSkill(ctx context.Context, args ReadSkillArgs) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	skill := strings.TrimSpace(args.Skill)
	if skill == "" {
		return "", fmt.Errorf("read_skill: skill required")
	}
	body, path, err := loadSkillMarkdown(skill)
	if err != nil {
		return "", err
	}
	maxRead := SkillReadMaxBytes()
	truncated := false
	if len(body) > maxRead {
		body = body[:maxRead] + "\n…(truncated by max_read_bytes)"
		truncated = true
	}
	shown := len(body)
	if truncated {
		shown = maxRead
	}
	return fmt.Sprintf("read_skill %s resolved=%s (%d bytes shown)\n%s", skill, path, shown, body), nil
}
