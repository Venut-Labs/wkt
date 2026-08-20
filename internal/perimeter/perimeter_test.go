package perimeter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wkt/internal/container"
	"wkt/internal/state"
	"wkt/internal/wkterr"
)

func fixture(t *testing.T, siblings ...string) (container.C, state.Task, []string) {
	t.Helper()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := container.Locate(ws)
	if err != nil {
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
			WorktreePath: filepath.Join(c.TreePath("feat-42"), "services", "svc-a"),
		}},
	}
	return c, task, siblings
}

// TestEveryRuleUsesTheDoubleSlashPrefix is the most important test in this
// file. Verified against Claude Code 2.1.238: "Edit(//Users/x/f)" and
// "Edit(///Users/x/f)" both deny, while "Edit(/Users/x/f)" — one leading
// slash — is accepted by the settings parser and silently does nothing. A
// perimeter spelled that way looks present and protects nothing.
func TestEveryRuleUsesTheDoubleSlashPrefix(t *testing.T) {
	c, task, sib := fixture(t, "other-task")
	d, err := For(c, task, sib)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Permissions.Deny) == 0 {
		t.Fatal("a perimeter with no deny rules is not a perimeter")
	}
	for _, r := range d.Permissions.Deny {
		if !strings.HasPrefix(r, "Edit(//") {
			t.Fatalf("rule must start with Edit(// — a single slash is a silent no-op: %q", r)
		}
		if strings.HasPrefix(r, "Edit(///") && !strings.HasPrefix(r, "Edit(////") {
			continue // "//" + an absolute path is the documented spelling
		}
	}
}

