package artifact

import "testing"

// TestIsRegenerable pins the classifier the tree and the teardown share.
// Live-run finding L2: the list lived inside the teardown, so it applied to
// gitignored content inside a repository and nowhere else — and a .DS_Store
// at the tree root, which Finder writes the moment a macOS user opens the
// folder, blocked removal anyway.
func TestIsRegenerable(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{".DS_Store", true},
		{"nested/dir/.DS_Store", true},
		{"Thumbs.db", true},
		{"node_modules/left-pad/index.js", true},
		{"dist/", true},
		{"services/svc-a/dist/app.js", true},
		{"vendor/bundle/gems", true},
		// Claude Code writes this into a tree the moment the agent edits a
		// file; without it every hook-driven task needs --force to remove.
		{".claude/.cc-writes", true},
		{".claude/.cc-writes/2026-08-21.jsonl", true},
		// The rest of .claude is the user's: agents, instructions, settings
		// they wrote themselves.
		{".claude/agents/reviewer.md", false},
		{".claude/CLAUDE.md", false},
		{".env", false},
		{"server.key", false},
		{"src/main.go", false},
		{"notes.md", false},
		// A file that merely mentions a regenerable name is not one.
		{"docs/node_modules-explained.md", false},
		{"dist-plan.txt", false},
	} {
		if got := IsRegenerable(tc.path); got != tc.want {
			t.Errorf("IsRegenerable(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
