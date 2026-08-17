package brain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// 消费侧记忆检索：主 chat 每轮出站前，按当前请求检索最相关的长期记忆片段，
// 作为高权重 system 块注入（紧跟 boot-leader）。解决「evolve 记了但后续用不上」——
// 记忆从「全量摘要 + 靠模型自觉 read_file」升级为「按需检索 + 精确注入」。

const (
	// RetrievedMemorySystemPrefix 检索注入块的 system 前缀（幂等检测用）。
	RetrievedMemorySystemPrefix = "【Cata 相关记忆】"
	maxRetrievedHits            = 4
	maxRetrievedSnippetBytes    = 420
	maxRetrievedBlockBytes      = 2200
)

// MemoryHit 按 query 检索出的一条相关记忆。
type MemoryHit struct {
	Source   string `json:"source"`
	Summary  string `json:"summary,omitempty"`
	Category string `json:"category,omitempty"`
	Priority int    `json:"priority"`
	Score    int    `json:"score"`
	Snippet  string `json:"snippet"`
}

// RetrievedMemorySystemBlock 组装检索注入块；无命中或非主 chat 档位返回空。
// RetrievedMemorySystemBlock 组装检索注入块，并返回命中的记忆 source 列表（供命中观测/评估）。
func RetrievedMemorySystemBlock(ctx context.Context, p PromptProfile, query string) (string, []string) {
	if ProfileRank(p) < 1 {
		return "", nil // worker/minimal 不检索
	}
	cc := ChatContextFrom(ctx)
	if cc == nil || cc.WS == nil {
		return "", nil
	}
	hits := RetrieveRelevantMemory(cc.WS, query, maxRetrievedHits)
	if len(hits) == 0 {
		return "", nil
	}
	sources := make([]string, 0, len(hits))
	for _, h := range hits {
		sources = append(sources, h.Source)
	}
	var b strings.Builder
	b.WriteString(RetrievedMemorySystemPrefix)
	b.WriteString("\n\n> 与当前请求相关的长期记忆片段（按需命中）；全文用 `read_file` 展开。\n")
	for _, h := range hits {
		snippet := strings.TrimSpace(h.Snippet)
		if snippet == "" {
			snippet = strings.TrimSpace(h.Summary)
		}
		if snippet == "" {
			continue
		}
		if b.Len() > maxRetrievedBlockBytes {
			break
		}
		fmt.Fprintf(&b, "\n### [%s] %s\n%s\n", h.Category, h.Source, snippet)
	}
	if b.Len() == 0 {
		return "", nil
	}
	return strings.TrimSpace(b.String()), sources
}

// RetrieveRelevantMemory 从记忆索引检索与 query 最相关的条目，并展开源文件相关片段。
func RetrieveRelevantMemory(w *Workspace, query string, k int) []MemoryHit {
	if w == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	idx, err := LoadMemoryIndexFor(w)
	if err != nil || len(idx.Entries) == 0 {
		return nil
	}
	qTokens := tokenizeForRetrieval(query)
	if len(qTokens) == 0 {
		return nil
	}

	type scored struct {
		e IndexEntry
		s int
	}
	var hits []scored
	for _, e := range idx.Entries {
		if !isRetrievableSource(e.Source) {
			continue
		}
		s := scoreEntry(qTokens, e)
		if s <= 0 {
			continue
		}
		hits = append(hits, scored{e, s})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].s != hits[j].s {
			return hits[i].s > hits[j].s
		}
		return hits[i].e.Priority > hits[j].e.Priority
	})
	if k <= 0 || k > maxRetrievedHits {
		k = maxRetrievedHits
	}
	if len(hits) > k {
		hits = hits[:k]
	}

	out := make([]MemoryHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, MemoryHit{
			Source:   h.e.Source,
			Summary:  h.e.Summary,
			Category: h.e.Category,
			Priority: h.e.Priority,
			Score:    h.s,
			Snippet:  snippetForSource(w, h.e.Source, qTokens, maxRetrievedSnippetBytes),
		})
	}
	return out
}

// isRetrievableSource 只检索未在别处全文/摘要注入的资产：
// memory/long、memory/archive、skills。persona/behavior/persona.local 由
// 【项目内容】/【记忆索引】专门注入，不重复。
func isRetrievableSource(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "brain/")
	switch {
	case strings.HasPrefix(rel, "memory/long/"):
		return true
	case strings.HasPrefix(rel, "memory/archive/"):
		return true
	case strings.HasPrefix(rel, "skills/"):
		return true
	default:
		return false
	}
}