// TestDenyCoversEveryRequiredRegion pins spec §5.6's list. Asserting the
// section as well as the path matters: naming the workspace under sandbox
// instead of permissions would pass a looser test and protect nothing from
// the file-editing tools.
func TestDenyCoversEveryRequiredRegion(t *testing.T) {
	c, task, sib := fixture(t, "other-task")
	d, err := For(c, task, sib)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(d.Permissions.Deny, "\n")
	for _, want := range []string{
		c.Workspace,
		filepath.Join(c.Root, "state"),
		filepath.Join(c.Root, "staging"),
		filepath.Join(c.TreesDir(), "other-task"),
		filepath.Join(c.TreePath("feat-42"), ".claude"),
		filepath.Join(c.TreePath("feat-42"), ".wkt"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("deny list must cover %s:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "hooks") || !strings.Contains(joined, "config") {
		t.Errorf("the store's hooks/ and config must be denied:\n%s", joined)
	}
}

// TestOwnTreeIsNotDenied guards the mistake H16 exists to describe: deny wins
// over a narrower allow, so a task that denies its own tree cannot carve an
// exception back out — it simply cannot work in it.
func TestOwnTreeIsNotDenied(t *testing.T) {
	// The container lists every task, this one included — state.List does not
	// filter — so the guard is only exercised when the task's own name is in
	// the slice. A fixture that omits it tests nothing.
	c, task, sib := fixture(t, "other-task", "feat-42")
	d, err := For(c, task, sib)
	if err != nil {
		t.Fatal(err)
	}
	own := c.TreePath("feat-42")
	for _, r := range d.Permissions.Deny {
		inner := strings.TrimSuffix(strings.TrimPrefix(r, "Edit(//"), ")")
		inner = strings.TrimSuffix(inner, "/**")
		if inner == own || inner == "/"+strings.TrimPrefix(own, "/") {
			t.Fatalf("a task must not deny its own tree: %q", r)
		}
	}
}

// TestSpellingsAreAllPresent: deny globs are lexical, so an aliased workspace
// defeats a single spelling entirely (spec §5.6).
func TestSpellingsAreAllPresent(t *testing.T) {
	c, task, sib := fixture(t)
	d, err := For(c, task, sib)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(d.Permissions.Deny, "\n")
	for _, sp := range spellingsOf(c.Workspace) {
		if !strings.Contains(joined, sp) {
			t.Errorf("workspace spelling %q missing from the deny list", sp)
		}
	}
}

// TestGrowthIsProportionalToSiblings — the profile is compiled into every Bash
// command, so the shape of this growth is a cost model, not a detail.
func TestGrowthIsProportionalToSiblings(t *testing.T) {
	c, task, _ := fixture(t)
	one, err := For(c, task, []string{"s1"})
	if err != nil {
		t.Fatal(err)
	}
	forty := make([]string, 40)
	for i := range forty {
		forty[i] = "s" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	many, err := For(c, task, forty)
	if err != nil {
		t.Fatal(err)
	}
	perSibling := len(spellingsOf(c.TreesDir()))
	grew := len(many.Permissions.Deny) - len(one.Permissions.Deny)
	if want := 39 * perSibling; grew != want {
		t.Fatalf("39 more siblings added %d rules, want %d (%d spellings each)", grew, want, perSibling)
	}
}

// TestRefusesAnUnboundedList — measured on 2.1.238: past roughly 9,000 paths
// the sandbox profile stops compiling and *every* Bash command in the session
// fails. Refusing loudly beats generating a file that breaks the tool.
func TestRefusesAnUnboundedList(t *testing.T) {
	c, task, _ := fixture(t)
	huge := make([]string, 5000)
	for i := range huge {
		huge[i] = "task-" + strings.Repeat("x", 3) + string(rune(i))
	}
	_, err := For(c, task, huge)
	if err == nil {
		t.Fatal("a perimeter that would break every Bash command must be refused")
	}
	var e *wkterr.E
	if !errors.As(err, &e) || e.Code != "WKT_PERIMETER_TOO_LARGE" {
		t.Fatalf("want WKT_PERIMETER_TOO_LARGE, got %v", err)
	}
}

// TestRenderIsDeterministic — an unstable order makes every regeneration look
// like drift to the hash check that reports coverage.
func TestRenderIsDeterministic(t *testing.T) {
	c, task, _ := fixture(t)
	a, err := For(c, task, []string{"b-task", "a-task", "c-task"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := For(c, task, []string{"c-task", "a-task", "b-task"})
	if err != nil {
		t.Fatal(err)
	}
	ra, err := Render(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := Render(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ra) != string(rb) {
		t.Fatal("the same task must render byte-identically whatever the sibling order")
	}
	if !json.Valid(ra) {
		t.Fatal("the rendered perimeter must be valid JSON")
	}
}

// TestStoreStaysWritable — the task's gitdir lives in the store, and denying
// it breaks git add (H5). This is the one mandatory allow.
func TestStoreStaysWritable(t *testing.T) {
	c, task, _ := fixture(t)
	d, err := For(c, task, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Sandbox.Enabled {
		t.Fatal("the sandbox must be switched on")
	}
	joined := strings.Join(d.Sandbox.Filesystem.AllowWrite, "\n")
	if !strings.Contains(joined, c.StoreDir()) {
		t.Fatalf("the store must be writable, got %v", d.Sandbox.Filesystem.AllowWrite)
	}
	for _, want := range []string{".ssh", ".aws", ".claude"} {
		if !strings.Contains(strings.Join(d.Sandbox.Filesystem.DenyRead, "\n"), want) {
			t.Errorf("credential directory %s must be unreadable", want)
		}
	}
}

// TestDenyPathsAreNotDuplicatedUnderSandbox — Task 1 verified that Edit(...)
// deny rules are merged into the Bash profile already. Restating them doubles
// the profile every command carries, for nothing.
func TestDenyPathsAreNotDuplicatedUnderSandbox(t *testing.T) {
	c, task, _ := fixture(t)
	d, err := For(c, task, []string{"other"})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sandbox.Filesystem.DenyWrite) != 0 {
		t.Fatalf("deny paths are merged from the Edit rules; restating them is waste: %v",
			d.Sandbox.Filesystem.DenyWrite)
	}
}
