package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReturnsTrimmedStdout(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-q"); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := Run(dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	if out != "true" {
		t.Fatalf("got %q, want %q", out, "true")
	}
}

func TestRunWrapsFailureWithoutLeakingFullStderr(t *testing.T) {
	dir := t.TempDir()
	// Initialize a git repo so the command starts, but use an invalid flag to trigger multi-line stderr
	if _, err := Run(dir, "init", "-q"); err != nil {
		t.Fatalf("init: %v", err)
	}
	// git checkout --nonexistent-flag produces multiple lines of stderr:
	// "error: unknown option `nonexistent-flag'\nusage: git checkout ..."
	_, err := Run(dir, "checkout", "--nonexistent-flag")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "WKT_GIT_FAILED") {
		t.Fatalf("error %q does not carry the code", msg)
	}
	// Error message must be single line
	if strings.Count(msg, "\n") > 0 {
		t.Fatalf("error must be a single line, got %q", msg)
	}
	// Error message must contain first line of stderr (starting with "error:")
	if !strings.Contains(msg, "error:") {
		t.Fatalf("error should contain first stderr line, got %q", msg)
	}
	// Error message must NOT contain usage lines (later lines of stderr)
	if strings.Contains(msg, "usage:") {
		t.Fatalf("error should not contain later stderr lines, got %q", msg)
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input   string
		wantMaj int
		wantMin int
		wantErr bool
	}{
		{"git version 2.50.1", 2, 50, false},
		{"git version 2.50.1 (Apple Git-155)", 2, 50, false},
		{"git version 2.29.0", 2, 29, false},
		{"git version 2.29", 2, 29, false},
		{"git version 3.0.0", 3, 0, false},
		{"git version", 0, 0, true},          // not enough fields
		{"git version abc", 0, 0, true},      // invalid major
		{"git version 2.abc.0", 2, 0, false}, // invalid minor defaults to 0
		{"malformed output", 0, 0, true},     // not enough fields
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			maj, min, err := parseVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseVersion(%q): got err=%v, want err=%v", tt.input, err != nil, tt.wantErr)
			}
			if !tt.wantErr {
				if maj != tt.wantMaj || min != tt.wantMin {
					t.Errorf("parseVersion(%q): got (%d, %d), want (%d, %d)", tt.input, maj, min, tt.wantMaj, tt.wantMin)
				}
			}
		})
	}
}

// TestFirstUsefulLine covers what wkt shows a user when git fails.
//
// Measured against real git 2.50 (a required smudge filter that exits 3):
//
//	fetching objects                                       <- the filter's own noise
//	error: external filter 'sh -c '…' --token=glpat-…' failed 3
//	error: external filter 'sh -c '…' --token=glpat-…' failed
//	fatal: f.txt: smudge filter leaky failed               <- the only useful line
//
// Taking the first line — which is what wkt did — reports the filter's progress
// message and never says the words "filter" or "smudge". Taking the error line
// instead would echo the configured command, tokens included. Prefer fatal.
func TestFirstUsefulLine(t *testing.T) {
	const cmd = "sh -c 'echo fetching objects >&2; exit 3' --token=glpat-SECRET123 %f"
	for name, tc := range map[string]struct{ in, want string }{
		"prefers fatal over filter noise": {
			in: "fetching objects\n" +
				"error: external filter '" + cmd + "' failed 3\n" +
				"error: external filter '" + cmd + "' failed\n" +
				"fatal: f.txt: smudge filter leaky failed\n",
			want: "fatal: f.txt: smudge filter leaky failed",
		},
		"redacts the command when there is no fatal line": {
			// A clean filter with required=false produces exactly this and
			// nothing else — measured.
			in:   "error: external filter '" + cmd + "' failed 3\n",
			want: "error: external filter '<configured command withheld>' failed 3",
		},
		"ordinary usage error is unchanged": {
			in:   "error: unknown option `nonexistent-flag'\nusage: git checkout ...\n",
			want: "error: unknown option `nonexistent-flag'",
		},
		"a fatal line further down still wins": {
			in:   "warning: something\nfatal: the real reason\n",
			want: "fatal: the real reason",
		},
		"empty": {in: "", want: ""},
	} {
		if got := firstUsefulLine(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
	}
}

// TestRunNeverEchoesAConfiguredFilterCommand drives it through real git,
// because the string git actually emits is the thing under test.
func TestRunNeverEchoesAConfiguredFilterCommand(t *testing.T) {
	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		if _, err := Run(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	mustRun("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("* filter=leaky\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun("add", "-A")
	mustRun("-c", "user.email=e@x", "-c", "user.name=t", "commit", "-qm", "init")
	mustRun("config", "filter.leaky.smudge", "sh -c 'echo fetching objects >&2; exit 3' --token=glpat-SECRET123 %f")
	mustRun("config", "filter.leaky.required", "true")
	if err := os.Remove(filepath.Join(dir, "f.txt")); err != nil {
		t.Fatal(err)
	}

	_, err := Run(dir, "checkout", "--", "f.txt")
	if err == nil {
		t.Fatal("a required filter that exits non-zero must fail the checkout")
	}
	msg := err.Error()
	if strings.Contains(msg, "glpat-SECRET123") {
		t.Fatalf("the error echoes a secret out of the user's git config: %q", msg)
	}
	if strings.Contains(msg, "fetching objects") {
		t.Fatalf("the error reports the filter's progress noise instead of the cause: %q", msg)
	}
	if !strings.Contains(msg, "filter") {
		t.Fatalf("the error must name what actually failed: %q", msg)
	}
}
