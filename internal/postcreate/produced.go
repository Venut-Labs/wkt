package postcreate

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/Venut-Labs/wkt/internal/gitx"
)

// Snapshot lists the ignored paths each materialised repository holds, in
// git's own reporting.
//
// It runs the same command teardown reads — "git status --porcelain
// --ignored=matching" — and that is the point rather than a convenience.
// Teardown classifies the "!! " lines that command produces; a snapshot taken
// any other way would speak a different vocabulary and the two sets would
// never line up. Measured: a wholly ignored directory collapses to a single
// line whatever it holds, so "config/" is reported and "config/local.yaml"
// never is — and so this stays cheap even after an install has put a hundred
// thousand files under node_modules.
//
// A repository that cannot be asked contributes nothing, which is the safe
// direction to be wrong in: an unrecorded path makes teardown refuse and list
// it, which is the behaviour that already exists.
func Snapshot(treeRoot string, repos []string) map[string]bool {
	out := map[string]bool{}
	for _, rel := range repos {
		dir := filepath.Join(treeRoot, filepath.FromSlash(rel))
		s, err := gitx.Run(dir, "status", "--porcelain", "--ignored=matching")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(s, "\n") {
			if !strings.HasPrefix(line, "!! ") {
				continue
			}
			out[rel+"/"+strings.TrimPrefix(line, "!! ")] = true
		}
	}
	return out
}

// NewSince returns what the script brought into being, sorted so that state
// written twice from the same tree is written the same way.
func NewSince(before, after map[string]bool) []string {
	var out []string
	for p := range after {
		if !before[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
