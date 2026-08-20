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
	_, err := Run(dir, "rev-parse", "--verify", "refs/heads/nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "WKT_GIT_FAILED") {
		t.Fatalf("error %q does not carry the code", msg)
	}
	if strings.Count(msg, "\n") > 0 {
		t.Fatalf("error must be a single line, got %q", msg)
	}
}

func TestVersionMeetsFloor(t *testing.T) {
	major, minor, err := Version()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if major < 2 || (major == 2 && minor < 29) {
		t.Skipf("git %d.%d is below the 2.29 floor; nothing to assert", major, minor)
	}
}
