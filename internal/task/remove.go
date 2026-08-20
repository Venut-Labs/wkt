// Refuse-only teardown: Remove never deletes anything until Preflight finds
// no blocker (or the caller passes --force for the ordinary ones), and even
// then a foreign repository or a submodule is never removable. Anything that
// deletes enumerates the filesystem, never the state file — state says what
// should be there, the disk says what is there.
package task

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"wkt/internal/container"
	"wkt/internal/gitx"
	"wkt/internal/paths"
	"wkt/internal/state"
	"wkt/internal/tree"
	"wkt/internal/wkterr"
)

// Severity distinguishes a blocker that gates removal from one that is
// merely reported. The zero value ("") means blocking, so every existing
// code that never sets Severity keeps its original meaning unchanged.
type Blocker struct {
	Code     string
	Repo     string
	Path     string
	Detail   string
	Severity string // "" (blocking, default) or "info" (listed, not blocking)
}

// regenerable lists path-component sequences that mark git-ignored content
// as safe to delete without a warning: build output and dependency caches
// any build system regenerates on demand. This is deliberately an
// allowlist, not a denylist. A denylist of "precious" filename substrings
// was tried first and failed the one property that matters here: it cannot
// be made complete. A gitignored "server.key" — an entirely ordinary TLS or
// SSH private key name — matched none of its five entries and was deleted
// with zero blockers and no --force. Now unknown ignored content blocks by
// default; only what's provably regenerable is exempt, and even that is
// still reported, not silently passed over (see the "info" Severity below).
var regenerable = [][]string{
	{"node_modules"}, {"dist"}, {"build"}, {"target"},
	{".venv"}, {"venv"}, {".next"}, {".nuxt"},
	{"__pycache__"}, {".pytest_cache"}, {"coverage"},
	{".gradle"}, {".tox"}, {"vendor", "bundle"}, {".terraform"},
}

