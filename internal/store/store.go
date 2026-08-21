package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/Venut-Labs/wkt/internal/gitx"
	"github.com/Venut-Labs/wkt/internal/paths"
	"github.com/Venut-Labs/wkt/internal/wkterr"
)

// ID is a collision-free function of the workspace-relative path — never the
// basename, so services/api and tools/api cannot collide (spec §5.2).
func ID(relPath, absPath string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, relPath)
	canon, _ := paths.Canonical(absPath)
	sum := sha256.Sum256([]byte(canon))
	return strings.Trim(slug, "-") + "-" + hex.EncodeToString(sum[:])[:8]
}

// Ensure performs the six steps of spec §5.2, in order. The base pin is written
// FIRST so no gc window exists before the store references the base (H11).
func Ensure(storeDir, repoAbs, relPath, taskName, baseSHA string) (string, error) {
	pin := "refs/wkt/base/" + taskName
	if _, err := gitx.Run(repoAbs, "update-ref", pin, baseSHA); err != nil {
		return "", wkterr.New("WKT_PIN_FAILED", "cannot pin the base commit in the workspace repository").
			WithRepo(relPath).WithPath(repoAbs).WithFound(err.Error())
	}

	sp := filepath.Join(storeDir, ID(relPath, repoAbs)+".git")
	if _, err := os.Stat(sp); err == nil {
		// A directory here is not evidence that the store inside it is
		// finished. A build interrupted after the clone — Ctrl-C is enough —
		// leaves one that looks complete and is not: it still borrows objects
		// from the developer's own repository, so a later gc or re-clone makes
		// every commit in the task unreadable, and its hooks are live.
		// Verify before adopting.
		if err := adoptable(sp, repoAbs); err != nil {
			return "", err
		}
		return sp, nil
	}

	if _, err := gitx.Run(storeDir, "clone", "--shared", "--bare", "-q", repoAbs, sp); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot mirror the repository").
			WithRepo(relPath).WithPath(sp).WithFound(err.Error())
	}
	// De-borrow: copy the objects in, then drop the alternates pointer, so the
	// store survives deletion or re-clone of the workspace repository (spec §5.2).
	if _, err := gitx.Run(sp, "repack", "-a", "-d", "-q"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot repack the store").WithRepo(relPath).WithPath(sp).WithFound(err.Error())
	}
	if err := os.Remove(filepath.Join(sp, "objects", "info", "alternates")); err != nil && !os.IsNotExist(err) {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot de-borrow the store").WithRepo(relPath).WithPath(sp).WithFound(err.Error())
	}

	origin, err := gitx.Run(repoAbs, "remote", "get-url", "origin")
	if err == nil && origin != "" {
		if _, err := gitx.Run(sp, "remote", "set-url", "origin", origin); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot point the store at the real origin").WithRepo(relPath).WithFound(err.Error())
		}
		// Bare clones set NO fetch refspec; without this refs/remotes/* never exist,
		// which silently breaks sync and the unpushed-commit guard (spec H15).
		if _, err := gitx.Run(sp, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot configure the origin refspec").WithRepo(relPath).WithFound(err.Error())
		}
	} else {
		// No origin on the workspace repository: drop the borrowed "origin" the
		// clone created rather than leave a URL-less remote with a refspec.
		_, _ = gitx.Run(sp, "remote", "remove", "origin")
	}
	// Second remote: the workspace repository, so a task can branch from work the
	// developer has committed locally and not pushed (spec §5.2).
	if _, err := gitx.Run(sp, "remote", "add", "workspace", repoAbs); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot add the workspace remote").WithRepo(relPath).WithFound(err.Error())
	}
	if _, err := gitx.Run(sp, "config", "remote.workspace.fetch", "+refs/heads/*:refs/remotes/ws/*"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot configure the workspace refspec").WithRepo(relPath).WithFound(err.Error())
	}
	for _, kv := range [][2]string{{"gc.auto", "0"}, {"core.hooksPath", "/dev/null"}} {
		if _, err := gitx.Run(sp, "config", kv[0], kv[1]); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot harden the store").WithRepo(relPath).WithFound(err.Error())
		}
	}
	// The marker is written by the code that verifies the invariants, never
	// by hand. Ordering by convention is how "stamp it last" quietly becomes
	// "stamp it first" in a later edit — and a marker that can be written
	// before the store is hardened is a marker that lies, permanently, since
	// nothing re-checks a marked store.
	if err := stamp(sp, repoAbs); err != nil {
		return "", err
	}
	return sp, nil
}

