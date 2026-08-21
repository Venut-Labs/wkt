// Package perimeter renders the settings document wkt writes into a task
// tree. It is defence in depth against accidents, never a boundary (spec §0):
// what it buys is that an agent working in one task does not casually write
// into the workspace or into another task's tree.
//
// Two facts measured against Claude Code 2.1.238 shape everything here
// (docs/superpowers/specs/2026-08-21-hazard-reverification.md):
//
//   - An "Edit(...)" deny rule constrains the Bash tool too — the rules are
//     merged into the sandbox profile — so the paths are stated once, under
//     permissions.deny, and never restated under sandbox.filesystem.denyWrite.
//   - That profile is compiled into every command the session runs. It works
//     at ~5,000 paths; past roughly 9,000 the profile stops compiling and
//     *every* Bash command fails. So the list is capped, and exceeding the cap
//     is a refusal rather than a file that quietly breaks the tool.
package perimeter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"wkt/internal/container"
	"wkt/internal/paths"
	"wkt/internal/state"
	"wkt/internal/wkterr"
)

// MaxPaths caps the deny list well below the measured failure point. The
// failure past it is not degradation — the session's Bash stops working
// entirely — so the margin is deliberate.
const MaxPaths = 2000

type Document struct {
	// Marker says who wrote this file and for which task. Claude Code
	// ignores unknown top-level keys — verified on 2.1.238, where a
	// settings file carrying "$wkt" still enforced its deny rules — and it
	// makes ownership a property of the file rather than of wkt's state.
	// Without it, a task whose state lost its recorded hashes could never
	// have its perimeter regenerated: the command that exists to repair
	// that case would refuse, mistaking its own output for the user's file.
	Marker      Marker      `json:"$wkt"`
	Permissions Permissions `json:"permissions"`
	Sandbox     Sandbox     `json:"sandbox"`
}

type Marker struct {
	Version int    `json:"version"`
	Task    string `json:"task"`
	Note    string `json:"note"`
}

// MarkerVersion is the shape of the generated document, not the wkt release.
const MarkerVersion = 1

type Permissions struct {
	Deny []string `json:"deny"`
}

type Sandbox struct {
	Enabled    bool       `json:"enabled"`
	Filesystem Filesystem `json:"filesystem"`
}

type Filesystem struct {
	AllowWrite []string `json:"allowWrite,omitempty"`
	// DenyWrite stays empty by design: the deny paths already reach this
	// layer through the Edit rules. The field exists so a test can assert
	// that it is empty, and so a future release that stops merging can fill
	// it without a schema change.
	DenyWrite []string `json:"denyWrite,omitempty"`
	DenyRead  []string `json:"denyRead,omitempty"`
}

// spellingsOf returns every spelling of one path. Deny globs are lexical, so
// an alias such as ~/work -> /Volumes/Data/work defeats a single spelling
// entirely (spec §5.6).
func spellingsOf(p string) []string { return paths.Spellings(p) }

// rule renders one deny rule. The "//" prefix is what makes the path
// absolute: verified on 2.1.238 that "Edit(//Users/x/f)" and
// "Edit(///Users/x/f)" both deny, while "Edit(/Users/x/f)" is accepted and
// silently does nothing — the worst possible failure for a guard.
func rule(p string, recursive bool) string {
	if recursive {
		return "Edit(//" + p + "/**)"
	}
	return "Edit(//" + p + ")"
}

// For builds the document for one task, given the names of the other tasks in
// the container.
func For(c container.C, t state.Task, siblings []string) (Document, error) {
	tree := c.TreePath(t.Name)

	deny := map[string]bool{}
	addAll := func(p string, recursive bool) {
		for _, sp := range spellingsOf(p) {
			deny[rule(sp, recursive)] = true
		}
	}

	// The workspace itself: the whole point is that a task never writes there.
	addAll(c.Workspace, true)
	// wkt's own bookkeeping. State is authoritative; staging is where a
	// forced removal parks a tree mid-delete.
	addAll(filepath.Join(c.Root, "state"), true)
	addAll(c.StagingDir(), true)
	// Every other task's tree, named individually: H16 was re-confirmed on
	// 2.1.238, so a wide glob with a narrower allow for this task's own tree
	// does not work — deny wins.
	for _, s := range siblings {
		if s == t.Name {
			continue // never deny the tree this perimeter is for
		}
		addAll(filepath.Join(c.TreesDir(), s), true)
	}
	// The store is writable (see AllowWrite below) because the task's gitdir
	// lives there, so its two dangerous spots are closed explicitly. A
	// narrower deny still beats the broader allow.
	for _, r := range t.Repos {
		storePath := filepath.Join(c.StoreDir(), r.StoreID+".git")
		addAll(filepath.Join(storePath, "hooks"), true)
		addAll(filepath.Join(storePath, "config"), false)
	}
	// The perimeter protects itself (H13), and the task's state mirror.
	addAll(filepath.Join(tree, ".claude"), true)
	addAll(filepath.Join(tree, ".wkt"), true)
	for _, r := range t.Repos {
		addAll(filepath.Join(tree, r.RelPath, ".claude"), true)
	}

	if len(deny) > MaxPaths {
		return Document{}, wkterr.New("WKT_PERIMETER_TOO_LARGE",
			"the perimeter would carry more paths than a sandbox profile can hold").
			WithExpected(strconv.Itoa(MaxPaths)).
			WithFound(strconv.Itoa(len(deny))).
			WithRemedy("remove tasks from this container, or run wkt rm on the ones you have finished")
	}

	return Document{
		Marker: Marker{
			Version: MarkerVersion,
			Task:    t.Name,
			Note:    "generated by wkt; edits are overwritten on the next regeneration",
		},
		Permissions: Permissions{Deny: sorted(deny)},
		Sandbox: Sandbox{
			Enabled: true,
			Filesystem: Filesystem{
				AllowWrite: spellingsOf(c.StoreDir()),
				DenyRead:   credentialDirs(),
			},
		},
	}, nil
}

// credentialDirs are read-denied outright: a task has no business reading the
// developer's keys, cloud credentials, gh token or Claude Code configuration
// (which holds a live OAuth token).
func credentialDirs() []string {
	home, err := homeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range []string{".ssh", ".aws", ".config/gh", ".claude"} {
		out = append(out, filepath.Join(home, filepath.FromSlash(d)))
	}
	sort.Strings(out)
	return out
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Render produces the file's bytes. Key order is fixed by the struct and
// slice order by sorted(), so an unchanged task renders identically every
// time — anything else would read as drift to the hash check in status.
func Render(d Document) ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, wkterr.New("WKT_PERIMETER_RENDER", "cannot encode the perimeter").
			WithFound(err.Error())
	}
	return append(b, '\n'), nil
}

func homeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" && strings.HasPrefix(h, "/private/") {
		return strings.TrimPrefix(h, "/private"), nil
	}
	return h, nil
}
