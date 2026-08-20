package container

import (
	"os"
	"path/filepath"
	"testing"

	"wkt/internal/paths"
)

func TestLocateIsSiblingAndNeverInsideWorkspace(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "work")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := Locate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if c.Root != c.Workspace+".worktrees" {
		t.Fatalf("container %q is not the documented sibling of %q", c.Root, c.Workspace)
	}
	if paths.IsUnder(c.Root, c.Workspace) {
		t.Fatalf("container %q must never live inside the workspace", c.Root)
	}
	canonWs, err := paths.Canonical(ws)
	if err != nil {
		t.Fatal(err)
	}
	if c.Workspace != canonWs {
		t.Fatalf("container workspace %q is not canonical (expected %q)", c.Workspace, canonWs)
	}
}

func TestLocateFallsBackWhenParentUnwritable(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "work")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o755) })

	c, err := Locate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if c.Root == ws+".worktrees" {
		t.Fatal("an unwritable parent must force the state-directory fallback")
	}
}

func TestLockIsExclusive(t *testing.T) {
	base := t.TempDir()
	c := C{Root: filepath.Join(base, "c"), Workspace: base}
	if err := Create(c); err != nil {
		t.Fatal(err)
	}
	release, err := Lock(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(c); err == nil {
		release()
		t.Fatal("a second lock must fail while the first is held")
	}
	release()
	release2, err := Lock(c)
	if err != nil {
		t.Fatalf("lock after release must succeed: %v", err)
	}
	release2()
}
