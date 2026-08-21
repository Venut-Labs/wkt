package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Venut-Labs/wkt/internal/wkterr"
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

// --- review round 2 ---

// TestPathAndRmRequireATaskNameExitTwo is minor fix 4: an empty task name on
// path or rm is a usage error, exactly like new, not whatever incidental
// error state.Load or task.Remove happens to produce for an empty name.
func TestPathAndRmRequireATaskNameExitTwo(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"path", "--workspace", ws}, &out, &errb); code != 2 {
		t.Fatalf("path with no task name exited %d, want 2", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"rm", "--workspace", ws}, &out, &errb); code != 2 {
		t.Fatalf("rm with no task name exited %d, want 2", code)
	}
}

// TestUninitialisedContainerExitsFour is Important fix 1: new, path, status
// and rm against a workspace that was never `wkt init`-ed must all exit 4,
// not whatever incidental error each command happens to hit first. This is
// an integration test against real commands, not just a check on fail()'s
// mapping table — it would have caught the original bug (status silently
// exiting 0, new/path/rm exiting 1) that the mapping-only test could not.
func TestUninitialisedContainerExitsFour(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))
	// Deliberately no "init" call.

	cases := [][]string{
		{"new", "t1", "--workspace", ws, "--all"},
		{"path", "t1", "--workspace", ws},
		{"status", "--workspace", ws},
		{"rm", "t1", "--workspace", ws},
	}
	for _, args := range cases {
		var out, errb bytes.Buffer
		code := Run(args, &out, &errb)
		if code != 4 {
			t.Errorf("%v against an uninitialised container exited %d, want 4: stdout=%q stderr=%q",
				args, code, out.String(), errb.String())
		}
		if !strings.Contains(errb.String(), "WKT_NO_CONTAINER") {
			t.Errorf("%v: stderr must report WKT_NO_CONTAINER, got %q", args, errb.String())
		}
	}
}

// TestInitRefusesNonexistentWorkspace and TestInitRefusesWorkspaceWithNoRepos
// are Important fix 2: init must not silently succeed and create an empty
// container for a workspace that plainly isn't one — a typo in --workspace
// looks exactly like success otherwise, since discover.Walk swallows a
// root-level walk error and simply returns zero entries.
func TestInitRefusesNonexistentWorkspace(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "does-not-exist")

	var out, errb bytes.Buffer
	code := Run([]string{"init", "--workspace", ws}, &out, &errb)
	if code == 0 {
		t.Fatalf("init on a nonexistent workspace exited 0, want a typed failure; stdout=%q", out.String())
	}
	if _, err := os.Stat(ws + ".worktrees"); !os.IsNotExist(err) {
		t.Fatal("init must not create a container for a workspace that does not exist")
	}
}

func TestInitRefusesWorkspaceWithNoRepos(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := Run([]string{"init", "--workspace", ws}, &out, &errb)
	if code == 0 {
		t.Fatalf("init on a workspace with zero repositories exited 0, want a typed failure; stdout=%q", out.String())
	}
	if _, err := os.Stat(ws + ".worktrees"); !os.IsNotExist(err) {
		t.Fatal("init must not create a container for a workspace with nothing to materialise")
	}
}

// TestNewAcceptsFlagsBeforeOrAfterTheTaskName is Important fix 3: the task
// name may be typed before its flags or after them. Each subtest checks the
// flags actually took effect (a materialised tree under the given
// --workspace, a repo selected by --all), not merely that the exit code was
// 0 — a version that silently ignored --workspace and operated on "." could
// still exit 0 while doing the wrong thing entirely.
func TestNewAcceptsFlagsBeforeOrAfterTheTaskName(t *testing.T) {
	positionalFirst := func(t *testing.T, ws, task string) {
		var out, errb bytes.Buffer
		if code := Run([]string{"new", task, "--workspace", ws, "--all"}, &out, &errb); code != 0 {
			t.Fatalf("positional-first order exited %d: %s", code, errb.String())
		}
	}
	flagsFirst := func(t *testing.T, ws, task string) {
		var out, errb bytes.Buffer
		if code := Run([]string{"new", "--all", "--workspace", ws, task}, &out, &errb); code != 0 {
			t.Fatalf("flags-first order exited %d: %s", code, errb.String())
		}
	}

	for name, create := range map[string]func(t *testing.T, ws, task string){
		"positional-first": positionalFirst,
		"flags-first":      flagsFirst,
	} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			ws := filepath.Join(base, "ws")
			seedRepo(t, filepath.Join(ws, "svc-a"))
			var out, errb bytes.Buffer
			if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
				t.Fatalf("init exited %d: %s", code, errb.String())
			}

			create(t, ws, "t-order")

			out.Reset()
			errb.Reset()
			if code := Run([]string{"path", "t-order", "--workspace", ws}, &out, &errb); code != 0 {
				t.Fatalf("path exited %d: %s", code, errb.String())
			}
			// Proves both flags actually took effect, not merely that the
			// exit code was 0: if --workspace were silently ignored (falling
			// back to "."), svc-a — created only under this test's isolated
			// tempdir — would not exist under whatever tree got built, and
			// if --all were ignored, no repository would be selected at all.
			treePath := strings.TrimSpace(out.String())
			if _, err := os.Stat(filepath.Join(treePath, "svc-a")); err != nil {
				t.Fatalf("--workspace and/or --all was not honoured (svc-a missing from %q): %v", treePath, err)
			}
		})
	}
}

