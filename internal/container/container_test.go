package container

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"wkt/internal/paths"
	"wkt/internal/wkterr"
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

func TestLocateRejectsWhenFallbackWouldBeInsideWorkspace(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "work")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o755) })

	// Set HOME to the workspace so the fallback would be inside it
	oldHome := os.Getenv("HOME")
	t.Setenv("HOME", ws)
	t.Cleanup(func() { os.Setenv("HOME", oldHome) })

	_, err := Locate(ws)
	if err == nil {
		t.Fatal("Locate must refuse when the fallback container would be inside the workspace")
	}
	e, ok := err.(*wkterr.E)
	if !ok {
		t.Fatalf("error is not *wkterr.E: %T", err)
	}
	if e.Code != "WKT_NO_CONTAINER" {
		t.Fatalf("error code is %q, expected WKT_NO_CONTAINER", e.Code)
	}
}

func TestLockIsExclusive(t *testing.T) {
	base := t.TempDir()
	c := C{Root: filepath.Join(base, "c"), Workspace: base}
	if err := Create(c); err != nil {
		t.Fatal(err)
	}

	lockPath := filepath.Join(c.Root, ".wkt.lock")
	release, err := Lock(c)
	if err != nil {
		t.Fatal(err)
	}

	// Capture inode after first lock
	info1, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	inode1 := info1.Sys().(*syscall.Stat_t).Ino

	if _, err := Lock(c); err == nil {
		release()
		t.Fatal("a second lock must fail while the first is held")
	}
	release()

	release2, err := Lock(c)
	if err != nil {
		t.Fatalf("lock after release must succeed: %v", err)
	}

	// Verify inode is the same after second lock
	info2, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	inode2 := info2.Sys().(*syscall.Stat_t).Ino

	if inode1 != inode2 {
		t.Fatalf("lock file inode changed: %d != %d (proves lock file was unlinked and recreated)", inode1, inode2)
	}

	release2()
}