// scoreEntry 关键词重叠 + 优先级 + 类别加权。命中 0 个 token 直接 0 分。
func scoreEntry(qTokens []string, e IndexEntry) int {
	entryText := strings.Join(e.Keywords, " ") + " " + e.Summary + " " + e.Source
	set := make(map[string]bool)
	for _, t := range tokenizeForRetrieval(entryText) {
		set[t] = true
	}
	overlap := 0
	for _, t := range qTokens {
		if set[t] {
			overlap++
		}
	}
	if overlap == 0 {
		return 0
	}
	score := overlap*10 + e.Priority
	switch e.Category {
	case "procedure", "preference":
		score += 2 // 流程/偏好对执行更有价值
	}
	return score
}

// snippetForSource 读取命中 source 的物理文件，截取与 query 最相关的一节；无命中回退头部。
func snippetForSource(w *Workspace, source string, qTokens []string, maxBytes int) string {
	abs, err := ResolveBrainDocAbs(w, source)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(abs)
	if err != nil || len(data) == 0 {
		return ""
	}
	body := CompactExcessiveNewlines(string(data))
	if sec := bestMatchingSection(body, qTokens); sec != "" {
		return truncateBytes(sec, maxBytes)
	}
	return truncateBytes(headContent(body), maxBytes)
}

// bestMatchingSection 按 ## / ### 切节，返回与 query token 重叠最多的一节；无命中返回空。
func bestMatchingSection(body string, qTokens []string) string {
	sections := splitMarkdownSections(body)
	best := ""
	bestScore := 0
	for _, sec := range sections {
		s := overlapScore(sec, qTokens)
		if s > bestScore {
			bestScore = s
			best = sec
		}
	}
	if bestScore == 0 {
		return ""
	}
	return best
}

func overlapScore(text string, qTokens []string) int {
	set := make(map[string]bool)
	for _, t := range tokenizeForRetrieval(text) {
		set[t] = true
	}
	n := 0
	for _, t := range qTokens {
		if set[t] {
			n++
		}
	}
	return n
}

// splitMarkdownSections 按标题行（## 或 ###）切分，保留标题。
func splitMarkdownSections(body string) []string {
	lines := strings.Split(body, "\n")
	var out []string
	var cur []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "### ") {
			if len(cur) > 0 {
				out = append(out, strings.Join(cur, "\n"))
			}
			cur = []string{ln}
			continue
		}
		cur = append(cur, ln)
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, "\n"))
	}
	return out
}

// headContent 返回正文开头一段（跳过标题行与引用提示行）。
func headContent(body string) string {
	lines := strings.Split(body, "\n")
	var keep []string
	started := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !started {
			if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ">") {
				continue
			}
			started = true
		}
		keep = append(keep, ln)
		if len(strings.Join(keep, "\n")) >= 600 {
			break
		}
	}
	return strings.Join(keep, "\n")
}

// tokenizeForRetrieval 中英文混排分词：中文按相邻字符 bigram，英文/数字按词。
// 与 index.json 里 extractKeywords 的「整段中文」不同——检索侧不依赖 keywords 粒度，
// 而是对 query 与 entry 文本统一 bigram 化后做重叠，中文也能命中。
func tokenizeForRetrieval(s string) []string {
	s = strings.ToLower(s)
	var tokens []string
	var word []rune // 连续英文/数字
	var han []rune  // 连续中文
	flushWord := func() {
		if len(word) >= 2 {
			tokens = append(tokens, string(word))
		}
		word = word[:0]
	}
	flushHan := func() {
		if len(han) >= 2 {
			for i := 0; i+1 < len(han); i++ {
				tokens = append(tokens, string(han[i])+string(han[i+1]))
			}
		} else if len(han) == 1 {
			tokens = append(tokens, string(han[0]))
		}
		han = han[:0]
	}
	for _, r := range s {
		switch {
		case isHanRune(r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			flushHan()
			word = append(word, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return dedupeStrings(tokens)
}

func isHanRune(r rune) bool {
	return r >= 0x4e00 && r <= 0x9fff
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func truncateBytes(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" || max <= 0 {
		return s
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}
