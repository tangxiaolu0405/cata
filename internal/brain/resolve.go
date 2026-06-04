package brain

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"cata/internal/clock"
	"cata/internal/config"
)

// ResolveWorkspace 用产出区 cwd 解析脑子分区（focus_path），并设置 exec 产出目录。
func ResolveWorkspace(clientCwd string) (*Workspace, error) {
	if err := EnsureCataLayout(); err != nil {
		return nil, err
	}
	out := strings.TrimSpace(clientCwd)
	if out == "" {
		var err error
		out, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	outputCwd, err := filepath.Abs(out)
	if err != nil {
		return nil, err
	}
	SetOutputCwd(outputCwd)
	syncOutputDir(outputCwd)

	focus, kind, err := resolveFocusPath(outputCwd)
	if err != nil {
		return nil, err
	}
	root := focus

	if ent, err := findRegistryByRoot(root); err != nil {
		return nil, err
	} else if ent != nil {
		ws := entryToWorkspace(ent)
		ws.Kind = kind
		y := readProjectWorkspaceYAML(root)
		if y.Name != "" {
			ws.Name = y.Name
		}
		if y.ActiveMode != "" {
			ws.ActiveMode = NormalizeModeID(y.ActiveMode)
		}
		_ = ws.EnsureScaffold()
		touchRegistryEntry(ent.ID)
		_ = upsertRegistryEntry(workspaceToEntry(ws))
		SetActive(ws)
		log.Printf("cata binding: %s", LogBinding())
		return ws, nil
	}

	y := readProjectWorkspaceYAML(root)
	ws := &Workspace{
		ID:         workspaceID(root),
		RootPath:   root,
		Kind:       kind,
		Name:       y.Name,
		ActiveMode: NormalizeModeID(y.ActiveMode),
	}
	if ws.ActiveMode == "" {
		ws.ActiveMode = ModeDefaultID
	}
	if err := ws.EnsureScaffold(); err != nil {
		return nil, err
	}
	now := clock.RFC3339()
	if err := upsertRegistryEntry(RegistryEntry{
		ID:         ws.ID,
		RootPath:   ws.RootPath,
		Kind:       ws.Kind,
		Name:       ws.Name,
		CreatedAt:  now,
		LastSeenAt: now,
		ActiveMode: ws.ActiveMode,
	}); err != nil {
		return nil, err
	}
	SetActive(ws)
	log.Printf("cata binding: %s", LogBinding())
	return ws, nil
}

// syncOutputDir 将 config 的 brain.base_dir 设为产出区（run_command cwd）。
func syncOutputDir(outputCwd string) {
	if config.Config != nil {
		config.Config.Brain.BaseDir = outputCwd
	}
	config.BrainBaseDir = outputCwd
}

// workspaceID 从 root 路径生成可读标识（参考 Claude 的 ~/.claude/projects/<id> 命名）。
// Windows: D--project-mybot
// Unix:    home-user-project
func workspaceID(root string) string {
	root = filepath.Clean(root)
	vol := filepath.VolumeName(root)
	rest := root
	if vol != "" {
		rest = root[len(vol):]
	}
	rest = strings.Trim(rest, "/\\")

	var parts []string
	if vol != "" {
		vol = strings.TrimRight(vol, ":")
		parts = append(parts, vol)
	}
	if rest != "" {
		seg := strings.ReplaceAll(rest, "/", "-")
		seg = strings.ReplaceAll(seg, "\\", "-")
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return "root"
	}

	sep := "--"
	if vol == "" {
		sep = "-"
	}
	raw := parts[0]
	if len(parts) > 1 {
		raw += sep + parts[1]
	}

	raw = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, raw)
	for strings.Contains(raw, "--") {
		raw = strings.ReplaceAll(raw, "--", "-")
	}
	raw = strings.ToLower(raw)
	raw = strings.Trim(raw, "-")
	if raw == "" {
		return "root"
	}
	return raw
}

func resolveFocusPath(cwd string) (focus string, kind WorkspaceKind, err error) {
	if strings.TrimSpace(cwd) == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return "", "", err
	}
	if gitRoot := findGitRoot(cwd); gitRoot != "" {
		return gitRoot, KindGit, nil
	}
	if marked := findMarkedRoot(cwd); marked != "" {
		return marked, KindMarked, nil
	}
	return cwd, KindEphemeral, nil
}

func findGitRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func findMarkedRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ProjectCataDir, FileWorkspaceYAML)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func entryToWorkspace(e *RegistryEntry) *Workspace {
	return &Workspace{
		ID:         e.ID,
		RootPath:   e.RootPath,
		Kind:       e.Kind,
		Name:       e.Name,
		ActiveMode: NormalizeModeID(e.ActiveMode),
	}
}

func workspaceToEntry(w *Workspace) RegistryEntry {
	return RegistryEntry{
		ID:         w.ID,
		RootPath:   w.RootPath,
		Kind:       w.Kind,
		Name:       w.Name,
		ActiveMode: NormalizeModeID(w.ActiveMode),
		LastSeenAt: clock.RFC3339(),
	}
}

// ListWorkspaces 返回所有已注册工作区（用于演进轮询）。
func ListWorkspaces() ([]*Workspace, error) {
	entries, err := ListRegistryEntries()
	if err != nil {
		return nil, err
	}
	out := make([]*Workspace, len(entries))
	for i := range entries {
		ws := entryToWorkspace(&entries[i])
		out[i] = ws
	}
	return out, nil
}
