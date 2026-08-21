package perimeter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Venut-Labs/wkt/internal/container"
	"github.com/Venut-Labs/wkt/internal/state"
	"github.com/Venut-Labs/wkt/internal/wkterr"
)

// treeFixture builds a container with a real task tree on disk: one
// materialised repository directory and one back-filled repository that is a
// symlink into the workspace, which is the case that loses data if Write gets
// it wrong.
func treeFixture(t *testing.T) (container.C, state.Task) {
	t.Helper()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	for _, d := range []string{"services/svc-a", "services/svc-b"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c, err := container.Locate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Create(c); err != nil {
		t.Fatal(err)
	}
	tree := c.TreePath("feat-42")
	if err := os.MkdirAll(filepath.Join(tree, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	// svc-b is back-filled: the tree entry is a symlink at the mirrored
	// position, pointing into the user's workspace.
	if err := os.Symlink(filepath.Join(c.Workspace, "services", "svc-b"),
		filepath.Join(tree, "services", "svc-b")); err != nil {
		t.Fatal(err)
	}
	task := state.Task{
		Name:      "feat-42",
		Container: c.Root,
		Workspace: c.Workspace,
		Repos: []state.Repo{{
			RelPath:      "services/svc-a",
			AbsPath:      filepath.Join(c.Workspace, "services", "svc-a"),
			StoreID:      "services-svc-a-deadbeef",
			WorktreePath: filepath.Join(tree, "services", "svc-a"),
		}},
		Links: []state.LinkSlot{{
			RelPath: "services/svc-b",
			Target:  filepath.Join(c.Workspace, "services", "svc-b"),
			Type:    "symlink",
		}},
	}
	return c, task
}

// TestWriteCoversTheTreeRootAndEachMaterialisedRepository — H6a: a perimeter
// file covers only the directory it sits in, so one copy at the root is not
// enough for a session started in a repository.
func TestWriteCoversTheTreeRootAndEachMaterialisedRepository(t *testing.T) {
	c, task := treeFixture(t)
	coverage, hashes, err := Write(c, task, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree := c.TreePath("feat-42")
	want := []string{tree, filepath.Join(tree, "services", "svc-a")}
	if len(coverage) != len(want) {
		t.Fatalf("coverage %v, want %v", coverage, want)
	}
	for _, dir := range want {
		if !contains(coverage, dir) {
			t.Errorf("coverage must list %s: %v", dir, coverage)
		}
		f := filepath.Join(dir, ".claude", "settings.json")
		b, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("missing perimeter copy at %s: %v", f, readErr)
		}
		if !json.Valid(b) {
			t.Fatalf("perimeter copy at %s is not valid JSON", f)
		}
		if _, ok := hashes[dir]; !ok {
			t.Errorf("no hash recorded for %s: %v", dir, hashes)
		}
	}
}

// TestWriteNeverFollowsABackFillLink is the one that loses data if it fails:
// the tree entry for an unselected repository is a symlink into the user's
// workspace, and writing "into the tree" there writes into their repository.
func TestWriteNeverFollowsABackFillLink(t *testing.T) {
	c, task := treeFixture(t)
	if _, _, err := Write(c, task, nil); err != nil {
		t.Fatal(err)
	}
	leaked := filepath.Join(c.Workspace, "services", "svc-b", ".claude")
	if _, err := os.Lstat(leaked); !os.IsNotExist(err) {
		t.Fatalf("Write followed a back-fill link into the workspace: %s exists", leaked)
	}
	viaLink := filepath.Join(c.TreePath("feat-42"), "services", "svc-b", ".claude")
	if _, err := os.Lstat(viaLink); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written through a link slot: %s exists", viaLink)
	}
}

// TestWriteRefusesToClobberSettingsItDoesNotOwn — a repository may carry its
// own .claude/settings.json, checked into git. Overwriting it silently would
// destroy the user's configuration.
func TestWriteRefusesToClobberSettingsItDoesNotOwn(t *testing.T) {
	c, task := treeFixture(t)
	repo := filepath.Join(c.TreePath("feat-42"), "services", "svc-a")
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := []byte(`{"permissions":{"allow":["Bash(make *)"]}}`)
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), mine, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := Write(c, task, nil)
	if err == nil {
		t.Fatal("a settings file wkt did not write must not be overwritten")
	}
	var e *wkterr.E
	if !errors.As(err, &e) || e.Code != "WKT_PERIMETER_FOREIGN" {
		t.Fatalf("want WKT_PERIMETER_FOREIGN, got %v", err)
	}
	if !strings.Contains(e.Path, "svc-a") {
		t.Fatalf("the refusal must name the file it protected: %+v", e)
	}
	back, _ := os.ReadFile(filepath.Join(repo, ".claude", "settings.json"))
	if string(back) != string(mine) {
		t.Fatal("the user's settings file was modified by a refused write")
	}
}

// TestWriteReplacesItsOwnCopy: a recorded hash means wkt owns the file, so
// regeneration must proceed — otherwise no perimeter could ever be refreshed.
func TestWriteReplacesItsOwnCopy(t *testing.T) {
	c, task := treeFixture(t)
	_, hashes, err := Write(c, task, nil)
	if err != nil {
		t.Fatal(err)
	}
	task.PerimeterHashes = hashes
	if _, _, err := Write(c, task, []string{"another-task"}); err != nil {
		t.Fatalf("wkt must be able to rewrite the copy it owns: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(c.TreePath("feat-42"), ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "another-task") {
		t.Fatal("the regenerated perimeter must name the new sibling")
	}
}

// TestWriteLeavesNoTemporaryFiles — the write is atomic, so a reader never
// sees a partial perimeter and no debris survives.
func TestWriteLeavesNoTemporaryFiles(t *testing.T) {
	c, task := treeFixture(t)
	if _, _, err := Write(c, task, nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(c.TreePath("feat-42"), ".claude")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "settings.json" {
			t.Fatalf("unexpected file left in %s: %s", dir, e.Name())
		}
	}
}

// TestVerifyReportsMissingAndDiverged — what status and doctor read back.
func TestVerifyReportsMissingAndDiverged(t *testing.T) {
	c, task := treeFixture(t)
	coverage, hashes, err := Write(c, task, nil)
	if err != nil {
		t.Fatal(err)
	}
	task.PerimeterCoverage, task.PerimeterHashes = coverage, hashes

	if d, vErr := Verify(c, task); vErr != nil || len(d) != 0 {
		t.Fatalf("a freshly written perimeter must verify clean: %v %v", d, vErr)
	}

	// One copy edited by hand, one deleted.
	root := filepath.Join(c.TreePath("feat-42"), ".claude", "settings.json")
	if err := os.WriteFile(root, []byte(`{"permissions":{"deny":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	repoCopy := filepath.Join(c.TreePath("feat-42"), "services", "svc-a", ".claude", "settings.json")
	if err := os.Remove(repoCopy); err != nil {
		t.Fatal(err)
	}

	div, err := Verify(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var sawDiverged, sawMissing bool
	for _, d := range div {
		switch d.Reason {
		case "diverged":
			sawDiverged = d.Dir == c.TreePath("feat-42")
		case "missing":
			sawMissing = strings.HasSuffix(d.Dir, "svc-a")
		}
	}
	if !sawDiverged || !sawMissing {
		t.Fatalf("want one diverged and one missing copy, got %+v", div)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestWriteAdoptsItsOwnMarkedFileWhenStateForgot — state can be lost,
// hand-edited, or written by a wkt that predates the perimeter. The file
// carries a marker saying who wrote it, so the command that exists to repair
// that case is not blocked by its own guard.
func TestWriteAdoptsItsOwnMarkedFileWhenStateForgot(t *testing.T) {
	c, task := treeFixture(t)
	if _, _, err := Write(c, task, nil); err != nil {
		t.Fatal(err)
	}
	// The file stays on disk; state forgets all about it.
	task.PerimeterCoverage, task.PerimeterHashes = nil, nil

	if _, _, err := Write(c, task, []string{"later-task"}); err != nil {
		t.Fatalf("wkt must adopt a file carrying its own marker: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(c.TreePath("feat-42"), ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "later-task") {
		t.Fatal("the adopted file must be regenerated, not merely tolerated")
	}
}

// TestWriteStillRefusesAnUnmarkedFile is the other half: adoption keys on the
// marker, not on the filename, so the user's own settings are still safe.
func TestWriteStillRefusesAnUnmarkedFile(t *testing.T) {
	c, task := treeFixture(t)
	dir := filepath.Join(c.TreePath("feat-42"), ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Valid JSON, plausibly a settings file, but not wkt's.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"permissions":{"deny":["Edit(//tmp/**)"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Write(c, task, nil); err == nil {
		t.Fatal("a settings file without wkt's marker must still be refused")
	}
}
