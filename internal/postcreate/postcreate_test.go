package postcreate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Venut-Labs/wkt/internal/wkterr"
)

// script writes an executable post-create into a workspace.
func script(t *testing.T, ws, body string) string {
	t.Helper()
	dir := filepath.Join(ws, ".wkt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "post-create")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var e *wkterr.E
	if !errors.As(err, &e) {
		t.Fatalf("not a wkt error: %v", err)
	}
	return e.Code
}

// The seam is opt-in, and the common case is not having one.
func TestAbsentScriptIsANoOp(t *testing.T) {
	res, err := Run(Request{Workspace: t.TempDir(), TreeRoot: t.TempDir(), Task: "t", Out: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("an absent script is not an error: %v", err)
	}
	if res.Ran {
		t.Fatal("nothing should have run")
	}
}

// A guard that quietly does nothing is the worst kind a guard can be — this
// project has already been bitten by one, in a deny rule that was accepted
// and enforced nothing. A script someone wrote and forgot to chmod says so.
func TestNonExecutableScriptIsRefused(t *testing.T) {
	ws := t.TempDir()
	p := script(t, ws, "#!/bin/sh\ntrue\n")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Request{Workspace: ws, TreeRoot: t.TempDir(), Task: "t", Out: &bytes.Buffer{}})
	if code := codeOf(t, err); code != "WKT_POST_CREATE_NOT_EXECUTABLE" {
		t.Fatalf("want WKT_POST_CREATE_NOT_EXECUTABLE, got %q (err %v)", code, err)
	}
}

// wkt does not execute through a link whose target it has not established,
// the same rule perimeter.checkOwned follows for the settings file.
func TestSymlinkedScriptIsRefused(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".wkt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(ws, ".wkt", "post-create")); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Request{Workspace: ws, TreeRoot: t.TempDir(), Task: "t", Out: &bytes.Buffer{}})
	if code := codeOf(t, err); code != "WKT_POST_CREATE_FOREIGN" {
		t.Fatalf("want WKT_POST_CREATE_FOREIGN, got %q (err %v)", code, err)
	}
}

// Measured: wkt new accepts a;b, a$b, a&b and a`b`, all legal branch names.
// wkt itself is safe — the script is executed directly, never through a
// shell — but handing a script a WKT_TREE with metacharacters in it arms
// every unquoted expansion the script contains, and "rm -rf $WKT_TREE" in a
// cleanup path is the obvious way to be hurt by that.
func TestUnsafeTaskNameIsRefusedBeforeAnythingRuns(t *testing.T) {
	ws := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	script(t, ws, "#!/bin/sh\ntouch "+marker+"\n")
	_, err := Run(Request{Workspace: ws, TreeRoot: t.TempDir(), Task: "a;b", Out: &bytes.Buffer{}})
	if code := codeOf(t, err); code != "WKT_POST_CREATE_UNSAFE_NAME" {
		t.Fatalf("want WKT_POST_CREATE_UNSAFE_NAME, got %q (err %v)", code, err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the script ran despite the refusal")
	}
}

func TestSafeNameAcceptsWhatTasksActuallyUse(t *testing.T) {
	for _, ok := range []string{"feat-42", "feat_42", "feat.42", "Feat42"} {
		if !SafeName(ok) {
			t.Fatalf("%q must be accepted", ok)
		}
	}
	for _, bad := range []string{"a;b", "a$b", "a&b", "a`b`", "a b", "", "a/b", "a\nb"} {
		if SafeName(bad) {
			t.Fatalf("%q must be refused", bad)
		}
	}
}
