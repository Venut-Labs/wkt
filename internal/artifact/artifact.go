// Package artifact classifies paths that a build or the operating system
// recreates on its own. Both halves of wkt need the same answer: the tree
// must not copy such a file into a task, and the teardown must not refuse
// on one — a classifier that lives in only one of them leaves the other
// treating a .DS_Store as work at risk.
package artifact

import "strings"

var regenerable = [][]string{
	{"node_modules"}, {"dist"}, {"build"}, {"target"},
	{".venv"}, {"venv"}, {".next"}, {".nuxt"},
	{"__pycache__"}, {".pytest_cache"}, {"coverage"},
	{".gradle"}, {".tox"}, {"vendor", "bundle"}, {".terraform"},
	// Operating-system artifacts: recreated automatically by the OS/file
	// manager and never carry any work of their own. Without these, a
	// gitignored ".DS_Store" (which Finder writes into essentially every
	// directory a macOS user has so much as opened, and which nearly every
	// macOS repository gitignores) would make "wkt rm" refuse on almost
	// every real tree on the primary development platform — teaching
	// people to reach for --force without reading the list, which defeats
	// the reason this check exists at all, including for the "server.key"
	// case it was just fixed to catch.
	{".DS_Store"}, {"Thumbs.db"}, {".Spotlight-V100"}, {".fseventsd"},
	{".Trashes"}, {"desktop.ini"},
	// Claude Code's own bookkeeping inside a task tree. It appears the
	// first time the agent writes a file, so without this entry every task
	// driven through the worktree hooks needs --force to remove — verified
	// end to end against 2.1.238. Only this one path: the rest of .claude/
	// may hold the user's agents and instructions, and those are work.
	{".claude", ".cc-writes"},
}

// IsRegenerable reports whether a workspace-relative path is build output or
// an operating-system artifact.
func IsRegenerable(relPath string) bool {
	comps := strings.Split(strings.TrimSuffix(relPath, "/"), "/")
	for _, seq := range regenerable {
		if containsComponentSequence(comps, seq) {
			return true
		}
	}
	return false
}

func containsComponentSequence(comps, seq []string) bool {
	if len(seq) > len(comps) {
		return false
	}
	for i := 0; i+len(seq) <= len(comps); i++ {
		match := true
		for j, s := range seq {
			if comps[i+j] != s {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
