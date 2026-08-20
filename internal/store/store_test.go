package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func seedRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, dir, "init", "-q")
	g(t, dir, "add", "-A")
	g(t, dir, "commit", "-qm", "init")
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestEnsureProducesUsableStoreThatSurvivesSourceDeletion(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws", "services", "svc-a")
	sha := seedRepo(t, ws)
	storeDir := filepath.Join(base, "container", "store")
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	sp, err := Ensure(storeDir, ws, "services/svc-a", "feat-42", sha)
	if err != nil {
		t.Fatal(err)
	}
	if !HasObject(sp, sha) {
		t.Fatal("the store must contain the base commit")
	}
	// The pin must exist in the WORKSPACE repository, written before the clone.
	if out := g(t, ws, "rev-parse", "--verify", "refs/wkt/base/feat-42"); len(out) < 40 {
		t.Fatalf("base pin missing in the workspace repo: %q", out)
	}
	// De-borrowed: no alternates file, and the object survives losing the source.
	if _, err := os.Stat(filepath.Join(sp, "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatal("a de-borrowed store must have no alternates file")
	}
	if err := os.RemoveAll(filepath.Join(base, "ws")); err != nil {
		t.Fatal(err)
	}
	if !HasObject(sp, sha) {
		t.Fatal("the store must survive deletion of the workspace repository")
	}
}

func TestEnsureConfiguresFetchRefspecAndWorkspaceRemote(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws", "svc-a")
	sha := seedRepo(t, ws)
	storeDir := filepath.Join(base, "c", "store")
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sp, err := Ensure(storeDir, ws, "svc-a", "t1", sha)
	if err != nil {
		t.Fatal(err)
	}
	refspec := g(t, sp, "config", "--get", "remote.origin.fetch")
	if len(refspec) == 0 {
		t.Fatal("bare clones set no refspec; Ensure must add one (spec H15)")
	}
	if out := g(t, sp, "remote"); !containsLine(out, "workspace") {
		t.Fatalf("the workspace remote is missing: %q", out)
	}
	// A commit made locally in the workspace and never pushed must become reachable.
	if err := os.WriteFile(filepath.Join(ws, "src", "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, ws, "add", "-A")
	g(t, ws, "commit", "-qm", "local only")
	local := g(t, ws, "rev-parse", "HEAD")[:40]
	if HasObject(sp, local) {
		t.Fatal("precondition: the store should not have the new commit yet")
	}
	if err := FetchWorkspace(sp); err != nil {
		t.Fatal(err)
	}
	if !HasObject(sp, local) {
		t.Fatal("after FetchWorkspace the local-only commit must be reachable (spec §5.2)")
	}
}

func containsLine(haystack, want string) bool {
	for _, line := range splitLines(haystack) {
		if line == want {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out, cur = []string{}, ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
