package llm

import (
	"embed"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"cata/internal/cata/brain"
)

// RoleCard 一张角色卡片：一个 AgentRole 的静态身份 + 协议 + 采样 + 注入策略。
// 卡片是编译期 embed 的单一真相；调用点只说「我是哪个角色」，统一按卡片组装。
//
// 与「引导层」（guidance/constraints.md、behavior.md、delegate-guide.md）的区别：
// 引导层是全机共享的环境规则与委派 SOP（运行时可改、含动态占位符），
// 角色卡片是某个角色的「我是谁 + 协作协议」（静态，随版本发布）。

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

var roleCardCache sync.Map // Role -> *RoleCard

// CardForRole 加载某角色的卡片（带缓存）。
func CardForRole(role Role) (*RoleCard, error) {
	if role == "" {
		role = RoleDefault
	}
	if v, ok := roleCardCache.Load(role); ok {
		if c, ok := v.(*RoleCard); ok {
			return c, nil
		}
	}
	card, err := loadRoleCard(role)
	if err != nil {
		return nil, err
	}
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

func loadRoleCard(role Role) (*RoleCard, error) {
	fname := roleCardFilename(role) + ".md"
	data, err := roleCardsFS.ReadFile("rolecards/" + fname)
	if err != nil {
		return nil, fmt.Errorf("role card %q not found: %w", role, err)
	}
	meta, body := parseRoleCard(string(data))
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
	return card, nil
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
