package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpellingsIncludeCanonicalAndGiven(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got := Spellings(link)
	if len(got) < 2 {
		t.Fatalf("want at least the given and canonical spellings, got %v", got)
	}
	canon, _ := Canonical(link)
	var sawGiven, sawCanon bool
	for _, s := range got {
		if s == link {
			sawGiven = true
		}
		if s == canon {
			sawCanon = true
		}
	}
	if !sawGiven || !sawCanon {
		t.Fatalf("spellings %v must contain both %q and %q", got, link, canon)
	}
}

func TestIsUnderRejectsLexicalSiblings(t *testing.T) {
	base := t.TempDir()
	b := filepath.Join(base, "b")
	bc := filepath.Join(base, "bc")
	for _, d := range []string{b, bc} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if IsUnder(bc, b) {
		t.Fatalf("%q must not be considered under %q", bc, b)
	}
	if !IsUnder(filepath.Join(b, "x"), b) {
		t.Fatal("a real child must be under its parent")
	}
}

func TestIsUnderMultiLevelNonexistent(t *testing.T) {
	base := t.TempDir()
	b := filepath.Join(base, "b")
	if err := os.Mkdir(b, 0o755); err != nil {
		t.Fatal(err)
	}
	// x and y both don't exist
	if !IsUnder(filepath.Join(b, "x", "y"), b) {
		t.Fatal("b/x/y must be under b even though x and y don't exist")
	}
}

func TestIsUnderMultiLevelNonexistentThroughSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "x", "y")
	// Neither x nor y exist; resolution must recursively walk up through the symlink
	if !IsUnder(target, real) {
		t.Fatal("link/x/y must be under real when link -> real, even though x and y don't exist")
	}
}