// isRegenerable reports whether relPath — git's own slash-separated,
// repo-relative reporting of an ignored path — contains one of the
// regenerable sequences as whole path components. It never matches a bare
// substring: "target" matches ".../target/..." but not
// ".../my-target-cache/...", and a name like "server.key" never matches
// anything here at all, which is the point.
func isRegenerable(relPath string) bool {
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

// Preflight enumerates every reason removing t's tree would lose work. It
// walks the real filesystem under the tree root — never the state file — so
// content a task's state knows nothing about (a foreign repository dropped
// into a worktree, a link slot an agent turned into a real file) is still
// found. It never descends into a symlink: those are workspace back-fill
// slots, not part of the tree wkt owns.
func Preflight(c container.C, t state.Task) ([]Blocker, error) {
	var out []Blocker
	treeRoot := c.TreePath(t.Name)

	for _, r := range t.Repos {
		wt := r.WorktreePath
		if _, err := os.Stat(wt); os.IsNotExist(err) {
			out = append(out, Blocker{Code: "WKT_WORKTREE_MISSING", Repo: r.RelPath, Path: wt})
			continue
		}
		// 1+2: uncommitted and untracked.
		if s, err := gitx.Run(wt, "status", "--porcelain"); err != nil {
			out = append(out, Blocker{Code: "WKT_CHECK_FAILED", Repo: r.RelPath, Path: wt})
		} else if s != "" {
			out = append(out, Blocker{Code: "WKT_DIRTY", Repo: r.RelPath, Path: wt, Detail: firstLine(s)})
		}
		// 3: ignored content. git's own refusal never fires on any of it (H1).
		// A bulk-ignored directory collapses to one "!! dir/" line — its
		// contents are never listed individually — so an allowlisted
		// directory is trusted whole, and an unrecognised one blocks whole:
		// unknown means precious, not just unknown-named files.
		if s, err := gitx.Run(wt, "status", "--porcelain", "--ignored=matching"); err == nil {
			for _, line := range strings.Split(s, "\n") {
				if !strings.HasPrefix(line, "!! ") {
					continue
				}
				rel := strings.TrimPrefix(line, "!! ")
				if isRegenerable(rel) {
					out = append(out, Blocker{Code: "WKT_REGENERABLE_IGNORED", Repo: r.RelPath, Path: rel, Severity: "info"})
					continue
				}
				out = append(out, Blocker{Code: "WKT_PRECIOUS_IGNORED", Repo: r.RelPath, Path: rel})
			}
		}
		// 4: in-progress operations — invisible to status --porcelain (H2):
		// empty during an interactive rebase pause, a bisect, or a detached
		// HEAD, so removal must not be gated on status alone.
		for _, marker := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"} {
			p, err := gitx.Run(wt, "rev-parse", "--git-path", marker)
			if err != nil {
				continue
			}
			// --git-path may answer relatively or absolutely; --path-format needs
			// git 2.31 and our floor is 2.29, so normalise by hand.
			if !filepath.IsAbs(p) {
				p = filepath.Join(wt, p)
			}
			if _, statErr := os.Stat(p); statErr == nil {
				out = append(out, Blocker{Code: "WKT_IN_PROGRESS", Repo: r.RelPath, Detail: marker})
			}
		}
		// 5: unpushed commits, including the no-upstream and detached cases. The
		// recorded base is excluded as well as every remote ref: a bare store has
		// no refs/remotes/* until something fetches, so counting against remotes
		// alone would flag the whole history of a freshly created task.
		args := []string{"rev-list", "--count", "HEAD", "--not", "--remotes"}
		if r.BaseSHA != "" {
			args = append(args, r.BaseSHA)
		}
		if n, err := gitx.Run(wt, args...); err == nil && n != "" && n != "0" {
			out = append(out, Blocker{Code: "WKT_UNPUSHED", Repo: r.RelPath, Detail: n + " commit(s)"})
		}
		// 6: submodules — worktree remove refuses unconditionally, and --force
		// destroys their object store (spec §5.7). Run from the worktree, not the
		// store, since submodule state is per-worktree (index-based) and this
		// fires even when the addition is fully committed and status is clean.
		if sm, err := gitx.Run(wt, "submodule", "status", "--recursive"); err == nil && strings.TrimSpace(sm) != "" {
			out = append(out, Blocker{Code: "WKT_SUBMODULE", Repo: r.RelPath, Detail: firstLine(sm)})
		}
	}

	// 7: a foreign .git anywhere in the tree, found without following symlinks.
	known := map[string]bool{}
	for _, r := range t.Repos {
		canon, _ := paths.Canonical(r.WorktreePath)
		known[canon] = true
	}
	_ = filepath.WalkDir(treeRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// fs.WalkDir's SkipDir has two different meanings depending on the
		// entry it's returned for: on a directory it means "don't descend
		// into this", but on anything else it means "skip the rest of this
		// directory's *siblings*" — a completely different, much more
		// destructive effect. A symlink is never a directory as far as
		// DirEntry.IsDir() is concerned (Lstat, not Stat), and WalkDir never
		// follows symlinks on its own regardless, so SkipDir here bought
		// nothing and only tripped the sibling-skipping behaviour: whenever a
		// back-fill symlink (e.g. "docs") sorted before other tree content
		// (e.g. "services"), it silently truncated the scan of the entire
		// rest of the tree root, hiding every materialised worktree from the
		// foreign-repo check below. Confirmed against real fs.WalkDir before
		// this fix. plain "return nil" is correct and sufficient: the walk
		// already never recurses into a symlink.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.Name() != ".git" {
			return nil
		}
		if !d.IsDir() {
			// A regular-file .git marker is either our own materialised
			// worktree's marker (excluded below via known) or a submodule
			// checkout's marker, whose gitdir always points into the owning
			// repository's own .git/modules/<name> (git's own convention).
			// A submodule is not "a repository wkt did not create" — it is
			// nested inside one wkt did — and it already gets a precise,
			// correctly-worded blocker (WKT_SUBMODULE, with the right
			// remedy) from the per-repo check above. Flagging it here too as
			// WKT_FOREIGN_REPO would mislead a user with the wrong remedy
			// ("move it out of the tree") and, worse, made the submodule
			// hard-force-block in Remove() untestable in isolation: the
			// foreign-repo hard block always fired first regardless of
			// whether the submodule-specific guard was even present.
			// Confirmed by deliberately disabling the submodule guard and
			// finding TestRemoveRefusesOnSubmoduleEvenWithForce still passed
			// before this fix.
			if b, rerr := os.ReadFile(p); rerr == nil &&
				strings.Contains(string(b), string(filepath.Separator)+"modules"+string(filepath.Separator)) {
				return nil
			}
		}
		owner, _ := paths.Canonical(filepath.Dir(p))
		if !known[owner] {
			out = append(out, Blocker{Code: "WKT_FOREIGN_REPO", Path: filepath.Dir(p)})
		}
		// The same asymmetry applies here: a linked worktree's own ".git"
		// marker is a regular *file* (every materialised worktree has
		// exactly this), so SkipDir returned unconditionally on it also hit
		// the sibling-skipping case and stopped scanning the remainder of
		// the worktree right after its own marker was visited — a foreign
		// repository nested anywhere sorting after ".git" (almost anything)
		// was never found. Only an actual .git *directory* (an ordinary
		// nested repository, foreign or otherwise) should stop descent.
		// Confirmed against real fs.WalkDir before this fix; see
		// remove_test.go's
		// TestRemoveDetectsForeignRepoNestedInsideMaterialisedWorktree.
		if d.IsDir() {
			return fs.SkipDir
		}
		return nil
	})

	// 8: link slots whose type changed, and copies that diverged (H12, §5.3 r5).
	for _, slot := range t.Links {
		p := filepath.Join(treeRoot, slot.RelPath)
		info, err := os.Lstat(p)
		if err != nil {
			out = append(out, Blocker{Code: "WKT_LINK_SLOT_MISSING", Path: slot.RelPath})
			continue
		}
		switch slot.Type {
		case "symlink":
			if info.Mode()&os.ModeSymlink == 0 {
				out = append(out, Blocker{Code: "WKT_LINK_SLOT_CHANGED", Path: slot.RelPath,
					Detail: "the link was replaced by a regular file"})
			}
		case "copy":
			if sum, err := tree.Hash(p); err != nil || sum != slot.Hash {
				out = append(out, Blocker{Code: "WKT_COPY_DIVERGED", Path: slot.RelPath})
			}
		}
	}
	return out, nil
}

