package tree

import (
	"os"
	"path/filepath"
	"testing"

	"wkt/internal/discover"
)

func TestBackFilledRepoKeepsCrossRepoRelativePathResolving(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "shared", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "shared", "src", "index.js"), []byte("export const X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []discover.Entry{
		{RelPath: "services/svc-a", AbsPath: filepath.Join(ws, "services", "svc-a"), Kind: discover.KindRepo},
		{RelPath: "shared", AbsPath: filepath.Join(ws, "shared"), Kind: discover.KindRepo},
	}
	p, err := PlanFor(ws, entries, []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.BackFill) != 1 || p.BackFill[0] != "shared" {
		t.Fatalf("shared must be back-filled, got %+v", p)
	}

	treeRoot := filepath.Join(base, "tree")
	if err := os.MkdirAll(filepath.Join(treeRoot, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialise(treeRoot, ws, p); err != nil {
		t.Fatal(err)
	}
	// The reference the source code actually contains: ../../shared from services/svc-a
	target := filepath.Join(treeRoot, "services", "svc-a", "..", "..", "shared", "src", "index.js")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("../../shared must resolve through the back-filled symlink: %v", err)
	}
}

func TestAncestorDirectoriesAreRealNotSymlinked(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries := []discover.Entry{
		{RelPath: "services/svc-a", AbsPath: filepath.Join(ws, "services", "svc-a"), Kind: discover.KindRepo},
	}
	p, err := PlanFor(ws, entries, []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	treeRoot := filepath.Join(base, "tree")
	if err := os.MkdirAll(filepath.Join(treeRoot, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialise(treeRoot, ws, p); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(treeRoot, "services"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("a directory on the path to a selected repo must be real, never a symlink (spec §5.3 rule 2)")
	}
}