// TestUsageStringDoesNotAdvertiseJSON is minor fix 5: --json was advertised
// but never implemented as a flag on any command, so passing it fails with
// "flag provided but not defined". The usage text must not promise it.
func TestUsageStringDoesNotAdvertiseJSON(t *testing.T) {
	if strings.Contains(usage, "json") {
		t.Fatalf("usage text still advertises --json: %q", usage)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"status", "--json"}, &out, &errb); code != 2 {
		t.Fatalf("--json exited %d, want 2 (an unrecognised flag is a usage error)", code)
	}
}

// --- review round 3 ---

// newSplitFlagsWorkspace seeds a two-repository workspace and runs `wkt
// init` against it, returning the workspace path. Two repositories are
// essential here, not one: with only one repository, --repos and --all
// select the same tree, and a version that silently ignored --repos would
// pass undetected.
func newSplitFlagsWorkspace(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))
	seedRepo(t, filepath.Join(ws, "svc-b"))
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	return ws
}

// assertOnlySvcA runs `wkt path` for the task and asserts the materialised
// tree contains svc-a and does NOT contain svc-b — proving --repos svc-a
// was actually honoured, not merely that the command exited 0. Exit 0 alone
// is not enough: the round 3 regression exited 0 while silently
// materialising both repositories instead of the one requested.
func assertOnlySvcA(t *testing.T, ws, task string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run([]string{"path", task, "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("path exited %d: %s", code, errb.String())
	}
	treePath := strings.TrimSpace(out.String())

	// An unselected repository is still present in the tree — as a
	// back-filled symlink to the workspace, per tree.PlanFor's design, so
	// cross-repo references still resolve — so "svc-b must not exist" is
	// the wrong assertion (and was, in an earlier draft of this test,
	// incorrectly red against CORRECT code). The real distinction --repos
	// makes is real materialised worktree (a directory, on the task
	// branch) vs. back-fill symlink (unmodified, still on main).
	aInfo, err := os.Lstat(filepath.Join(treePath, "svc-a"))
	if err != nil {
		t.Fatalf("--repos svc-a must materialise svc-a: %v", err)
	}
	if aInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatal("svc-a must be a real materialised worktree, not a back-fill symlink")
	}
	if branch, err := exec.Command("git", "-C", filepath.Join(treePath, "svc-a"), "rev-parse", "--abbrev-ref", "HEAD").Output(); err != nil || strings.TrimSpace(string(branch)) != task {
		t.Fatalf("svc-a must be checked out on the task branch %q, got %q (err=%v)", task, strings.TrimSpace(string(branch)), err)
	}

	bInfo, err := os.Lstat(filepath.Join(treePath, "svc-b"))
	if err != nil {
		t.Fatalf("svc-b should still be present as a back-fill symlink: %v", err)
	}
	if bInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("--repos svc-a must NOT materialise svc-b as a real worktree; it must remain a back-fill symlink")
	}
}

