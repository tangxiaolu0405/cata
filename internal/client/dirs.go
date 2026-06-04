package client

import "path/filepath"

// ParseOutputDirs extracts absolute paths from `--dir <path>` flags (chat client).
func ParseOutputDirs(args []string) []string {
	var dirs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--dir" && i+1 < len(args) {
			if abs, err := filepath.Abs(args[i+1]); err == nil {
				dirs = append(dirs, abs)
			}
			i++
		}
	}
	return dirs
}