func firstLine(s string) string { return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0] }

// Remove refuses while any blocker exists. With force it overrides the
// ordinary ones, but never a foreign repository (its history exists nowhere
// else) and never a submodule (its object store lives under the doomed
// worktree; --force would destroy objects reachable from neither the store
// nor the original submodule repository) — spec §5.7.
//
// The fence: one os.Rename moves the whole tree into staging/ first, making
// a still-running agent's cwd vanish atomically, before anything is deleted.
// Deletion goes through os.RemoveAll on a path this function computed —
// never a shell command, never a symlink-following walker (spec H3): "rm -rf
// link/" with a trailing slash destroys the symlink's target, not the link.
func Remove(c container.C, name string, force bool) error {
	t, err := state.Load(c.StateDir(), name)
	if err != nil {
		return err
	}
	all, err := Preflight(c, t)
	if err != nil {
		return err
	}
	// Severity "info" entries (regenerable ignored content) are reported but
	// never gate removal or the force decision — only the blocking ones do.
	var blocking []Blocker
	for _, b := range all {
		if b.Severity != "info" {
			blocking = append(blocking, b)
		}
	}
	for _, b := range blocking {
		if b.Code == "WKT_FOREIGN_REPO" {
			return wkterr.New(b.Code, "a repository wkt did not create lives inside the tree").
				WithPath(b.Path).WithRemedy("move it out of the tree, then retry")
		}
		if b.Code == "WKT_SUBMODULE" && force {
			return wkterr.New(b.Code, "a submodule is present; --force would destroy its objects").
				WithRepo(b.Repo).WithRemedy("push the submodule's commits", "git submodule deinit", "then retry")
		}
	}
	if len(blocking) > 0 && !force {
		e := wkterr.New("WKT_WOULD_LOSE_WORK", "removal would lose work")
		// List every entry, blocking and informational alike, so --force
		// doesn't become reflexive: the user sees that most of what's in the
		// way is build output, and one line is their .env (spec §5.7).
		for _, b := range all {
			line := b.Code
			if b.Severity == "info" {
				line = "(not blocking) " + line
			}
			e = e.WithRemedy(line + " " + b.Repo + " " + b.Path + " " + b.Detail)
		}
		return e
	}

	treeRoot := c.TreePath(name)
	staged := filepath.Join(c.StagingDir(), name)
	if err := os.MkdirAll(c.StagingDir(), 0o700); err != nil {
		return wkterr.New("WKT_STAGING", "cannot create the staging directory").WithPath(c.StagingDir())
	}
	if err := os.Rename(treeRoot, staged); err != nil {
		// Degrading the fence to a per-repo, non-atomic sequence when
		// staging/ is on another filesystem would defeat the reason the
		// fence exists (a still-running agent's cwd must vanish atomically),
		// so this refuses rather than falling back. Name the cause when it's
		// the well-known one (EXDEV) instead of only echoing the raw OS
		// error, so the remedy is actionable on first read.
		msg := "cannot move the tree into staging"
		remedy := "relocate the container so its trees/ and staging/ share one filesystem, then retry"
		if errors.Is(err, syscall.EXDEV) {
			msg = "the tree and staging/ are on different filesystems, so the removal fence cannot be atomic"
		}
		return wkterr.New("WKT_STAGING", msg).
			WithPath(treeRoot).WithFound(err.Error()).
			WithRemedy(remedy)
	}

	for _, r := range t.Repos {
		sp := filepath.Join(c.StoreDir(), r.StoreID+".git")
		_, _ = gitx.Run(sp, "worktree", "unlock", r.WorktreePath)
		_, _ = gitx.Run(sp, "worktree", "prune")
		// The branch a task's worktree was created on (spec §5.4) is never
		// removed by "worktree prune" — confirmed against real git: after
		// unlock+prune the admin entry is gone but "branch --list" still shows
		// it. Left behind, it makes the store un-reusable: a later Create of a
		// task with the same name fails Validate's WKT_BRANCH_EXISTS check
		// against the store even though the task was cleanly removed. See
		// remove_test.go's TestRemoveCleansUpStoreBranchSoTaskNameCanBeReused.
		_, _ = gitx.Run(sp, "branch", "-D", r.Branch)
		_, _ = gitx.Run(r.AbsPath, "update-ref", "-d", r.BasePinRef)
	}
	if err := os.RemoveAll(staged); err != nil {
		return wkterr.New("WKT_REMOVE_FAILED", "cannot remove the staged tree").WithPath(staged)
	}
	if err := os.Remove(filepath.Join(c.StateDir(), name+".json")); err != nil && !os.IsNotExist(err) {
		return wkterr.New("WKT_STATE_WRITE", "cannot remove task state").WithPath(name)
	}
	return nil
}
