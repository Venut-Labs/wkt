// Package tree materialises a task's tree: repositories mirrored at their
// workspace-relative positions, un-materialised repositories back-filled as
// absolute symlinks so cross-repo references still resolve, non-git
// directories linked, and loose files copied with their content hash
// recorded.
package tree

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"wkt/internal/discover"
	"wkt/internal/state"
	"wkt/internal/wkterr"
)

type Plan struct {
	Materialise []string // repositories that become real worktrees
	BackFill    []string // repositories present only as symlinks to the workspace
	LinkDirs    []string // non-git directories
	CopyFiles   []string // loose files
}

func PlanFor(workspace string, entries []discover.Entry, selected []string) (Plan, error) {
	sel := map[string]bool{}
	for _, s := range selected {
		sel[s] = true
	}
	var p Plan
	repoPaths := map[string]bool{}
	for _, e := range entries {
		if e.Kind != discover.KindRepo {
			continue
		}
		repoPaths[e.RelPath] = true
		if sel[e.RelPath] {
			p.Materialise = append(p.Materialise, e.RelPath)
		} else {
			p.BackFill = append(p.BackFill, e.RelPath)
		}
	}
	for _, s := range selected {
		if !repoPaths[s] {
			return Plan{}, wkterr.New("WKT_NO_SUCH_REPO", "not a discovered repository").WithRepo(s)
		}
	}
	// Ancestors of anything we materialise must stay real directories.
	ancestors := map[string]bool{}
	for _, m := range p.Materialise {
		for d := filepath.Dir(m); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
			ancestors[d] = true
		}
	}
	top, err := os.ReadDir(workspace)
	if err != nil {
		return Plan{}, wkterr.New("WKT_WORKSPACE_UNREADABLE", "cannot read the workspace").WithPath(workspace)
	}
	for _, e := range top {
		name := e.Name()
		if name == ".claude" || name == ".wkt" || repoPaths[name] || ancestors[name] {
			continue
		}
		if e.IsDir() {
			p.LinkDirs = append(p.LinkDirs, name)
		} else {
			p.CopyFiles = append(p.CopyFiles, name)
		}
	}
	return p, nil
}

func Materialise(treeRoot, workspace string, p Plan) ([]state.LinkSlot, error) {
	var slots []state.LinkSlot
	link := func(rel string) error {
		dst := filepath.Join(treeRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return wkterr.New("WKT_TREE_BUILD", "cannot create an ancestor directory").WithPath(dst)
		}
		src := filepath.Join(workspace, rel) // absolute target (spec §5.3 rule 3)
		if err := os.Symlink(src, dst); err != nil && !os.IsExist(err) {
			return wkterr.New("WKT_TREE_BUILD", "cannot create a link slot").WithPath(dst).WithFound(err.Error())
		}
		slots = append(slots, state.LinkSlot{RelPath: rel, Target: src, Type: "symlink"})
		return nil
	}
	for _, rel := range append(append([]string{}, p.BackFill...), p.LinkDirs...) {
		if err := link(rel); err != nil {
			return nil, err
		}
	}
	for _, rel := range p.CopyFiles {
		src := filepath.Join(workspace, rel)
		dst := filepath.Join(treeRoot, rel)
		sum, err := copyFile(src, dst)
		if err != nil {
			return nil, err
		}
		slots = append(slots, state.LinkSlot{RelPath: rel, Target: src, Type: "copy", Hash: sum})
	}
	return slots, nil
}

func copyFile(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot read a workspace file").WithPath(src)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot create a directory").WithPath(dst)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot write into the tree").WithPath(dst)
	}
	defer out.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot copy a workspace file").WithPath(dst)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Hash re-reads a copied file so teardown can detect divergence (spec §5.3 rule 5).
func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
