package cli

import (
	"bytes"
	"flag"
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
