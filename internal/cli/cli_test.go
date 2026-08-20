package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wkt/internal/wkterr"
)

func seedRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
		full := append([]string{"-c", "user.email=e@x", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
}

func TestInitNewPathRmRoundTrip(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "services", "svc-a"))
	seedRepo(t, filepath.Join(ws, "docs"))

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"new", "feat-42", "--workspace", ws, "--all"}, &out, &errb); code != 0 {
		t.Fatalf("new exited %d: %s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"path", "feat-42", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("path exited %d: %s", code, errb.String())
	}
	treePath := strings.TrimSpace(out.String())
	if _, err := os.Stat(filepath.Join(treePath, "services", "svc-a")); err != nil {
		t.Fatalf("path must point at a materialised mirrored tree: %v", err)
	}
	out.Reset()
	if code := Run([]string{"rm", "feat-42", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("rm on a clean task exited %d: %s", code, errb.String())
	}
	if _, err := os.Stat(treePath); !os.IsNotExist(err) {
		t.Fatal("rm must remove a clean tree")
	}
}

func TestNewOnExistingTaskExitsTwo(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))
	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	Run([]string{"new", "t1", "--workspace", ws, "--all"}, &out, &errb)
	if code := Run([]string{"new", "t1", "--workspace", ws, "--all"}, &out, &errb); code != 2 {
		t.Fatalf("a duplicate task must exit 2, got %d", code)
	}
}

// --- exit-code contract, exercised beyond "not zero" ---

// TestFailMapsErrorCodesToContractExitCodes locks the fail() mapping table
// down directly: each wkterr code must land on its documented exit code, not
// merely "something non-zero". A regression that mapped every error to 1
// (or every error to 2) would still pass a test that only checked "!= 0".
func TestFailMapsErrorCodesToContractExitCodes(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{"WKT_TASK_EXISTS", 2},
		{"WKT_NO_CONTAINER", 4},
		{"WKT_TREE_MISSING", 3},
		{"WKT_NO_TASK", 1},
		{"WKT_DIRTY", 1},
		{"WKT_GIT_FAILED", 1},
	}
	for _, c := range cases {
		var errb bytes.Buffer
		got := fail(&errb, wkterr.New(c.code, "x"))
		if got != c.want {
			t.Errorf("fail(%s) = %d, want %d", c.code, got, c.want)
		}
		if errb.Len() == 0 {
			t.Errorf("fail(%s) wrote nothing to stderr", c.code)
		}
		if strings.Contains(errb.String(), "\t") {
			t.Errorf("fail(%s) leaked a raw-looking payload: %s", c.code, errb.String())
		}
	}
}

