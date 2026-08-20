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
	for _, e := range entries {
		if e.RelPath == "svc-a-wt" && e.Kind != KindLinkedWorktree {
			t.Fatalf("linked worktree classified as %v, want KindLinkedWorktree", e.Kind)
		}
	}
}
