package postcreate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.email=e@x", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

func repoIn(t *testing.T, tree, rel, ignore string) string {
	t.Helper()
	repo := filepath.Join(tree, rel)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(ignore), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "init")
	return repo
}

// The snapshot must speak the vocabulary teardown reads, because teardown is
// what compares against it. Measured: a wholly ignored directory collapses to
// one line whatever it holds, so a generated config directory is reported as
// "config/" and never as "config/local.yaml". A snapshot taken any other way
// would record the second and never match the first.
func TestSnapshotSpeaksGitsVocabulary(t *testing.T) {
	tree := t.TempDir()
	repo := repoIn(t, tree, "svc", "config/\n*.sqlite\n")

	before := Snapshot(tree, []string{"svc"})

	if err := os.MkdirAll(filepath.Join(repo, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"config/a.yaml", "config/b.yaml"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "local.sqlite"), []byte("d\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Snapshot(tree, []string{"svc"})
	if !got["svc/config/"] {
		t.Fatalf("a wholly ignored directory must be recorded as git reports it; got %v", got)
	}
	if got["svc/config/a.yaml"] {
		t.Fatalf("its contents must not be recorded individually; got %v", got)
	}
	if !got["svc/local.sqlite"] {
		t.Fatalf("an individually ignored file must be recorded; got %v", got)
	}
	if before["svc/local.sqlite"] {
		t.Fatal("the before snapshot must not already hold what the script made")
	}
}

// What was already there is not the script's doing. This is what earns the
// before snapshot its place on add, where the tree already holds the previous
// run's output.
func TestNewSinceIgnoresWhatWasAlreadyThere(t *testing.T) {
	before := map[string]bool{"svc/old.sqlite": true}
	after := map[string]bool{"svc/old.sqlite": true, "svc/new.sqlite": true}
	got := NewSince(before, after)
	if len(got) != 1 || got[0] != "svc/new.sqlite" {
		t.Fatalf("want [svc/new.sqlite], got %v", got)
	}
}

func TestNewSinceIsSortedSoStateIsStable(t *testing.T) {
	after := map[string]bool{"svc/b": true, "svc/a": true, "svc/c": true}
	got := NewSince(nil, after)
	want := []string{"svc/a", "svc/b", "svc/c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}
