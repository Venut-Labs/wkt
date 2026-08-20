package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wkt/internal/discover"
	"wkt/internal/state"
)

func TestRemoveRefusesOnIgnoredButPreciousFile(t *testing.T) {
	c, entries := fixture(t)
	// .env is gitignored, so git's own refusal never fires on it (spec H1).
	repo := filepath.Join(c.Workspace, "services", "svc-a")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, repo, "add", "-A")
	g(t, repo, "commit", "-qm", "ignore env")
	entries, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}

	task, err := Create(c, entries, "feat-x", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(task.Repos[0].WorktreePath, ".env")
	if err := os.WriteFile(envPath, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = Remove(c, "feat-x", false)
	if err == nil {
		t.Fatal("removal must refuse while an ignored-but-precious file exists")
	}
	if _, statErr := os.Stat(envPath); statErr != nil {
		t.Fatal("the refused removal must not have deleted anything")
	}
}

func TestPreflightSeesUnpushedCommits(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-y", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, wt, "add", "-A")
	g(t, wt, "commit", "-qm", "agent work")

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var sawUnpushed bool
	for _, b := range blockers {
		if b.Code == "WKT_UNPUSHED" {
			sawUnpushed = true
		}
	}
	if !sawUnpushed {
		t.Fatalf("an unpushed commit must block removal, got %+v", blockers)
	}
}

var _ = state.Task{}

// TestRemoveDetectsForeignRepoNestedInsideMaterialisedWorktree is a
// regression guard for a real bug found by mutating the foreign-repo walk:
// fs.WalkDir treats "return SkipDir" differently depending on whether the
// visited entry is a directory or not. A linked worktree's own ".git" is
// always a regular *file*; returning SkipDir unconditionally on it (rather
// than only when it is a directory) skips the rest of that directory's
// siblings, not just the ".git" contents — so a foreign repository nested
// anywhere sorting after ".git" (almost everything) was silently never
// visited. Confirmed empirically against Go's real fs.WalkDir before fixing
// internal/task/remove.go. This is the highest-stakes blocker in the
// package — it must fire even with --force — so it gets its own test rather
// than relying on the other tests to exercise it incidentally.
func TestRemoveDetectsForeignRepoNestedInsideMaterialisedWorktree(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-z", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	// Sorts after ".git" alphabetically, which is what the bug depended on.
	foreign := filepath.Join(wt, "zzz-vendor")
	seed(t, foreign)

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_FOREIGN_REPO" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a foreign repository nested inside a materialised worktree must be detected, got %+v", blockers)
	}

	if err := Remove(c, "feat-z", true); err == nil {
		t.Fatal("a foreign repository must block removal even with --force: its history exists nowhere else")
	}
	if _, statErr := os.Stat(filepath.Join(foreign, ".git")); statErr != nil {
		t.Fatal("the refused removal must not have deleted the foreign repository")
	}
}

// TestRemoveCleansUpStoreBranchSoTaskNameCanBeReused is a regression guard
// for a real bug found by mutating the store cleanup: "git worktree prune"
// removes the worktree's admin entry but never the branch the worktree was
// created on. Left behind, the branch makes a later Create of a task with
// the same name fail Validate's WKT_BRANCH_EXISTS check against the store —
// even though the previous task was fully and cleanly removed. Confirmed
// empirically against real git ("branch --list" still showed the branch
// after unlock+prune) before adding the "branch -D" cleanup step.
func TestRemoveCleansUpStoreBranchSoTaskNameCanBeReused(t *testing.T) {
	c, entries := fixture(t)
	if _, err := Create(c, entries, "feat-reuse", []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	if err := Remove(c, "feat-reuse", false); err != nil {
		t.Fatalf("a clean tree must remove without --force: %v", err)
	}

	entries2, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(c, entries2, "feat-reuse", []string{"docs"}); err != nil {
		t.Fatalf("a task name freed by a clean removal must be reusable, got: %v", err)
	}
}

// TestRemoveRefusesOnUncommittedWorkWithoutForceButForceRemoves exercises
// the whole lifecycle end to end: refusal leaves the tree untouched, --force
// actually deletes it through the staging fence, staging is left clean
// afterward, and the task's state file is gone.
func TestRemoveRefusesOnUncommittedWorkWithoutForceButForceRemoves(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-dirty", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(c, "feat-dirty", false); err == nil {
		t.Fatal("uncommitted work must block removal without --force")
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatal("the refused removal must not have deleted anything")
	}

	if err := Remove(c, "feat-dirty", true); err != nil {
		t.Fatalf("--force must remove a tree whose only problem is uncommitted work: %v", err)
	}
	if _, statErr := os.Stat(c.TreePath("feat-dirty")); !os.IsNotExist(statErr) {
		t.Fatal("a forced removal must actually delete the tree")
	}
	if _, statErr := os.Stat(filepath.Join(c.StagingDir(), "feat-dirty")); !os.IsNotExist(statErr) {
		t.Fatal("the staging fence must not leave a leftover directory behind")
	}
	if _, err := state.Load(c.StateDir(), "feat-dirty"); err == nil {
		t.Fatal("a successful removal must delete the task's state file")
	}
}

