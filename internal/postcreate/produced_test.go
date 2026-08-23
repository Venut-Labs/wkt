package postcreate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// A script can write outside every repository — an intermediate directory of
// the mirrored tree, or the tree root itself. Teardown blocks on that as
// untracked tree content, so the snapshot has to see it too. Found by the
// battery: "for d in */" touches the mirrored tree's own directories.
func TestSnapshotSeesContentOutsideEveryRepository(t *testing.T) {
	tree := t.TempDir()
	repoIn(t, tree, filepath.Join("services", "svc-a"), "*.sqlite\n")

	before := Snapshot(tree, []string{"services/svc-a"})

	// Beside the repository, not in it.
	if err := os.WriteFile(filepath.Join(tree, "services", "installed"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "root-level.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Snapshot(tree, []string{"services/svc-a"})
	for _, want := range []string{"services/installed", "root-level.log"} {
		if !got[want] {
			t.Fatalf("%q must be recorded; got %v", want, got)
		}
		if before[want] {
			t.Fatalf("%q must not be in the before snapshot", want)
		}
	}
	// The repository's own contents stay git's business: recording them from
	// the filesystem as well would speak a vocabulary teardown does not read.
	if got["services/svc-a/.gitignore"] {
		t.Fatalf("tracked repository content must not be recorded; got %v", got)
	}
}

// The seam's whole job is running installers, and an installer at the tree
// root makes a node_modules with a hundred thousand files in it. The
// repository half relies on git collapsing such a directory to one line; the
// tree half has to do the same, or every one of those files becomes a
// permanent entry in the task's state.
func TestABuildDirectoryOutsideARepositoryIsRecordedWhole(t *testing.T) {
	tree := t.TempDir()
	repoIn(t, tree, "svc", "*.sqlite\n")
	deep := filepath.Join(tree, "node_modules", "pkg", "dist")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.js", "b.js", "c.js"} {
		if err := os.WriteFile(filepath.Join(deep, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := Snapshot(tree, []string{"svc"})
	if !got["node_modules"] {
		t.Fatalf("the directory itself must be recorded; got %v", got)
	}
	for k := range got {
		if strings.HasPrefix(k, "node_modules/") {
			t.Fatalf("nothing below it may be recorded; got %q", k)
		}
	}
}
