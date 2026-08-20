package discover

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"wkt/internal/gitx"
	"wkt/internal/paths"
)

type Kind int

const (
	KindRepo Kind = iota
	KindLinkedWorktree
	KindSubmodule
	KindNested
)

type Entry struct {
	RelPath     string
	AbsPath     string
	Kind        Kind
	ContainedBy string
}

// Walk enumerates .git markers under workspace, never following symlinks.
func Walk(workspace string, maxDepth int) ([]Entry, error) {
	root, err := paths.Canonical(workspace)
	if err != nil {
		return nil, err
	}
	var out []Entry
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, do not abort the walk
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if strings.Count(rel, string(filepath.Separator)) >= maxDepth {
			return fs.SkipDir
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fs.SkipDir // never follow symlinks (spec §5.3 rule 4)
		}
		if d.Name() != ".git" {
			return nil
		}
		repoDir := filepath.Dir(p)
		repoRel, _ := filepath.Rel(root, repoDir)
		e := Entry{RelPath: filepath.ToSlash(repoRel), AbsPath: repoDir, Kind: classify(p, repoDir)}
		out = append(out, e)
		return fs.SkipDir // do not descend into a repository's own .git
	})
	if err != nil {
		return nil, err
	}
	markNested(out)
	return out, nil
}

func classify(gitMarker, repoDir string) Kind {
	info, err := os.Lstat(gitMarker)
	if err != nil {
		return KindRepo
	}
	if info.IsDir() {
		return KindRepo
	}
	// .git is a file: either a linked worktree or a submodule checkout.
	b, err := os.ReadFile(gitMarker)
	if err != nil {
		return KindRepo
	}
	target := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
	if strings.Contains(target, string(filepath.Separator)+"worktrees"+string(filepath.Separator)) {
		return KindLinkedWorktree
	}
	if strings.Contains(target, string(filepath.Separator)+"modules"+string(filepath.Separator)) {
		return KindSubmodule
	}
	if gitx.RunOK(repoDir, "rev-parse", "--git-common-dir") {
		return KindRepo
	}
	return KindRepo
}

func markNested(entries []Entry) {
	for i := range entries {
		if entries[i].Kind != KindRepo {
			continue
		}
		for j := range entries {
			if i == j || entries[j].Kind != KindRepo {
				continue
			}
			if paths.IsUnder(entries[i].AbsPath, entries[j].AbsPath) {
				entries[i].Kind = KindNested
				entries[i].ContainedBy = entries[j].RelPath
			}
		}
	}
}

func NestedPairs(entries []Entry) [][2]string {
	var out [][2]string
	for _, e := range entries {
		if e.Kind == KindNested {
			out = append(out, [2]string{e.RelPath, e.ContainedBy})
		}
	}
	return out
}
