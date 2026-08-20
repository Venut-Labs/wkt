package discover

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-c", "init.defaultBranch=main", "init", "-q")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init in %s: %v", dir, err)
	}
}

func TestWalkFindsReposAtMixedDepths(t *testing.T) {
	ws := t.TempDir()
	gitInit(t, filepath.Join(ws, "services", "svc-a"))
	gitInit(t, filepath.Join(ws, "docs"))
	if err := os.MkdirAll(filepath.Join(ws, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Kind{}
	for _, e := range entries {
		found[e.RelPath] = e.Kind
	}
	if found["services/svc-a"] != KindRepo {
		t.Fatalf("services/svc-a not discovered as a repo: %v", found)
	}
	if found["docs"] != KindRepo {
		t.Fatalf("docs not discovered as a repo: %v", found)
	}
	if _, ok := found["notes"]; ok {
		t.Fatal("a plain directory must not be reported as a repository")
	}
}

func TestWalkClassifiesLinkedWorktreeNotAsRepo(t *testing.T) {
	ws := t.TempDir()
	repo := filepath.Join(ws, "svc-a")
	gitInit(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=e@x", "-c", "user.name=t"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}
	wt := filepath.Join(ws, "svc-a-wt")
	cmd := exec.Command("git", "worktree", "add", "-q", wt)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %s", out)
	}
	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.RelPath == "svc-a-wt" {
			found = true
			if e.Kind != KindLinkedWorktree {
				t.Fatalf("linked worktree classified as %v, want KindLinkedWorktree", e.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("linked worktree not discovered")
	}
}

func TestWalkDoesNotSkipSiblingsAfterSymlink(t *testing.T) {
	ws := t.TempDir()
	// Create a symlink at "aaa-link" pointing to a directory
	linkTarget := filepath.Join(ws, "target")
	if err := os.MkdirAll(linkTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "aaa-link")
	if err := os.Symlink(linkTarget, link); err != nil {
		t.Fatal(err)
	}
	// Create a real repo at "zzz-repo" after the symlink (lexically)
	gitInit(t, filepath.Join(ws, "zzz-repo"))
	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.RelPath == "zzz-repo" {
			found = true
			if e.Kind != KindRepo {
				t.Fatalf("zzz-repo misclassified as %v", e.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("zzz-repo not discovered after symlink: %v", entries)
	}
}

func TestWalkFindsReposAtMixedDepthsBetter(t *testing.T) {
	ws := t.TempDir()
	gitInit(t, filepath.Join(ws, "services", "svc-a"))
	gitInit(t, filepath.Join(ws, "docs"))
	if err := os.MkdirAll(filepath.Join(ws, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Kind{}
	for _, e := range entries {
		found[e.RelPath] = e.Kind
	}
	// Check services/svc-a exists and is a repo
	if v, ok := found["services/svc-a"]; !ok {
		t.Fatalf("services/svc-a not discovered: %v", found)
	} else if v != KindRepo {
		t.Fatalf("services/svc-a not classified as repo: %v", found)
	}
	// Check docs exists and is a repo
	if v, ok := found["docs"]; !ok {
		t.Fatalf("docs not discovered: %v", found)
	} else if v != KindRepo {
		t.Fatalf("docs not classified as repo: %v", found)
	}
	// Check notes is not reported
	if _, ok := found["notes"]; ok {
		t.Fatal("a plain directory must not be reported as a repository")
	}
}

func TestWalkRespectsDepthBoundary(t *testing.T) {
	ws := t.TempDir()
	gitInit(t, filepath.Join(ws, "a", "b", "c"))
	gitInit(t, filepath.Join(ws, "a", "b", "c", "d"))

	// With maxDepth=3, a/b/c should be found but a/b/c/d should not
	entries, err := Walk(ws, 3)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Kind{}
	for _, e := range entries {
		found[e.RelPath] = e.Kind
	}

	// a/b/c is 3 levels deep (a=1, b=2, c=3), should be found
	if v, ok := found["a/b/c"]; !ok {
		t.Fatalf("a/b/c not discovered: %v", found)
	} else if v != KindRepo {
		t.Fatalf("a/b/c not classified as repo: %v", found)
	}

	// a/b/c/d is 4 levels deep, should not be found
	if _, ok := found["a/b/c/d"]; ok {
		t.Fatalf("a/b/c/d should not be discovered with maxDepth=3: %v", found)
	}
}

func TestWalkClassifiesSubmoduleWithWorktreesInPath(t *testing.T) {
	ws := t.TempDir()
	repo := filepath.Join(ws, "main-repo")
	gitInit(t, repo)

	// Create a separate submodule repository outside the main repo
	submoduleSource := filepath.Join(ws, "external-submodule")
	gitInit(t, submoduleSource)

	// Create a commit in the submodule
	if err := os.WriteFile(filepath.Join(submoduleSource, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=e@x", "-c", "user.name=t"}, args...)...)
		cmd.Dir = submoduleSource
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}

	// Add it as a submodule to the main repo in a path containing "worktrees"
	cmd := exec.Command("git", "-c", "protocol.file.allow=always", "submodule", "add", "-q", submoduleSource, "vendor/worktrees/mylib")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("submodule add: %s", out)
	}

	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, e := range entries {
		if e.RelPath == "main-repo/vendor/worktrees/mylib" {
			found = true
			if e.Kind != KindSubmodule {
				t.Fatalf("submodule misclassified as %v, want KindSubmodule", e.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("submodule not discovered: entries=%v", entries)
	}
}

func TestWalkFindsNearestContainingRepository(t *testing.T) {
	ws := t.TempDir()
	gitInit(t, filepath.Join(ws, "outer"))
	gitInit(t, filepath.Join(ws, "outer", "middle"))
	gitInit(t, filepath.Join(ws, "outer", "middle", "inner"))

	entries, err := Walk(ws, 5)
	if err != nil {
		t.Fatal(err)
	}

	// Build a map for easy lookup
	found := make(map[string]*Entry)
	for i, e := range entries {
		found[e.RelPath] = &entries[i]
	}

	// Verify outer is a repo
	if v, ok := found["outer"]; !ok {
		t.Fatalf("outer not discovered")
	} else if v.Kind != KindRepo {
		t.Fatalf("outer not classified as repo")
	}

	// Verify middle is nested inside outer, not outer/middle
	if v, ok := found["outer/middle"]; !ok {
		t.Fatalf("outer/middle not discovered")
	} else if v.Kind != KindNested {
		t.Fatalf("outer/middle not classified as nested: %v", v.Kind)
	} else if v.ContainedBy != "outer" {
		t.Fatalf("outer/middle.ContainedBy=%q, want outer", v.ContainedBy)
	}

	// Verify inner is nested inside middle, not outer
	if v, ok := found["outer/middle/inner"]; !ok {
		t.Fatalf("outer/middle/inner not discovered")
	} else if v.Kind != KindNested {
		t.Fatalf("outer/middle/inner not classified as nested: %v", v.Kind)
	} else if v.ContainedBy != "outer/middle" {
		t.Fatalf("outer/middle/inner.ContainedBy=%q, want outer/middle", v.ContainedBy)
	}

	// Verify NestedPairs is correct
	pairs := NestedPairs(entries)
	expectedPairs := [][2]string{
		{"outer/middle", "outer"},
		{"outer/middle/inner", "outer/middle"},
	}
	if len(pairs) != len(expectedPairs) {
		t.Fatalf("NestedPairs returned %d pairs, want %d: %v", len(pairs), len(expectedPairs), pairs)
	}
	pairMap := make(map[[2]string]bool)
	for _, p := range pairs {
		pairMap[p] = true
	}
	for _, ep := range expectedPairs {
		if !pairMap[ep] {
			t.Fatalf("NestedPairs missing expected pair %v", ep)
		}
	}
}