// TestNewHonoursReposFlagRegardlessOfPositionalPlacement is the round 3 fix:
// --repos must take effect no matter which side of the task name it's typed
// on, including split across both sides of it — the shape that regressed
// ("new --workspace WS task --repos svc-a" silently selected every
// repository instead of refusing or honouring --repos).
func TestNewHonoursReposFlagRegardlessOfPositionalPlacement(t *testing.T) {
	t.Run("positional-first", func(t *testing.T) {
		ws := newSplitFlagsWorkspace(t)
		var out, errb bytes.Buffer
		if code := Run([]string{"new", "task", "--workspace", ws, "--repos", "svc-a"}, &out, &errb); code != 0 {
			t.Fatalf("new exited %d: %s", code, errb.String())
		}
		assertOnlySvcA(t, ws, "task")
	})

	t.Run("flags-first", func(t *testing.T) {
		ws := newSplitFlagsWorkspace(t)
		var out, errb bytes.Buffer
		if code := Run([]string{"new", "--workspace", ws, "--repos", "svc-a", "task"}, &out, &errb); code != 0 {
			t.Fatalf("new exited %d: %s", code, errb.String())
		}
		assertOnlySvcA(t, ws, "task")
	})

	// This is the shape that regressed in round 2: flags on both sides of
	// the positional. Before this fix it exited 0 and silently materialised
	// every repository in the workspace (svc-a AND svc-b) instead of
	// honouring --repos svc-a — the worst-direction failure (a safe refusal
	// traded for a silent wrong result). Verified live against the round 2
	// binary before writing this fix; see the task report for the
	// transcript.
	t.Run("split-across-the-positional", func(t *testing.T) {
		ws := newSplitFlagsWorkspace(t)
		var out, errb bytes.Buffer
		if code := Run([]string{"new", "--workspace", ws, "task", "--repos", "svc-a"}, &out, &errb); code != 0 {
			t.Fatalf("new exited %d: %s", code, errb.String())
		}
		assertOnlySvcA(t, ws, "task")
	})
}

// TestNewRefusesTwoPositionalsExitsTwo is the belt-and-braces half of the
// round 3 fix: a shape splitPositional cannot safely resolve to one
// positional (here, two bare tokens) must be refused, not silently resolved
// by picking the first and discarding the second.
func TestNewRefusesTwoPositionalsExitsTwo(t *testing.T) {
	ws := newSplitFlagsWorkspace(t)
	var out, errb bytes.Buffer
	code := Run([]string{"new", "task1", "task2", "--workspace", ws, "--all"}, &out, &errb)
	if code != 2 {
		t.Fatalf("two positionals exited %d, want 2: stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	// And nothing must have been created under either name.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"path", "task1", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("a refused two-positional new must not have created task1")
	}
}

// TestSplitPositionalDerivesValueFlagsFromTheFlagSet is review finding
// Minor 9: which flags consume a separately-typed value used to be a
// hand-maintained map, independent of the FlagSet actually being parsed. A
// future flag added to the FlagSet but forgotten in that map silently
// reintroduces the exact round-2 bug (a value flag's argument mistaken for
// the positional, or vice versa) — which already happened once on this
// branch. Registers a brand-new string flag, "--extra", that has never
// appeared in any hardcoded list anywhere, and checks that its
// separately-typed value is still skipped correctly purely because
// splitPositional now derives the classification from fs.VisitAll.
func TestSplitPositionalDerivesValueFlagsFromTheFlagSet(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("workspace", ".", "")
	fs.Bool("all", false, "")
	fs.String("extra", "", "a brand-new value flag, not in any hand-maintained list")

	positional, remaining := splitPositional(fs, []string{"--extra", "not-the-positional", "task", "--all"})
	if positional != "task" {
		t.Fatalf("got positional %q, want %q — the new value flag's argument must be skipped, not mistaken for the positional", positional, "task")
	}
	wantRemaining := []string{"--extra", "not-the-positional", "--all"}
	if strings.Join(remaining, ",") != strings.Join(wantRemaining, ",") {
		t.Fatalf("got remaining %v, want %v", remaining, wantRemaining)
	}

	// A boolean flag's own argument, by contrast, must never be skipped: it
	// takes no separately-typed value, so the very next token is fair game
	// as the positional.
	positional2, _ := splitPositional(fs, []string{"--all", "task2"})
	if positional2 != "task2" {
		t.Fatalf("got positional %q, want %q — a boolean flag must not swallow the following token", positional2, "task2")
	}
}

// TestNewWithASeparatorInTheTaskNameCreatesNothing covers adversarial
// finding F1 end to end: the refusal must happen before anything is built,
// so no debris is left in trees/ to block a later, legitimate task name.
func TestNewWithASeparatorInTheTaskNameCreatesNothing(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"new", "feature/x", "--all", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatalf("new must refuse a task name carrying a path separator; stderr=%s", errb.String())
	}
	if !strings.Contains(errb.String(), "WKT_BAD_TASK_NAME") {
		t.Fatalf("want WKT_BAD_TASK_NAME, got %s", errb.String())
	}
	trees := filepath.Join(ws+".worktrees", "trees")
	ents, err := os.ReadDir(trees)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("the refusal must leave trees/ empty, found %d entries", len(ents))
	}
	// The name whose slot the debris used to occupy must still be usable.
	out.Reset()
	if code := Run([]string{"new", "feature", "--all", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("the plain name must remain available, exited %d: %s", code, errb.String())
	}
}

