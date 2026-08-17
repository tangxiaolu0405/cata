package brain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxArchiveFiles memory/archive/ 下保留的 consolidated-*.md 数量上限。
// 归档冷流水的价值已在 consolidate 时沉淀进项目 persona/behavior/learnings，旧的可安全删除。
const maxArchiveFiles = 5

// PruneArchiveDir 清理 memory/archive/ 下旧的 consolidated-*.md，保留最近 keep 个。
// keep<=0 时用默认上限。归档文件名含时间戳（consolidated-YYYYMMDD-HHMMSS.md），字典序即时间序。
func PruneArchiveDir(w *Workspace, keep int) error {
	if w == nil {
		return nil
	}
	if keep <= 0 {
		keep = maxArchiveFiles
	}
	dir := w.ArchiveDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // 目录不存在或无权限，忽略
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "consolidated-") && strings.HasSuffix(name, ".md") {
			names = append(names, name)
		}
	}
	if len(names) <= keep {
		return nil
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(dir, name))
	}
	return nil
}
