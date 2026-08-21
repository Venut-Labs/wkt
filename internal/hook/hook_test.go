package hook

import (
	"strings"
	"testing"
)

// TestParseCreateToleratesBothPayloadShapes — H14: the payload differs by
// entry path, five keys on the --worktree startup path and six on the
// EnterWorktree path, and it is explicitly unstable. Parsing must take what it
// needs and ignore the rest.
func TestParseCreateToleratesBothPayloadShapes(t *testing.T) {
	startup := `{"session_id":"s1","transcript_path":"/nope/not/created/yet.jsonl",
	             "cwd":"/tmp/ws","hook_event_name":"WorktreeCreate","name":"feat-42"}`
	enter := `{"session_id":"s1","transcript_path":"/tmp/t.jsonl","cwd":"/tmp/ws",
	           "hook_event_name":"WorktreeCreate","name":"feat-42","prompt_id":"p9",
	           "something_added_next_release":true}`
	for _, in := range []string{startup, enter} {
		got, err := ParseCreate(strings.NewReader(in))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.Name != "feat-42" {
			t.Fatalf("name %q", got.Name)
		}
		if got.Cwd != "/tmp/ws" {
			t.Fatalf("cwd %q", got.Cwd)
		}
	}
}

// TestParseCreateRejectsAnEmptyName — the one field the contract promises. A
// hook that invents a name when the payload has none creates a task nobody
// asked for.
func TestParseCreateRejectsAnEmptyName(t *testing.T) {
	for _, in := range []string{`{}`, `{"name":""}`, `{"name":"   "}`, `not json`} {
		if _, err := ParseCreate(strings.NewReader(in)); err == nil {
			t.Fatalf("%q must be refused", in)
		}
	}
}

// TestSlugIsOnePathSegment — the suggested name is not a validated task name.
// It becomes a directory and a branch, so it has to survive both, and the
// hook contract has no channel for "I renamed your slug" — it must succeed.
func TestSlugIsOnePathSegment(t *testing.T) {
	for in, want := range map[string]string{
		"feat-42":     "feat-42",
		"feature/x":   "feature-x",
		"a/b/c":       "a-b-c",
		"  spaced  ":  "spaced",
		"weird::name": "weird-name",
		"../escape":   "escape",
		"..":          "task",
		"":            "task",
		"trailing/":   "trailing",
		"UPPER/Case":  "UPPER-Case",
		"dots...":     "dots",
		"-leading":    "leading",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSlugNeverProducesSomethingGitOrThePathWouldReject is the property that
// matters more than any single case above.
func TestSlugNeverProducesSomethingGitOrThePathWouldReject(t *testing.T) {
	for _, in := range []string{"..", ".", "/", "//", "...", "@{", "a\\b", "\n", "-", "@"} {
		got := Slug(in)
		if got == "" || got == "." || got == ".." {
			t.Fatalf("Slug(%q) = %q, which is not a usable directory name", in, got)
		}
		if strings.ContainsAny(got, `/\`) {
			t.Fatalf("Slug(%q) = %q, which is not one path segment", in, got)
		}
		if strings.HasPrefix(got, "-") || strings.HasPrefix(got, ".") {
			t.Fatalf("Slug(%q) = %q, which git would refuse as a branch", in, got)
		}
	}
}

// TestParseRemoveTakesTheWorktreePath — the remove payload is documented as
// carrying worktree_path and nothing else is guaranteed.
func TestParseRemoveTakesTheWorktreePath(t *testing.T) {
	got, err := ParseRemove(strings.NewReader(`{"worktree_path":"/tmp/c/trees/t1","extra":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.WorktreePath != "/tmp/c/trees/t1" {
		t.Fatalf("worktree_path %q", got.WorktreePath)
	}
	if _, err := ParseRemove(strings.NewReader(`{}`)); err == nil {
		t.Fatal("a remove with no path must be refused, not guessed at")
	}
}
