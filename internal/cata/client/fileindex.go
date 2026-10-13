package client

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// fileIndex 是产出区（工作目录）的文件路径索引，供 TUI 的 `@文件` 补全使用。
//
// 设计要点：
//   - 惰性构建：首次需要候选时才扫描，避免 TUI 启动时卡顿（大仓库扫描可秒级）。
//   - 缓存 + TTL：扫描结果缓存，超过 ttl 后下次访问重建；也可 ForceRefresh 手动刷新。
//   - 忽略噪音：.git/.cata/node_modules/vendor/__pycache__ 等目录、点文件、二进制/大文件不索引。
//   - 并发安全：TUI 单 goroutine 访问，但仍加锁以便测试与未来复用。
type fileIndex struct {
	root string

	mu       sync.Mutex
	files    []string // 相对 root 的斜杠路径，已排序
	builtAt  time.Time
	ttl      time.Duration
	maxFiles int
}

// 默认索引上限：超出后截断（避免巨型仓库把内存/渲染拖垮）。
const (
	defaultIndexTTL      = 5 * time.Minute
	defaultIndexMaxFiles = 20000
)

// ignoreDirs 是扫描时整棵跳过的目录名（按 basename 匹配，任意层级生效）。
var ignoreDirs = map[string]bool{
	".git":          true,
	".hg":           true,
	".svn":          true,
	".cata":         true,
	".codegraph":    true,
	".idea":         true,
	".vscode":       true,
	".cursor":       true,
	"node_modules":  true,
	"vendor":        true,
	"dist":          true,
	"build":         true,
	"target":        true,
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".next":         true,
	".cache":        true,
	"out":           true,
}

// ignoreExts 是扫描时跳过的文件扩展名（二进制 / 归档 / 媒体等，不适合作为文本附件）。
var ignoreExts = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".a": true, ".o": true, ".obj": true,
	".class": true, ".jar": true, ".pyc": true, ".pyo": true,
	".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".webp": true, ".ico": true, ".tiff": true,
	".mp3": true, ".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".wav": true, ".flac": true,
	".pdf": true, ".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".lock": true, ".sum": true,
}

// maxIndexFileBytes 是单个文件被索引的大小上限（超过则视为大文件不索引）。
const maxIndexFileBytes = 4 << 20 // 4 MiB

func newFileIndex(root string) *fileIndex {
	return &fileIndex{root: root, ttl: defaultIndexTTL, maxFiles: defaultIndexMaxFiles}
}

// Files 返回索引中的相对路径列表；必要时（首次 / 过期）重建。
func (fi *fileIndex) Files() []string {
	fi.mu.Lock()
	defer fi.mu.Unlock()
	if fi.files == nil || time.Since(fi.builtAt) > fi.ttl {
		fi.files = fi.scanLocked()
		fi.builtAt = time.Now()
	}
	return fi.files
}

// Refresh 强制重建索引（`@` 菜单里的「刷新」或 /attach 场景可调用）。
func (fi *fileIndex) Refresh() []string {
	fi.mu.Lock()
	defer fi.mu.Unlock()
	fi.files = fi.scanLocked()
	fi.builtAt = time.Now()
	return fi.files
}

// scanLocked 遍历 root 收集候选文件。调用方须持有 fi.mu。
func (fi *fileIndex) scanLocked() []string {
	root := fi.root
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if root == "" {
		return nil
	}
	out := make([]string, 0, 256)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// 权限等错误不阻断整次扫描，跳过该条目。
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path == root {
				return nil
			}
			if ignoreDirs[strings.ToLower(name)] {
				return fs.SkipDir
			}
			return nil
		}
		// 跳过点文件（.DS_Store、.env 等）与噪音扩展名。
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if ignoreExts[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && info.Size() > maxIndexFileBytes {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= fi.maxFiles {
			return fs.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// matchFiles 按子串（不区分大小写）过滤候选路径，并做简单排序：
// 前缀命中 > 文件名命中 > 路径命中；同级按路径字典序。
func matchFiles(files []string, query string, limit int) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		if limit > 0 && len(files) > limit {
			return files[:limit]
		}
		return files
	}
	type scored struct {
		path string
		rank int
	}
	var hits []scored
	for _, f := range files {
		lf := strings.ToLower(f)
		base := lf
		if i := strings.LastIndex(lf, "/"); i >= 0 {
			base = lf[i+1:]
		}
		rank := -1
		switch {
		case strings.HasPrefix(base, q):
			rank = 0
		case strings.HasPrefix(lf, q):
			rank = 1
		case strings.Contains(base, q):
			rank = 2
		case strings.Contains(lf, q):
			rank = 3
		}
		if rank >= 0 {
			hits = append(hits, scored{path: f, rank: rank})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		return hits[i].path < hits[j].path
	})
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.path)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
