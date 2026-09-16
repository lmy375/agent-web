// Package fsx is the filesystem work the shell does on the owner's behalf: the
// work-directory picker, cwd validation, and the @-mention file search. None of
// it is agent-specific.
package fsx

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one directory the picker can descend into.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Canonicalize resolves symlinks and makes the path absolute, the same way the
// harnesses do before hashing a transcript directory out of it.
func Canonicalize(path string) (string, error) {
	abs, err := filepath.Abs(os.ExpandEnv(expandHome(path)))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, err
	}
	return resolved, nil
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// ListDirs returns the visible sub-directories of path, name-sorted.
func ListDirs(path string) ([]Entry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, Entry{Name: e.Name(), Path: filepath.Join(path, e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// skipDirs are never walked for @-mentions: they are large, generated, and
// nothing in them is what the owner meant to type.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "venv": true, "dist": true,
	"build": true, "target": true, "__pycache__": true, ".next": true, ".cache": true,
	".mypy_cache": true, ".ruff_cache": true, ".pytest_cache": true, "vendor": true,
}

const searchWalkLimit = 20000

// Search ranks files under root against a typed query: a basename prefix beats
// a basename substring, which beats a path substring, and shorter paths win
// ties. Returns paths relative to root and whether the walk was cut short.
func Search(root, query string, limit int) ([]string, bool, error) {
	needle := strings.ToLower(query)
	type hit struct {
		path  string
		score int
	}
	hits := []hit{}
	seen := 0
	truncated := false

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > searchWalkLimit {
			truncated = true
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		lowerRel, lowerBase := strings.ToLower(rel), strings.ToLower(d.Name())
		switch {
		case strings.HasPrefix(lowerBase, needle):
			hits = append(hits, hit{rel, 0})
		case strings.Contains(lowerBase, needle):
			hits = append(hits, hit{rel, 1})
		case strings.Contains(lowerRel, needle):
			hits = append(hits, hit{rel, 2})
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		if len(hits[i].path) != len(hits[j].path) {
			return len(hits[i].path) < len(hits[j].path)
		}
		return hits[i].path < hits[j].path
	})
	if len(hits) > limit {
		hits, truncated = hits[:limit], true
	}
	paths := make([]string, 0, len(hits))
	for _, h := range hits {
		paths = append(paths, h.path)
	}
	return paths, truncated, nil
}
