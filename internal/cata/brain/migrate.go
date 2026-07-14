package brain

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/clock"
	"cata/internal/cata/config"
)

// MigrateLegacyBrain 将旧版扁平 ~/.cata/brain/{hot,core,memory,...} 迁入首个 workspace。
func MigrateLegacyBrain() error {
	entries, err := ListRegistryEntries()
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return nil
	}

	legacyHot := filepath.Join(brainRoot(), RelPathHot)
	legacyShort := filepath.Join(brainRoot(), RelPathShortTermCurrent)
	if _, err := os.Stat(legacyHot); os.IsNotExist(err) {
		if _, err2 := os.Stat(legacyShort); os.IsNotExist(err2) {
			return nil
		}
	}

	root := fallbackWorkspaceRoot()
	root, _ = filepath.Abs(root)
	kind := KindEphemeral
	if findGitRoot(root) == root {
		kind = KindGit
	} else if findMarkedRoot(root) == root {
		kind = KindMarked
	}
	ws := &Workspace{
		ID:         workspaceID(root),
		RootPath:   root,
		Kind:       kind,
		ActiveMode: ModeDefaultID,
	}
	if err := ws.EnsureScaffold(); err != nil {
		return err
	}
	now := clock.RFC3339()
	if err := upsertRegistryEntry(RegistryEntry{
		ID: ws.ID, RootPath: ws.RootPath, Kind: ws.Kind,
		CreatedAt: now, LastSeenAt: now, ActiveMode: ws.ActiveMode,
	}); err != nil {
		return err
	}

	_ = copyIfExists(legacyHot, ws.PersonaPath())
	_ = copyIfExists(legacyShort, ws.ShortTermPath())
	_ = copyTreeIfExists(filepath.Join(brainRoot(), RelPathLongTerm), ws.LongTermDir())
	_ = copyIfExists(filepath.Join(brainRoot(), RelPathEvolutionLog), ws.EvolutionLogPath())
	_ = copyTreeIfExists(filepath.Join(brainRoot(), RelPathArchive), ws.ArchiveDir())
	return nil
}

func fallbackWorkspaceRoot() string {
	_ = config.InitBrainPath()
	if b := config.GetBrainBaseDir(); b != "" {
		if gitRoot := findGitRoot(b); gitRoot != "" {
			return gitRoot
		}
		return b
	}
	if r := findGitRoot(mustGetwd()); r != "" {
		return r
	}
	return mustGetwd()
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	if wd == "" {
		return CataHome()
	}
	return wd
}

// MigrateWorkspaceNaming 将旧 ws_<hash> 命名的工作区目录迁移到可读命名。
func MigrateWorkspaceNaming() error {
	root := workspacesRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "ws_") {
			continue
		}
		oldDir := filepath.Join(root, e.Name())
		metaPath := filepath.Join(oldDir, RelMetaJSON)
		meta := readWorkspaceMeta(metaPath)
		if meta == nil || meta["root_path"] == "" {
			continue
		}

		newID := workspaceID(meta["root_path"])
		if newID == e.Name() {
			continue
		}
		newDir := filepath.Join(root, newID)
		if _, err := os.Stat(newDir); err == nil {
			if err := os.RemoveAll(oldDir); err != nil {
				log.Printf("migrate: remove old workspace dir %s: %v", oldDir, err)
				continue
			}
			log.Printf("migrate: removed old workspace %s (new %s already exists)", e.Name(), newID)
			continue
		}

		if err := os.Rename(oldDir, newDir); err != nil {
			log.Printf("migrate: rename %s → %s: %v", e.Name(), newID, err)
			continue
		}

		// update meta.json id
		meta["id"] = newID
		updateWorkspaceMeta(metaPath, meta)

		// update registry
		updateRegistryID(e.Name(), newID)

		// update .cata/workspace.link in project
		if rootPath := meta["root_path"]; rootPath != "" {
			updateProjectLink(rootPath, newID)
		}

		log.Printf("migrate: renamed workspace %s → %s", e.Name(), newID)
	}
	return nil
}

func readWorkspaceMeta(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func updateWorkspaceMeta(path string, m map[string]string) {
	data, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(path, data, 0644)
}

func updateRegistryID(oldID, newID string) {
	rf, err := loadRegistry()
	if err != nil {
		return
	}
	for i := range rf.Workspaces {
		if rf.Workspaces[i].ID == oldID {
			rf.Workspaces[i].ID = newID
		}
	}
	_ = saveRegistry(rf)
}

func updateProjectLink(rootPath, newID string) {
	linkPath := filepath.Join(rootPath, ProjectCataDir, FileWorkspaceLink)
	body := "id: " + newID + "\n"
	_ = os.WriteFile(linkPath, []byte(body), 0644)
}

func copyIfExists(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0755)
	return os.WriteFile(dst, data, 0644)
}

func copyTreeIfExists(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		return copyIfExists(path, filepath.Join(dst, rel))
	})
}