// stamp records that a store is finished, after checking that it is.
func stamp(sp, repoAbs string) error {
	if problems := invariants(sp, repoAbs); len(problems) > 0 {
		e := wkterr.New("WKT_STORE_CREATE", "the store is not in the state wkt requires; refusing to mark it complete").
			WithPath(sp)
		for _, p := range problems {
			e = e.WithProblem(wkterr.Problem{Code: "WKT_STORE_CREATE", Path: sp, Detail: p})
		}
		return e
	}
	if _, err := gitx.Run(sp, "config", markerKey, "1"); err != nil {
		return wkterr.New("WKT_STORE_CREATE", "cannot mark the store complete").
			WithPath(sp).WithFound(err.Error())
	}
	return nil
}

// invariants returns everything about a store that is not as spec §5.2
// requires, in language a person can act on.
func invariants(sp, repoAbs string) []string {
	var problems []string
	if _, err := os.Stat(filepath.Join(sp, "objects", "info", "alternates")); err == nil {
		problems = append(problems, "it still borrows objects from the workspace repository (objects/info/alternates present), so the task's commits would become unreadable if that repository is re-cloned or garbage-collected")
	}
	if v, err := gitx.Run(sp, "config", "--get", "gc.auto"); err != nil || strings.TrimSpace(v) != "0" {
		problems = append(problems, "gc.auto is not disabled, so git may collect objects a task still needs")
	}
	if v, err := gitx.Run(sp, "config", "--get", "core.hooksPath"); err != nil || strings.TrimSpace(v) == "" {
		problems = append(problems, "its hooks are live: anything in the store's hooks directory runs when a task commits")
	}
	if v, err := gitx.Run(sp, "config", "--get", "remote.workspace.url"); err != nil || strings.TrimSpace(v) == "" {
		problems = append(problems, "it has no workspace remote, so a task cannot branch from work committed locally and not pushed")
	}
	if v, err := gitx.Run(sp, "config", "--get", "remote.origin.url"); err == nil {
		if canon, cErr := paths.Canonical(strings.TrimSpace(v)); cErr == nil {
			if repoCanon, rErr := paths.Canonical(repoAbs); rErr == nil && canon == repoCanon {
				problems = append(problems, "its origin still points at the workspace repository instead of the real upstream")
			}
		}
	}
	return problems
}

// markerKey records that a store passed through every step of Ensure. git
// lowercases config key names, so it is written and read in the form git
// stores it.
const markerKey = "wkt.storecomplete"

// adoptable decides whether an existing store may be reused.
//
// It verifies spec §5.2's invariants rather than demanding the marker,
// because every store built before the marker existed is complete and has
// none — a rule of "unmarked means unusable" would condemn the whole
// installed base. A store that verifies is stamped, so the check happens once.
//
// It never deletes and never rebuilds. The store is the only copy of every
// task's unpushed commits, and it owns the worktree admin directories: a
// rebuild re-issues the registration names and aliases an existing task's tree
// onto a new one. Refusing loudly is the worst thing that may happen here.
func adoptable(sp, repoAbs string) error {
	if v, err := gitx.Run(sp, "config", "--get", markerKey); err == nil && strings.TrimSpace(v) != "" {
		return nil
	}

	problems := invariants(sp, repoAbs)
	if len(problems) == 0 {
		// Complete, merely unmarked: an earlier version built it. Verify once,
		// stamp, and never look again.
		_, _ = gitx.Run(sp, "config", markerKey, "1")
		return nil
	}

	e := wkterr.New("WKT_STORE_INCOMPLETE",
		"an unfinished store is already at that path; wkt will not adopt it and will not delete it").
		WithPath(sp)
	for _, p := range problems {
		e = e.WithProblem(wkterr.Problem{Code: "WKT_STORE_INCOMPLETE", Path: sp, Detail: p})
	}
	if out, err := gitx.Run(sp, "worktree", "list", "--porcelain"); err == nil && strings.Count(out, "worktree ") > 1 {
		e = e.WithRemedy("task trees are still attached to this store — inspect them before doing anything",
			"wkt status lists what this container holds")
	} else {
		e = e.WithRemedy("this store holds no attached trees; if it also holds no work you need, remove the directory yourself and retry",
			"wkt doctor --all reports what the container is holding")
	}
	return e
}

func FetchWorkspace(storePath string) error {
	if _, err := gitx.Run(storePath, "fetch", "-q", "workspace"); err != nil {
		return wkterr.New("WKT_FETCH_FAILED", "cannot fetch from the workspace repository").WithPath(storePath).WithFound(err.Error())
	}
	return nil
}

func HasObject(storePath, sha string) bool {
	return gitx.RunOK(storePath, "cat-file", "-e", sha)
}