// TestStatusInfoSeverityDoesNotTriggerDrift is correction 2: an ordinary
// regenerable directory like node_modules must be reported (so --force
// doesn't become reflexive) but must NOT flip status's exit code to 3. A
// status loop that set drift on every Preflight entry — info or not — would
// fail this test; one that filtered by severity passes it.
func TestStatusInfoSeverityDoesNotTriggerDrift(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	wsRepo := filepath.Join(ws, "svc-a")
	seedRepo(t, wsRepo)

	// node_modules must be gitignored for git to report it as "!!" ignored,
	// which is what Preflight's regenerable classifier keys on. The
	// .gitignore is committed into the *workspace* repository, before "new"
	// resolves the task's base — so it becomes part of the base commit
	// itself rather than an unpushed commit made inside the tree (which
	// would introduce its own, unrelated WKT_UNPUSHED blocker and confound
	// this test's claim that ONLY the info-severity entry is present).
	if err := os.WriteFile(filepath.Join(wsRepo, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "ignore node_modules"}} {
		full := append([]string{"-c", "user.email=e@x", "-c", "user.name=t"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = wsRepo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	if code := Run([]string{"new", "t-info", "--workspace", ws, "--all"}, &out, &errb); code != 0 {
		t.Fatalf("new exited %d: %s", code, errb.String())
	}
	out.Reset()
	Run([]string{"path", "t-info", "--workspace", ws}, &out, &errb)
	treePath := strings.TrimSpace(out.String())

	repoDir := filepath.Join(treePath, "svc-a")
	if err := os.MkdirAll(filepath.Join(repoDir, "node_modules", "leftpad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "node_modules", "leftpad", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	code := Run([]string{"status", "t-info", "--workspace", ws}, &out, &errb)
	if code != 0 {
		t.Fatalf("status with only regenerable ignored content exited %d, want 0: stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "WKT_REGENERABLE_IGNORED") {
		t.Fatalf("status must still report the regenerable entry, got: %s", out.String())
	}
}

// TestStatusRealBlockerTriggersDriftExitThree is the other half of
// correction 2: a genuine blocker (an uncommitted change) must still flip
// status to exit 3. Without this paired test, a status loop that never sets
// drift at all would also pass the info-severity test above.
func TestStatusRealBlockerTriggersDriftExitThree(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))

	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	Run([]string{"new", "t-dirty", "--workspace", ws, "--all"}, &out, &errb)
	out.Reset()
	Run([]string{"path", "t-dirty", "--workspace", ws}, &out, &errb)
	treePath := strings.TrimSpace(out.String())

	if err := os.WriteFile(filepath.Join(treePath, "svc-a", "f.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	code := Run([]string{"status", "t-dirty", "--workspace", ws}, &out, &errb)
	if code != 3 {
		t.Fatalf("status with an uncommitted change exited %d, want 3: stdout=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "WKT_DIRTY") {
		t.Fatalf("status must report the dirty blocker, got: %s", out.String())
	}
}

// TestPathFailsWhenTreeMissingFromDisk is correction 3: state can still have
// a record of the task after its tree directory is gone from disk (e.g.
// deleted outside wkt). path must refuse rather than print a path to
// nothing — and, crucially, must not merely print the path anyway.
func TestPathFailsWhenTreeMissingFromDisk(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))

	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	Run([]string{"new", "t-gone", "--workspace", ws, "--all"}, &out, &errb)
	out.Reset()
	Run([]string{"path", "t-gone", "--workspace", ws}, &out, &errb)
	treePath := strings.TrimSpace(out.String())
	if treePath == "" {
		t.Fatal("setup: expected a tree path")
	}

	if err := os.RemoveAll(treePath); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	code := Run([]string{"path", "t-gone", "--workspace", ws}, &out, &errb)
	if code == 0 {
		t.Fatalf("path must not exit 0 once the tree is gone from disk, got stdout=%q", out.String())
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("path must not print a path to nothing, got stdout=%q", out.String())
	}
	if code != 3 {
		t.Fatalf("path on a state/disk mismatch must exit 3 (drift), got %d", code)
	}
}

// TestRmRefusesOnDirtyTreeExitsOne mirrors the acceptance battery's
// destructive-cleanup test: a plain rm on a tree with uncommitted work must
// refuse (exit 1, not 0, not silently succeed) and must leave the tree
// untouched.
func TestRmRefusesOnDirtyTreeExitsOne(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "docs"))

	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	Run([]string{"new", "t-refuse", "--workspace", ws, "--all"}, &out, &errb)
	out.Reset()
	Run([]string{"path", "t-refuse", "--workspace", ws}, &out, &errb)
	treePath := strings.TrimSpace(out.String())

	if err := os.WriteFile(filepath.Join(treePath, "docs", "untracked.md"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	code := Run([]string{"rm", "t-refuse", "--workspace", ws}, &out, &errb)
	if code != 1 {
		t.Fatalf("rm on a dirty tree exited %d, want 1", code)
	}
	if _, err := os.Stat(treePath); err != nil {
		t.Fatalf("rm must not remove a dirty tree: %v", err)
	}
}

// TestCreateAndCleanupAreDocumentedAliases checks the acceptance battery's
// two required verbs actually reach new/rm, not merely that some command
// named "create" exists and returns 2 for anything.
func TestCreateAndCleanupAreDocumentedAliases(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))

	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	out.Reset()
	if code := Run([]string{"create", "t-alias", "--workspace", ws, "--all"}, &out, &errb); code != 0 {
		t.Fatalf("create exited %d: %s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"path", "t-alias", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("path after create exited %d: %s", code, errb.String())
	}
	treePath := strings.TrimSpace(out.String())
	if _, err := os.Stat(treePath); err != nil {
		t.Fatalf("create must materialise a tree just like new: %v", err)
	}
	out.Reset()
	if code := Run([]string{"cleanup", "t-alias", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("cleanup exited %d: %s", code, errb.String())
	}
	if _, err := os.Stat(treePath); !os.IsNotExist(err) {
		t.Fatal("cleanup must remove the tree just like rm")
	}
}

// TestUsageErrorsExitTwo checks the "2 = usage error" half of the contract
// with real malformed invocations rather than the happy path.
func TestUsageErrorsExitTwo(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{}, &out, &errb); code != 2 {
		t.Fatalf("no args exited %d, want 2", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"bogus-command"}, &out, &errb); code != 2 {
		t.Fatalf("unknown command exited %d, want 2", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"new"}, &out, &errb); code != 2 {
		t.Fatalf("new with no task name exited %d, want 2", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"new", "t", "--not-a-real-flag"}, &out, &errb); code != 2 {
		t.Fatalf("an unparsable flag set exited %d, want 2", code)
	}
}