// TestTreeExistsRemedyIsActionable covers the second half of F1: the old
// remedy suggested "wkt rm <task>", which answers WKT_NO_TASK when only the
// directory exists — a dead end that left the user with no documented way out.
func TestTreeExistsRemedyIsActionable(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	orphan := filepath.Join(ws+".worktrees", "trees", "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := Run([]string{"new", "orphan", "--all", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("new must refuse when the tree directory is already there")
	}
	msg := errb.String()
	if !strings.Contains(msg, "WKT_TREE_EXISTS") {
		t.Fatalf("want WKT_TREE_EXISTS, got %s", msg)
	}
	if strings.Contains(msg, "wkt rm orphan") {
		t.Fatalf("the remedy must not recommend a command that answers WKT_NO_TASK: %s", msg)
	}
	if !strings.Contains(msg, orphan) {
		t.Fatalf("the remedy must name the directory to deal with: %s", msg)
	}
}

// TestNewWarnsOnStderrWhenASelectedRepositoryHasASubmodule covers F3 at the
// CLI seam: the warning goes to stderr and the command still succeeds.
func TestNewWarnsOnStderrWhenASelectedRepositoryHasASubmodule(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(base, "lib"))
	seedRepo(t, filepath.Join(ws, "super"))
	sub := filepath.Join(ws, "super")
	for _, args := range [][]string{
		{"-c", "protocol.file.allow=always", "submodule", "add", "-q", filepath.Join(base, "lib"), "vendor"},
		{"commit", "-qm", "add submodule"},
	} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=e@x", "-c", "user.name=t"}, args...)...)
		cmd.Dir = sub
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, o)
		}
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"new", "t1", "--all", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("new must still succeed, exited %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "WKT_SUBMODULE") || !strings.Contains(errb.String(), "super") {
		t.Fatalf("new must warn on stderr that super carries a submodule, got %q", errb.String())
	}
}

// TestInitExcludeAdoptsAWorkspaceWithANestedRepository covers adversarial
// finding F4. Spec §5.3 rule 6 and the §7.1 command table both promise
// --exclude as the escape hatch for a genuine nested repository; without it,
// init refuses and a workspace containing one cannot be adopted at all.
func TestInitExcludeAdoptsAWorkspaceWithANestedRepository(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	seedRepo(t, filepath.Join(ws, "a", "inner"))

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("a genuine nested repository must be refused without --exclude")
	}
	if !strings.Contains(errb.String(), "WKT_NESTED_REPO") {
		t.Fatalf("want WKT_NESTED_REPO, got %s", errb.String())
	}
	// The refusal has to name the way out, or the user is stuck.
	if !strings.Contains(errb.String(), "--exclude") {
		t.Fatalf("the refusal must point at --exclude: %s", errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"init", "--exclude", "a/inner", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("--exclude must adopt the workspace, exited %d: %s", code, errb.String())
	}

	// Recorded in container state: a later run must not need the flag again.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("the exclusion must be remembered, exited %d: %s", code, errb.String())
	}

	// And the workspace is usable: a task over the outer repository works.
	out.Reset()
	if code := Run([]string{"new", "t1", "--all", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("new exited %d: %s", code, errb.String())
	}
}

// TestInitExcludeRefusesAPathThatIsNotANestedRepository keeps the flag from
// becoming a silent no-op for a typo, the same failure shape as defect 24.
func TestInitExcludeRefusesAPathThatIsNotANestedRepository(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	seedRepo(t, filepath.Join(ws, "a", "inner"))

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--exclude", "a/typo", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("excluding something that is not a nested repository must fail")
	}
	if !strings.Contains(errb.String(), "WKT_NO_SUCH_NESTED_REPO") {
		t.Fatalf("want WKT_NO_SUCH_NESTED_REPO, got %s", errb.String())
	}
	// Excluding the *outer* repository is not what the flag is for either:
	// it is not nested, and dropping it would leave its directory to be
	// linked whole, hiding a repository inside a shared writable link.
	errb.Reset()
	if code := Run([]string{"init", "--exclude", "a", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("excluding a non-nested repository must fail")
	}
}

