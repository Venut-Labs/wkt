package task

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Venut-Labs/wkt/internal/state"
	"github.com/Venut-Labs/wkt/internal/wkterr"
)

// TestFetchBringsTheTaskBranchIntoTheWorkspace — spec §6. The work happens in
// the task tree; fetch is how it gets back to the repository the developer
// actually works in, without either of them moving to the other.
func TestFetchBringsTheTaskBranchIntoTheWorkspace(t *testing.T) {
	c, entries := fixture(t)
	tk, err := Create(c, entries, "feat-fetch", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := tk.Repos[0].WorktreePath
	if err := os.WriteFile(filepath.Join(wt, "work.md"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, wt, "add", "-A")
	g(t, wt, "commit", "-qm", "task work")
	want := strings.TrimSpace(g(t, wt, "rev-parse", "HEAD"))

	results, err := Fetch(c, "feat-fetch", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Updated {
		t.Fatalf("the branch should have arrived: %+v", results)
	}
	repo := filepath.Join(c.Workspace, "docs")
	got := strings.TrimSpace(g(t, repo, "rev-parse", "refs/heads/feat-fetch"))
	if got != want {
		t.Fatalf("workspace has %s, task has %s", got, want)
	}
	// The workspace's own checkout is untouched: fetch moves a ref, not a
	// working copy.
	if branch := strings.TrimSpace(g(t, repo, "rev-parse", "--abbrev-ref", "HEAD")); branch == "feat-fetch" {
		t.Fatal("fetch must not check the branch out in the workspace")
	}
}

// TestFetchRefusesANonFastForward is the guarantee that makes this safe to run
// twice. Spec §6: fast-forward only, refuses when the workspace ref exists and
// is not an ancestor, names both SHAs, and offers --as. Never a forcing
// refspec — the developer's branch is theirs.
func TestFetchRefusesANonFastForward(t *testing.T) {
	c, entries := fixture(t)
	tk, err := Create(c, entries, "feat-ff", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := tk.Repos[0].WorktreePath
	g(t, wt, "commit", "-qm", "task work", "--allow-empty")
	taskSHA := strings.TrimSpace(g(t, wt, "rev-parse", "HEAD"))

	// Someone made a branch of the same name in the workspace, elsewhere.
	repo := filepath.Join(c.Workspace, "docs")
	g(t, repo, "branch", "feat-ff")
	g(t, repo, "checkout", "-q", "feat-ff")
	g(t, repo, "commit", "-qm", "unrelated work in the workspace", "--allow-empty")
	theirs := strings.TrimSpace(g(t, repo, "rev-parse", "HEAD"))
	g(t, repo, "checkout", "-q", "-")

	_, err = Fetch(c, "feat-ff", "")
	if err == nil {
		t.Fatal("a non-fast-forward must be refused, never forced")
	}
	var e *wkterr.E
	if !errors.As(err, &e) || e.Code != "WKT_NOT_FAST_FORWARD" {
		t.Fatalf("want WKT_NOT_FAST_FORWARD, got %v", err)
	}
	if !strings.Contains(e.Expected+e.Found+strings.Join(e.Remedy, " "), taskSHA[:7]) ||
		!strings.Contains(e.Expected+e.Found+strings.Join(e.Remedy, " "), theirs[:7]) {
		t.Fatalf("the refusal must name both commits: %+v", e)
	}
	if !strings.Contains(strings.Join(e.Remedy, " "), "--as") {
		t.Fatalf("the refusal must offer --as: %+v", e)
	}
	// And their branch is exactly where they left it.
	if now := strings.TrimSpace(g(t, repo, "rev-parse", "refs/heads/feat-ff")); now != theirs {
		t.Fatal("the developer's branch was moved by a refused fetch")
	}
}

// TestFetchAsGivesTheBranchAnotherName — the way out the refusal offers has to
// work, or it is not a remedy.
func TestFetchAsGivesTheBranchAnotherName(t *testing.T) {
	c, entries := fixture(t)
	tk, err := Create(c, entries, "feat-as", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := tk.Repos[0].WorktreePath
	g(t, wt, "commit", "-qm", "task work", "--allow-empty")
	want := strings.TrimSpace(g(t, wt, "rev-parse", "HEAD"))

	repo := filepath.Join(c.Workspace, "docs")
	g(t, repo, "branch", "feat-as")
	g(t, repo, "checkout", "-q", "feat-as")
	g(t, repo, "commit", "-qm", "theirs", "--allow-empty")
	g(t, repo, "checkout", "-q", "-")

	if _, err := Fetch(c, "feat-as", "rescued"); err != nil {
		t.Fatalf("--as must work where the plain fetch refused: %v", err)
	}
	got := strings.TrimSpace(g(t, repo, "rev-parse", "refs/heads/rescued"))
	if got != want {
		t.Fatalf("rescued is at %s, want %s", got, want)
	}
}

// TestFetchIsIdempotent — running it again when nothing changed reports
// nothing rather than failing.
func TestFetchIsIdempotent(t *testing.T) {
	c, entries := fixture(t)
	tk, err := Create(c, entries, "feat-idem", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	g(t, tk.Repos[0].WorktreePath, "commit", "-qm", "work", "--allow-empty")
	if _, err := Fetch(c, "feat-idem", ""); err != nil {
		t.Fatal(err)
	}
	second, err := Fetch(c, "feat-idem", "")
	if err != nil {
		t.Fatalf("a second fetch with nothing new must not fail: %v", err)
	}
	for _, r := range second {
		if r.Updated {
			t.Fatalf("nothing changed, so nothing should be reported as updated: %+v", r)
		}
	}
}

var _ = state.Repo{}