// TestPreflightDetectsInProgressBisect exercises spec H2 directly: a mid-
// bisect worktree has an empty "git status --porcelain" (confirmed
// empirically), so a preflight built on status alone would let it through.
// Bisect is used rather than an interactive rebase pause because it needs no
// editor/sequence-editor trickery to stay portable across macOS and Linux.
func TestPreflightDetectsInProgressBisect(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-bisect", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	good := task.Repos[0].BaseSHA

	g(t, wt, "commit", "-qm", "c2", "--allow-empty")
	g(t, wt, "commit", "-qm", "c3", "--allow-empty")
	bad := strings.TrimSpace(g(t, wt, "rev-parse", "HEAD"))

	g(t, wt, "bisect", "start")
	g(t, wt, "bisect", "bad", bad)
	g(t, wt, "bisect", "good", good)

	if s := g(t, wt, "status", "--porcelain"); s != "" {
		t.Fatalf("test setup invariant broken: expected a clean status mid-bisect, got %q", s)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_IN_PROGRESS" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a mid-bisect worktree must block removal even though git status is clean, got %+v", blockers)
	}
}

// TestRemoveRefusesOnSubmoduleEvenWithForce exercises spec §5.7's hardest
// block. Crucially the submodule addition is committed before the check
// runs: an uncommitted "git submodule add" already shows up in plain "git
// status --porcelain" (confirmed empirically) and would be caught by
// WKT_DIRTY regardless, which would prove nothing about the dedicated
// WKT_SUBMODULE check. Committed, status is clean and only the submodule
// check can catch it.
func TestRemoveRefusesOnSubmoduleEvenWithForce(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-sub", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	subSrc := filepath.Join(c.Workspace, "docs")

	cmd := exec.Command("git",
		"-c", "protocol.file.allow=always",
		"-c", "user.email=e@x", "-c", "user.name=t",
		"submodule", "add", "-q", subSrc, "subdir")
	cmd.Dir = wt
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git submodule add: %s", out)
	}
	g(t, wt, "commit", "-qm", "add submodule")

	if s := g(t, wt, "status", "--porcelain"); s != "" {
		t.Fatalf("test setup invariant broken: expected a clean status after committing the submodule, got %q", s)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_SUBMODULE" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a committed submodule must still be detected as a blocker, got %+v", blockers)
	}

	if err := Remove(c, "feat-sub", false); err == nil {
		t.Fatal("a submodule must block removal without --force")
	}
	if err := Remove(c, "feat-sub", true); err == nil {
		t.Fatal("a submodule must block removal even with --force: --force would destroy its object store")
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatal("the refused removal must not have deleted anything")
	}
}

// TestRemoveRefusesOnDivergedCopiedFile exercises the one blocker with no
// git mechanism behind it at all: a loose file living outside every
// repository is copied into the tree by tree.Materialise, and only the
// recorded content hash can tell an agent's edit to the copy apart from an
// untouched one before deletion would silently lose it.
func TestRemoveRefusesOnDivergedCopiedFile(t *testing.T) {
	c, entries := fixture(t)
	readme := filepath.Join(c.Workspace, "README.md")
	if err := os.WriteFile(readme, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Create(c, entries, "feat-copy", []string{"services/svc-a"}); err != nil {
		t.Fatal(err)
	}

	copied := filepath.Join(c.TreePath("feat-copy"), "README.md")
	if _, statErr := os.Stat(copied); statErr != nil {
		t.Fatalf("test setup invariant broken: README.md must have been copied into the tree, got %v", statErr)
	}
	if err := os.WriteFile(copied, []byte("agent edited this\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(c, "feat-copy", false); err == nil {
		t.Fatal("a diverged copied file must block removal")
	}
	if _, statErr := os.Stat(copied); statErr != nil {
		t.Fatal("the refused removal must not have deleted the diverged copy")
	}
}