// TestUsageDocumentsExclude keeps the escape hatch discoverable: a flag the
// refusal recommends but the usage never mentions is a flag nobody finds.
func TestUsageDocumentsExclude(t *testing.T) {
	if !strings.Contains(usage, "--exclude") {
		t.Fatalf("usage must document --exclude:\n%s", usage)
	}
}

// TestStatusColumnsAlignWhateverThePathLength covers live-run finding L3: the
// branch column was padded to a fixed 28 characters, so a real path like
// "DVS/Research/excalidraw-diagram-skill" pushed it out of line.
func TestStatusColumnsAlignWhateverThePathLength(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	seedRepo(t, filepath.Join(ws, "group", "research", "excalidraw-diagram-skill"))

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"new", "t1", "--all", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("new exited %d: %s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"status", "t1", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("status exited %d: %s", code, errb.String())
	}
	col := -1
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "  !") || strings.HasPrefix(line, "  i") {
			continue
		}
		at := strings.LastIndex(line, "t1")
		if at < 0 {
			continue
		}
		if col == -1 {
			col = at
		} else if at != col {
			t.Fatalf("branch column must line up for every repository:\n%s", out.String())
		}
	}
	if col == -1 {
		t.Fatalf("status printed no repository lines:\n%s", out.String())
	}
}

