package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wkt/internal/container"
	"wkt/internal/discover"
	"wkt/internal/store"
)

func g(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.email=e@x", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %s", args, dir, out)
	}
	return string(out)
}

func seed(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, dir, "init", "-q")
	g(t, dir, "add", "-A")
	g(t, dir, "commit", "-qm", "init")
}

func fixture(t *testing.T) (container.C, []discover.Entry) {
	t.Helper()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seed(t, filepath.Join(ws, "services", "svc-a"))
	seed(t, filepath.Join(ws, "docs"))
	c, err := container.Locate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Create(c); err != nil {
		t.Fatal(err)
	}
	entries, err := discover.Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	return c, entries
}

func TestCreateBuildsMirroredTreeOnOneBranch(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-42", []string{"services/svc-a", "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Repos) != 2 {
		t.Fatalf("want 2 repos in the task, got %d", len(task.Repos))
	}
	for _, r := range task.Repos {
		br := g(t, r.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD")
		if br != "feat-42\n" {
			t.Fatalf("%s is on %q, want feat-42", r.RelPath, br)
		}
		if r.StoreWorktreeName == "" {
			t.Fatalf("%s: the store worktree registration name must be recorded", r.RelPath)
		}
	}
	if _, err := os.Stat(filepath.Join(c.TreePath("feat-42"), "services", "svc-a")); err != nil {
		t.Fatalf("the tree must mirror the workspace shape: %v", err)
	}
}

func TestCreateAbortsWholeSetWhenOneRepoHasADivergentBranch(t *testing.T) {
	c, entries := fixture(t)
	// docs already carries a branch of that name, pointing somewhere else.
	docs := filepath.Join(c.Workspace, "docs")
	g(t, docs, "branch", "feat-42")

	_, err := Create(c, entries, "feat-42", []string{"services/svc-a", "docs"})
	if err == nil {
		t.Fatal("create must refuse when one repository of the set has a conflicting branch")
	}
	if _, statErr := os.Stat(c.TreePath("feat-42")); !os.IsNotExist(statErr) {
		t.Fatal("a refused create must leave no tree behind")
	}
	out := g(t, filepath.Join(c.Workspace, "services", "svc-a"), "branch", "--list", "feat-42")
	if len(out) != 0 {
		t.Fatalf("rollback must remove branches created during the attempt, found %q", out)
	}
}

// TestCreateRollsBackAlreadyCreatedRepositoriesOnMidPhaseTwoFailure closes a
// gap the other two tests leave open: a divergent branch is caught by
// Validate before phase two ever starts, so it never exercises the rollback
// undo stack — a no-op rollback passes it just as well as a working one.
// Here the conflict (a pre-existing, non-empty path at the second
// repository's worktree destination) is invisible to Validate and can only
// surface once "git worktree add" actually runs, after the first
// repository's store worktree, branch and base pin already exist. Only a
// real rollback removes them.
func TestCreateRollsBackAlreadyCreatedRepositoriesOnMidPhaseTwoFailure(t *testing.T) {
	c, entries := fixture(t)

	treeRoot := c.TreePath("feat-42")
	blocked := filepath.Join(treeRoot, "docs")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "stray.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Create(c, entries, "feat-42", []string{"services/svc-a", "docs"})
	if err == nil {
		t.Fatal("create must fail when a repository's worktree destination is blocked")
	}

	if _, statErr := os.Stat(treeRoot); !os.IsNotExist(statErr) {
		t.Fatal("a refused create must leave no tree behind, including the blocked path we injected")
	}

	svcAbs := filepath.Join(c.Workspace, "services", "svc-a")
	svcStore := filepath.Join(c.StoreDir(), store.ID("services/svc-a", svcAbs)+".git")
	if out := g(t, svcStore, "branch", "--list", "feat-42"); len(out) != 0 {
		t.Fatalf("rollback must remove the branch already created for the first repository, found %q", out)
	}
	if out := g(t, svcStore, "worktree", "list", "--porcelain"); strings.Contains(out, "branch refs/heads/feat-42") {
		t.Fatalf("rollback must deregister the worktree already created for the first repository, found:\n%s", out)
	}
	if out := g(t, svcAbs, "for-each-ref", "refs/wkt/base/feat-42"); len(out) != 0 {
		t.Fatalf("rollback must remove the base pin already written for the first repository, found %q", out)
	}
}
