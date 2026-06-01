package evolve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/brain"
	"cata/internal/clock"
)

// DocUpdate LLM 决策输出的文档补丁（路径相对当前 workspace 根）。
type DocUpdate struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Section string `json:"section,omitempty"`
	Content string `json:"content"`
}

// ApplyUpdates 将补丁写入指定 workspace 格子（禁止 global/*）。
func ApplyUpdates(ws *brain.Workspace, updates []DocUpdate) ([]string, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace required")
	}
	var touched []string
	for _, u := range updates {
		mode := strings.ToLower(strings.TrimSpace(u.Mode))
		if strings.TrimSpace(u.Content) == "" &&
			mode != "write" && mode != "overwrite" && mode != "delete" && mode != "delete_section" {
			continue
		}
		rel, err := brain.NormalizeEvolveUpdatePath(u.Path)
		if err != nil {
			return touched, err
		}
		if err := brain.RejectEvolveSharedGlobalPatch(rel); err != nil {
			return touched, err
		}
		abs, storeRel, err := brain.ResolveEvolveUpdateAbs(ws, rel)
		if err != nil {
			return touched, err
		}
		switch mode {
		case "delete":
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return touched, err
			}
		case "write", "overwrite":
			if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
				return touched, err
			}
			if err := os.WriteFile(abs, []byte(u.Content), 0644); err != nil {
				return touched, err
			}
		case "append", "":
			if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
				return touched, err
			}
			f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return touched, err
			}
			_, err = f.WriteString("\n\n" + strings.TrimSpace(u.Content) + "\n")
			_ = f.Close()
			if err != nil {
				return touched, err
			}
		case "append_section", "replace_section":
			if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
				return touched, err
			}
			body, _ := os.ReadFile(abs)
			merged := mergeMarkdownSection(body, u.Section, u.Content)
			if err := os.WriteFile(abs, merged, 0644); err != nil {
				return touched, err
			}
		case "delete_section":
			if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
				return touched, err
			}
			body, _ := os.ReadFile(abs)
			merged := deleteMarkdownSection(body, u.Section)
			if err := os.WriteFile(abs, merged, 0644); err != nil {
				return touched, err
			}
		default:
			return touched, fmt.Errorf("unknown patch mode: %s", u.Mode)
		}
		touched = append(touched, storeRel)
	}
	return touched, nil
}

// TouchArchiveDay 创建当日 archive 占位。
func TouchArchiveDay() (string, error) {
	w, err := brain.MustActive()
	if err != nil {
		return "", err
	}
	rel := filepath.Join(brain.RelMemoryArchive, clock.Format("2006-01-02")+".md")
	abs := w.Path(rel)
	if _, err := os.Stat(abs); err == nil {
		return rel, nil
	}
	content := fmt.Sprintf("# %s\n\n", clock.Format("2006-01-02"))
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		return "", err
	}
	return rel, nil
}
