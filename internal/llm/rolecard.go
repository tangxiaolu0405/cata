package llm

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"cata/internal/cata/brain"
)

// RoleCard 一张角色卡片：一个 AgentRole 的静态身份 + 协议 + 采样 + 注入策略。
// 卡片分两层：embed 内置默认（rolecards/*.md，随二进制）+ 运行时覆盖
// （~/.cata/global/roles/<role>.md，用户可编辑、立即生效，无需重编）。
//
// 与「引导层」（guidance/constraints.md、behavior.md、delegate-guide.md）的区别：
// 引导层是全机共享的环境规则与委派 SOP（运行时可改、含动态占位符），
// 角色卡片是某个角色的「我是谁 + 协作协议」。

//go:embed rolecards/*.md
var roleCardsFS embed.FS

// InjectMode 角色默认的 system 注入档位。
type InjectMode string

const (
	InjectOff     InjectMode = "off"     // 不注入 boot/brain/检索：卡片 Body 即完整 system（evolve）
	InjectMinimal InjectMode = "minimal" // 只注入路径块（worker）
	InjectTask    InjectMode = "task"    // 路径 + skills/modes/记忆索引 + 项目内容节选
	InjectFull    InjectMode = "full"    // 全量（主 chat）
)

// RoleCard 角色卡片（元数据来自 front-matter，正文来自 body）。
type RoleCard struct {
	Role            Role
	Temperature     float64
	DisableThinking bool
	Inject          InjectMode
	Body            string
}

// InjectProfile 返回 Inject 对应的 PromptProfile（off 无意义，返回空）。
func (c RoleCard) InjectProfile() brain.PromptProfile {
	switch c.Inject {
	case InjectMinimal:
		return brain.PromptProfileMinimal
	case InjectTask:
		return brain.PromptProfileTask
	default:
		return brain.PromptProfileFull
	}
}

// BodyBeforeSection 返回 "## "+title 之前的正文（不含该节）。无该节则返回全文。
func (c RoleCard) BodyBeforeSection(title string) string {
	idx := indexOfSection(c.Body, title)
	if idx < 0 {
		return strings.TrimSpace(c.Body)
	}
	return strings.TrimSpace(c.Body[:idx])
}

// Section 返回 "## "+title 开头的节（含标题，到下一个同级 ## 前）。无该节返回空。
func (c RoleCard) Section(title string) string {
	idx := indexOfSection(c.Body, title)
	if idx < 0 {
		return ""
	}
	rest := c.Body[idx:]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		return strings.TrimSpace(rest[:next])
	}
	return strings.TrimSpace(rest)
}

// indexOfSection 定位 "## "+title 的节标题位置（必须位于行首）。
func indexOfSection(body, title string) int {
	target := "## " + title
	idx := strings.Index(body, target)
	if idx < 0 {
		return -1
	}
	if idx > 0 && body[idx-1] != '\n' {
		return -1
	}
	return idx
}

var roleCardCache sync.Map // Role -> *RoleCard（仅缓存 embed 兜底）

// roleCardsRuntimeDir 角色卡片运行时覆盖目录：~/.cata/global/roles/。
func roleCardsRuntimeDir() string {
	return filepath.Join(brain.CataHome(), "global", "roles")
}

func runtimeRoleCardPath(role Role) string {
	return filepath.Join(roleCardsRuntimeDir(), roleCardFilename(role)+".md")
}

// roleCardSeedVersion 内置角色卡片的种子版本。内置卡片 front-matter 里的 seed_version
// 小于此值时，EnsureRoleCards 会用内置模板覆盖运行时文件（视为「旧 seed 未被用户编辑」）。
// 用户编辑后删除 front-matter 的 seed_version 行，即可阻止覆盖。
const roleCardSeedVersion = 1

// EnsureRoleCards 把内置角色卡片模板 seed 到 ~/.cata/global/roles/。
// - 文件不存在 → 写入内置模板；
// - 文件存在但 seed_version 小于当前内置版本（旧 seed）→ 覆盖；
// - 文件存在且无 seed_version（用户已编辑）→ 保留不覆盖。
func EnsureRoleCards() error {
	for _, role := range []Role{RoleChat, RoleWorker, RoleEvolution} {
		dst := runtimeRoleCardPath(role)
		if data, err := os.ReadFile(dst); err == nil {
			if !shouldRefreshRoleCard(string(data)) {
				continue
			}
		}
		data, err := roleCardsFS.ReadFile("rolecards/" + roleCardFilename(role) + ".md")
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// shouldRefreshRoleCard 判断运行时角色卡片是否应被内置模板覆盖：
// 无 seed_version（用户编辑）→ 否；seed_version < roleCardSeedVersion → 是。
func shouldRefreshRoleCard(raw string) bool {
	meta, _ := parseRoleCard(raw)
	v, ok := meta["seed_version"]
	if !ok {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	return err == nil && n < roleCardSeedVersion
}

// CardForRole 加载某角色的卡片：运行时覆盖优先（每次读文件，立即生效），embed 兜底（缓存）。
func CardForRole(role Role) (*RoleCard, error) {
	if role == "" {
		role = RoleDefault
	}
	if p := runtimeRoleCardPath(role); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			return buildCardFromRaw(role, string(data)), nil
		}
	}
	if v, ok := roleCardCache.Load(role); ok {
		if c, ok := v.(*RoleCard); ok {
			return c, nil
		}
	}
	data, err := roleCardsFS.ReadFile("rolecards/" + roleCardFilename(role) + ".md")
	if err != nil {
		return nil, fmt.Errorf("role card %q not found: %w", role, err)
	}
	card := buildCardFromRaw(role, string(data))
	roleCardCache.Store(role, card)
	return card, nil
}

// roleCardFilename 角色 → 卡片文件名（Role 常量值不必等于文件名，如 evolution→evolve）。
func roleCardFilename(role Role) string {
	switch role {
	case RoleEvolution:
		return "evolve"
	default:
		return strings.ToLower(string(role))
	}
}

// buildCardFromRaw 从 front-matter + body 原文构建 RoleCard。
func buildCardFromRaw(role Role, raw string) *RoleCard {
	meta, body := parseRoleCard(raw)
	card := &RoleCard{
		Role: role,
		Body: strings.TrimSpace(body),
	}
	if v, ok := meta["temperature"]; ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			card.Temperature = f
		}
	}
	if v, ok := meta["disable_thinking"]; ok {
		card.DisableThinking = strings.EqualFold(strings.TrimSpace(v), "true") || v == "1"
	}
	if v, ok := meta["inject"]; ok {
		switch InjectMode(strings.ToLower(strings.TrimSpace(v))) {
		case InjectOff, InjectMinimal, InjectTask, InjectFull:
			card.Inject = InjectMode(strings.ToLower(strings.TrimSpace(v)))
		default:
			card.Inject = InjectFull
		}
	} else {
		card.Inject = InjectFull
	}
	if card.Temperature == 0 {
		card.Temperature = 0.7
	}
	return card
}

// parseRoleCard 解析 front-matter（--- 之间 key: value 行）与 body（其后）。
func parseRoleCard(raw string) (map[string]string, string) {
	meta := map[string]string{}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "---") {
		return meta, raw
	}
	rest := strings.TrimPrefix(raw, "---")
	idx := strings.Index(rest, "---")
	if idx < 0 {
		return meta, raw
	}
	front := rest[:idx]
	body := rest[idx+3:]
	for _, ln := range strings.Split(front, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.Index(ln, ":"); i > 0 {
			meta[strings.TrimSpace(ln[:i])] = strings.TrimSpace(ln[i+1:])
		}
	}
	return meta, body
}
