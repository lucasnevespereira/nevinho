package tui

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
)

// fileListLimit caps how many paths the @-mention picker offers. Repos
// with millions of files would otherwise stall the picker open.
const fileListLimit = 2000

// listProjectFiles returns paths under cwd suitable for the @-mention
// picker. git ls-files is preferred because it already honours
// .gitignore; the walk fallback handles non-git directories.
func listProjectFiles(cwd string) []string {
	if paths, ok := gitLsFiles(cwd); ok {
		if len(paths) > fileListLimit {
			paths = paths[:fileListLimit]
		}
		return paths
	}
	return walkProject(cwd)
}

// gitLsFiles asks git for the tracked plus untracked-not-ignored files.
// The second return reports whether git was usable here at all.
func gitLsFiles(cwd string) ([]string, bool) {
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, true
	}
	return lines, true
}

// walkProject is the non-git fallback. It skips common heavy directories
// so a stray node_modules does not balloon the list.
func walkProject(cwd string) []string {
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"dist": true, "build": true, ".next": true, "target": true,
	}
	var paths []string
	filepath.WalkDir(cwd, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(cwd, p)
		if err != nil {
			return nil
		}
		paths = append(paths, rel)
		if len(paths) >= fileListLimit {
			return filepath.SkipAll
		}
		return nil
	})
	return paths
}

// fileMentionItems wraps the project file list as selector rows.
func fileMentionItems(cwd string) []selectorItem {
	paths := listProjectFiles(cwd)
	items := make([]selectorItem, len(paths))
	for i, p := range paths {
		items[i] = selectorItem{label: p, value: p}
	}
	return items
}