// TestPerimeterRegeneratesEveryTask — with no task name the verb refreshes
// them all, which is how a workspace recovers after tasks were created by a
// version that did not write perimeters, or after someone deleted one.
func TestPerimeterRegeneratesEveryTask(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t2", "--all", "--workspace", ws)

	// Delete one copy and corrupt another.
	c := containerOf(t, ws)
	gone := filepath.Join(c, "trees", "t1", ".claude", "settings.json")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(c, "trees", "t2", ".claude", "settings.json")
	if err := os.WriteFile(edited, []byte(`{"permissions":{"deny":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"perimeter", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("perimeter exited %d: %s", code, errb.String())
	}
	for _, f := range []string{gone, edited} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s was not regenerated: %v", f, err)
		}
		if !strings.Contains(string(b), "sandbox") {
			t.Fatalf("%s does not look like a regenerated perimeter:\n%s", f, b)
		}
	}

	// Regenerating has to record the new hashes, or the very next check
	// reports drift against the file it just wrote — and a report that is
	// wrong immediately after the repair teaches people to ignore it.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"perimeter", "--check", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("a check straight after a regeneration must be clean, exited %d: %s", code, out.String())
	}
}

// TestPerimeterCheckReportsDriftAndWritesNothing — --check is a report, and a
// report that repairs what it is reporting on cannot be trusted twice.
func TestPerimeterCheckReportsDriftAndWritesNothing(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)

	out.Reset()
	errb.Reset()
	if code := Run([]string{"perimeter", "--check", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("a clean perimeter must check clean, exited %d: %s", code, out.String()+errb.String())
	}

	f := filepath.Join(containerOf(t, ws), "trees", "t1", ".claude", "settings.json")
	tampered := []byte(`{"permissions":{"deny":[]}}`)
	if err := os.WriteFile(f, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Run([]string{"perimeter", "--check", "--workspace", ws}, &out, &errb); code != 3 {
		t.Fatalf("drift must exit 3, got %d: %s", code, out.String())
	}
	back, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(tampered) {
		t.Fatal("--check must not write: it repaired the file it was asked to report on")
	}
}

// TestEachVerbAcceptsOnlyItsOwnFlags covers finding F6: every verb shared one
// flag set, so "wkt path t --force --all" was accepted in silence. Input that
// means nothing must not read as success — the same rule as defect 24.
func TestEachVerbAcceptsOnlyItsOwnFlags(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)

	for _, args := range [][]string{
		{"path", "t1", "--force", "--workspace", ws},
		{"path", "t1", "--all", "--workspace", ws},
		{"init", "--repos", "a", "--workspace", ws},
		{"init", "--force", "--workspace", ws},
		{"new", "t2", "--check", "--workspace", ws},
		{"status", "--force", "--workspace", ws},
		{"rm", "t1", "--all", "--workspace", ws},
		{"perimeter", "--repos", "a", "--workspace", ws},
	} {
		out.Reset()
		errb.Reset()
		if code := Run(args, &out, &errb); code != 2 {
			t.Errorf("%v: exited %d, want 2 — a flag the verb does not take is a usage error", args, code)
		}
	}
}

// TestEachVerbStillAcceptsItsOwnFlags is the other half: the tightening must
// not remove a flag a verb is documented to take.
func TestEachVerbStillAcceptsItsOwnFlags(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	seedRepo(t, filepath.Join(ws, "b"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--dry-run", "--workspace", ws)
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--repos", "a", "--workspace", ws)
	mustRun(t, &out, &errb, "status", "t1", "--workspace", ws)
	mustRun(t, &out, &errb, "perimeter", "t1", "--workspace", ws)
	mustRun(t, &out, &errb, "rm", "t1", "--force", "--workspace", ws)
}

func TestUsageDocumentsPerimeter(t *testing.T) {
	if !strings.Contains(usage, "wkt perimeter") {
		t.Fatalf("usage must document the perimeter verb:\n%s", usage)
	}
}

func mustRun(t *testing.T, out, errb *bytes.Buffer, args ...string) {
	t.Helper()
	out.Reset()
	errb.Reset()
	if code := Run(args, out, errb); code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, out.String()+errb.String())
	}
}

func containerOf(t *testing.T, ws string) string {
	t.Helper()
	return ws + ".worktrees"
}

// TestPerimeterCheckCatchesATaskWithNoPerimeter — a task created before this
// feature existed has no recorded coverage, and that is precisely the case
// the command exists to repair. Reporting it clean would hide it.
func TestPerimeterCheckCatchesATaskWithNoPerimeter(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)

	// Rewrite the state as an older wkt would have left it: no coverage, no
	// hashes, and no files on disk either.
	stateFile := filepath.Join(containerOf(t, ws), "state", "tasks", "t1.json")
	b, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "perimeter_coverage")
	delete(raw, "perimeter_hashes")
	nb, _ := json.Marshal(raw)
	if err := os.WriteFile(stateFile, nb, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(containerOf(t, ws), "trees", "t1", ".claude")); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"perimeter", "--check", "--workspace", ws}, &out, &errb); code != 3 {
		t.Fatalf("a task with no perimeter must report drift, exited %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "uncovered") {
		t.Fatalf("the report must say what is wrong: %s", out.String())
	}

	// And the command must be able to repair exactly that.
	mustRun(t, &out, &errb, "perimeter", "--workspace", ws)
	out.Reset()
	if code := Run([]string{"perimeter", "--check", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("after repair the check must be clean, exited %d: %s", code, out.String())
	}
}

// TestStatusReportsPerimeterCoverage — status is where a user finds out what
// the perimeter actually covers, which is never "everything" (H6a).
func TestStatusReportsPerimeterCoverage(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)

	out.Reset()
	if code := Run([]string{"status", "t1", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("status exited %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "perimeter") {
		t.Fatalf("status must report perimeter coverage:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "2") {
		t.Fatalf("coverage should name how many directories are covered:\n%s", out.String())
	}
}

// TestStatusReportsPerimeterDrift — an edited copy is drift, exit 3, like any
// other drift.
func TestStatusReportsPerimeterDrift(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)

	f := filepath.Join(containerOf(t, ws), "trees", "t1", ".claude", "settings.json")
	if err := os.WriteFile(f, []byte(`{"$wkt":{"version":1},"permissions":{"deny":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Run([]string{"status", "t1", "--workspace", ws}, &out, &errb); code != 3 {
		t.Fatalf("a diverged perimeter copy is drift, exited %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "PERIMETER") {
		t.Fatalf("status must name the perimeter problem:\n%s", out.String())
	}
}

// TestStatusReportsAStalePerimeter — a perimeter that predates a sibling does
// not name that sibling's tree, and H16 means a wide glob cannot cover it. The
// user has to be told, because nothing else will.
func TestStatusReportsAStalePerimeter(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)

	// Freeze t1's perimeter directory so creating t2 cannot refresh it.
	dir := filepath.Join(containerOf(t, ws), "trees", "t1", ".claude")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	mustRun(t, &out, &errb, "new", "t2", "--all", "--workspace", ws)

	out.Reset()
	code := Run([]string{"status", "t1", "--workspace", ws}, &out, &errb)
	if code != 3 {
		t.Fatalf("a stale perimeter is drift, exited %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "STALE") {
		t.Fatalf("status must say the perimeter is out of date:\n%s", out.String())
	}
}

// TestStatusNeverClaimsIsolation pins a promise the project has already broken
// once (finding F2): v0 makes no isolation claim at all, and a promise is
// cheaper to keep with a test than with memory.
func TestStatusNeverClaimsIsolation(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)
	out.Reset()
	Run([]string{"status", "--workspace", ws}, &out, &errb)
	blob := strings.ToLower(out.String() + errb.String() + usage)
	for _, word := range []string{"isolated", "isolation", "sandboxed from", "secure"} {
		if strings.Contains(blob, word) {
			t.Fatalf("wkt claims %q; v0.1 makes no isolation claim (spec §0, §9):\n%s", word, blob)
		}
	}
}

// TestRemovingATaskRefreshesTheSurvivorsPerimeter — new refreshes siblings, so
// rm must too, or every removal leaves the others naming a tree that is gone.
func TestRemovingATaskRefreshesTheSurvivorsPerimeter(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "keeper", "--all", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "goner", "--all", "--workspace", ws)

	f := filepath.Join(containerOf(t, ws), "trees", "keeper", ".claude", "settings.json")
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "goner") {
		t.Fatal("precondition: keeper's perimeter should name goner")
	}
	mustRun(t, &out, &errb, "rm", "goner", "--workspace", ws)

	after, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "goner") {
		t.Fatal("after removal the survivor's perimeter must stop naming the tree that is gone")
	}
	out.Reset()
	if code := Run([]string{"status", "keeper", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("the survivor must not be left in drift, exited %d:\n%s", code, out.String())
	}
}

// TestVersionVerb — the release build stamps a version in, and a binary that
// cannot say which one it is makes every bug report guesswork.
func TestVersionVerb(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		var out, errb bytes.Buffer
		if code := Run([]string{arg}, &out, &errb); code != 0 {
			t.Fatalf("%s exited %d: %s", arg, code, errb.String())
		}
		if !strings.HasPrefix(out.String(), "wkt ") {
			t.Fatalf("%s printed %q", arg, out.String())
		}
	}
	if !strings.Contains(usage, "wkt version") {
		t.Fatal("usage must document the version verb")
	}
}

// TestDoctorReportsAndFixes — the verb's contract at the CLI seam: quiet on a
// healthy container, exit 3 on a problem, and --all lists what wkt wrote on
// purpose, which is the uninstall inventory.
func TestDoctorReportsAndFixes(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)

	out.Reset()
	if code := Run([]string{"doctor", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("a healthy container must exit 0, got %d:\n%s", code, out.String())
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("a healthy container must be quiet, got:\n%s", out.String())
	}

	// --all is the uninstall answer: it names the ref wkt put in the repo.
	out.Reset()
	if code := Run([]string{"doctor", "--all", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("--all must not turn information into failure, got %d", code)
	}
	if !strings.Contains(out.String(), "refs/wkt/base/t1") {
		t.Fatalf("--all must list what wkt wrote into the workspace:\n%s", out.String())
	}

	// A problem: debris in trees/ that no task claims.
	orphan := filepath.Join(containerOf(t, ws), "trees", "leftover")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Run([]string{"doctor", "--workspace", ws}, &out, &errb); code != 3 {
		t.Fatalf("a problem must exit 3, got %d:\n%s", code, out.String())
	}

	out.Reset()
	if code := Run([]string{"doctor", "--fix", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("--fix must clear it, got %d:\n%s", code, out.String())
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("--fix must remove the empty leftover")
	}
}

// TestHookWorktreeCreateEmitsOneLineAndIsIdempotent — the whole contract is
// that single line, and "--resume --worktree" re-fires the event (H14), so
// firing twice for the same name must hand back the same tree rather than
// failing with WKT_TASK_EXISTS.
func TestHookWorktreeCreateEmitsOneLineAndIsIdempotent(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	seedRepo(t, filepath.Join(ws, "b"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)

	run := func(payload string) (string, int) {
		out.Reset()
		errb.Reset()
		stdin = strings.NewReader(payload)
		defer func() { stdin = os.Stdin }()
		code := Run([]string{"hook", "worktree-create", "--workspace", ws}, &out, &errb)
		return out.String(), code
	}

	got, code := run(`{"session_id":"s","cwd":"` + ws + `","name":"feat-42"}`)
	if code != 0 {
		t.Fatalf("hook exited %d: %s", code, errb.String())
	}
	if n := strings.Count(strings.TrimRight(got, "\n"), "\n"); n != 0 {
		t.Fatalf("stdout must be exactly one line, got %d extra:\n%q", n, got)
	}
	tree := strings.TrimSpace(got)
	if !filepath.IsAbs(tree) {
		t.Fatalf("the hook must emit an absolute path, got %q", tree)
	}
	if strings.Contains(tree, "/../") || strings.Contains(tree, "/./") {
		t.Fatalf("the binary rejects a path with dot segments: %q", tree)
	}
	if st, err := os.Stat(tree); err != nil || !st.IsDir() {
		t.Fatalf("the emitted path must be a directory: %v", err)
	}

	again, code := run(`{"session_id":"s","cwd":"` + ws + `","name":"feat-42"}`)
	if code != 0 {
		t.Fatalf("re-firing must succeed, exited %d: %s", code, errb.String())
	}
	if strings.TrimSpace(again) != tree {
		t.Fatalf("re-firing must return the same tree:\n%q\n%q", again, tree)
	}
}

// TestHookWorktreeCreateSanitisesTheSuggestedName — the slug is a suggestion,
// not a validated task name, and the contract has nowhere to report a rename.
func TestHookWorktreeCreateSanitisesTheSuggestedName(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)

	out.Reset()
	errb.Reset()
	stdin = strings.NewReader(`{"name":"feature/x","cwd":"` + ws + `"}`)
	defer func() { stdin = os.Stdin }()
	if code := Run([]string{"hook", "worktree-create", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("a slug with a separator must still produce a worktree, exited %d: %s", code, errb.String())
	}
	tree := strings.TrimSpace(out.String())
	if filepath.Base(tree) != "feature-x" {
		t.Fatalf("want the sanitised name, got %q", tree)
	}
}

// TestHookWorktreeRemoveAcceptsAnySpellingOfThePath — the payload's path may
// arrive as typed or resolved, and on macOS a temp path resolves through
// /private, so the two differ. Comparing by string equality would miss one of
// them, which is why the lookup goes through paths.Spellings.
func TestHookWorktreeRemoveAcceptsAnySpellingOfThePath(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)

	// The un-resolved spelling is the interesting one: state records the
	// canonical path, so this is the case a naive comparison gets wrong.
	raw := filepath.Join(containerOf(t, ws), "trees", "t-raw")
	resolved := raw
	if r, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(raw))); err == nil {
		resolved = filepath.Join(r, "trees", "t-raw")
	}
	if raw == resolved {
		t.Skip("this platform has no second spelling for the temp path")
	}

	for name, spelling := range map[string]string{"t-raw": raw, "t-res": resolved} {
		mustRun(t, &out, &errb, "new", name, "--all", "--workspace", ws)
		p := strings.Replace(spelling, "t-raw", name, 1)
		out.Reset()
		errb.Reset()
		stdin = strings.NewReader(`{"worktree_path":"` + p + `"}`)
		if code := Run([]string{"hook", "worktree-remove", "--workspace", ws}, &out, &errb); code != 0 {
			stdin = os.Stdin
			t.Fatalf("removing by the %s spelling exited %d: %s", name, code, errb.String())
		}
		stdin = os.Stdin
		if _, err := os.Stat(filepath.Join(containerOf(t, ws), "trees", name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone", name)
		}
	}
}

// TestHookWorktreeRemoveKeepsTheRefusals — the hook is a different entry
// point, not a way around teardown. Its stderr is what the user sees.
func TestHookWorktreeRemoveKeepsTheRefusals(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	mustRun(t, &out, &errb, "init", "--workspace", ws)
	mustRun(t, &out, &errb, "new", "t1", "--all", "--workspace", ws)

	tree := filepath.Join(containerOf(t, ws), "trees", "t1")
	if err := os.WriteFile(filepath.Join(tree, "a", "uncommitted.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	stdin = strings.NewReader(`{"worktree_path":"` + tree + `"}`)
	defer func() { stdin = os.Stdin }()
	if code := Run([]string{"hook", "worktree-remove", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("uncommitted work must still block a hook-driven removal")
	}
	if !strings.Contains(errb.String(), "WKT_WOULD_LOSE_WORK") {
		t.Fatalf("the refusal must reach stderr, where the user sees it: %q", errb.String())
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatal("the refused removal must not have deleted anything")
	}
}

// TestHookInstallPrintsSomethingUsable — the settings block is what a user
// pastes; it has to name the real binary and both events.
func TestHookInstallPrintsSomethingUsable(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"hook", "install"}, &out, &errb); code != 0 {
		t.Fatalf("exited %d: %s", code, errb.String())
	}
	blob := out.String()
	for _, want := range []string{"WorktreeCreate", "WorktreeRemove", "hook worktree-create", "hook worktree-remove"} {
		if !strings.Contains(blob, want) {
			t.Fatalf("the printed settings must mention %q:\n%s", want, blob)
		}
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(blob[strings.Index(blob, "{"):strings.LastIndex(blob, "}")+1]), &probe); err != nil {
		t.Fatalf("the printed block must be valid JSON: %v\n%s", err, blob)
	}
}
