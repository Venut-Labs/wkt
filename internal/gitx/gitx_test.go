package gitx

import (
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
