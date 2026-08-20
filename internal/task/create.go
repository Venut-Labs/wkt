// Package task implements the two-phase creation of a wkt task: phase one
// (Validate) resolves and checks every selected repository before anything
// is touched, phase two (Create) builds the tree, store worktrees and
// branches, rolling back everything it created if any step fails.
package task

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"wkt/internal/container"
	"wkt/internal/discover"
	"wkt/internal/gitx"
	"wkt/internal/paths"
	"wkt/internal/state"
	"wkt/internal/store"
	"wkt/internal/tree"
	"wkt/internal/wkterr"
)

// Resolution pairs a resolved repository with the problems found for it.
// (Reserved for a future batch-diagnostics entry point; Validate today
// returns on the first problem it finds.)
type Resolution struct {
	Repo     state.Repo
	Problems []*wkterr.E
}

func resolveBase(repoAbs string) (sha, ref string, err error) {
	if out, e := gitx.Run(repoAbs, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); e == nil && out != "" {
		if s, e2 := gitx.Run(repoAbs, "rev-parse", out); e2 == nil {
			return s, out, nil
		}
	}
	if def, e := gitx.Run(repoAbs, "config", "--get", "init.defaultBranch"); e == nil && def != "" {
		if s, e2 := gitx.Run(repoAbs, "rev-parse", "--verify", "refs/heads/"+def); e2 == nil {
			return s, "refs/heads/" + def, nil
		}
	}
	s, e := gitx.Run(repoAbs, "rev-parse", "HEAD")
	if e != nil {
		return "", "", wkterr.New("WKT_NO_BASE", "cannot resolve a base commit").WithPath(repoAbs)
	}
	return s, "HEAD", nil
}

// Validate is phase one: it resolves the base and checks branch existence
// (in the workspace repository and, if already present, in the store),
// ancestry, case-fold collisions and D/F ref conflicts for every selected
// repository before phase two touches anything.
func Validate(c container.C, entries []discover.Entry, name string, selected []string) ([]state.Repo, error) {
	if !gitx.RunOK(".", "check-ref-format", "--branch", name) {
		return nil, wkterr.New("WKT_BAD_TASK_NAME", "not a valid branch name").WithFound(name)
	}
	byRel := map[string]discover.Entry{}
	for _, e := range entries {
		byRel[e.RelPath] = e
	}
	var repos []state.Repo
	for _, rel := range selected {
		e, ok := byRel[rel]
		if !ok || e.Kind != discover.KindRepo {
			return nil, wkterr.New("WKT_NO_SUCH_REPO", "not a discovered repository").WithRepo(rel)
		}
		// A branch of that name must not already exist, locally or on the remote.
		if gitx.RunOK(e.AbsPath, "rev-parse", "--verify", "refs/heads/"+name) {
			return nil, wkterr.New("WKT_BRANCH_EXISTS", "a branch of that name already exists").
				WithRepo(rel).WithRemedy("choose another task name", "or delete the branch")
		}
		// Task branches actually live in the store (since Task 6). A store may
		// already exist for this repository — from a task whose state was lost
		// but whose store survived — carrying the branch even though the
		// workspace repository does not. Check there too, when a store
		// directory already exists; a store with no directory yet simply
		// skips this half.
		storePath := filepath.Join(c.StoreDir(), store.ID(rel, e.AbsPath)+".git")
		if _, statErr := os.Stat(storePath); statErr == nil {
			if gitx.RunOK(storePath, "rev-parse", "--verify", "refs/heads/"+name) {
				return nil, wkterr.New("WKT_BRANCH_EXISTS", "a branch of that name already exists").
					WithRepo(rel).WithRemedy("choose another task name", "or delete the branch")
			}
		}
		// Case-fold collision, checked on every platform so macOS and Linux agree.
		if out, err := gitx.Run(e.AbsPath, "for-each-ref", "--format=%(refname:short)", "refs/heads/"); err == nil {
			for _, b := range strings.Split(out, "\n") {
				if b != "" && strings.EqualFold(b, name) {
					return nil, wkterr.New("WKT_BRANCH_CASE_COLLISION", "a branch differing only in case exists").
						WithRepo(rel).WithFound(b)
				}
			}
		}
		// D/F conflict: refs/heads/feat and refs/heads/feat/42 cannot coexist.
		if out, err := gitx.Run(e.AbsPath, "for-each-ref", "--format=%(refname:short)", "refs/heads/"); err == nil {
			for _, b := range strings.Split(out, "\n") {
				if b == "" {
					continue
				}
				if strings.HasPrefix(b, name+"/") || strings.HasPrefix(name, b+"/") {
					return nil, wkterr.New("WKT_BRANCH_DF_CONFLICT", "a branch name conflicts hierarchically").
						WithRepo(rel).WithFound(b)
				}
			}
		}
		sha, ref, err := resolveBase(e.AbsPath)
		if err != nil {
			return nil, err
		}
		repos = append(repos, state.Repo{
			RelPath: rel, AbsPath: e.AbsPath, StoreID: store.ID(rel, e.AbsPath),
			Branch: name, BaseSHA: sha, BaseRef: ref,
			WorktreePath: filepath.Join(c.TreePath(name), rel),
			BasePinRef:   "refs/wkt/base/" + name,
		})
	}
	if len(repos) == 0 {
		return nil, wkterr.New("WKT_EMPTY_TASK", "no repositories selected")
	}
	return repos, nil
}

type undo func()

// Create is phase two: it builds the store worktrees, branches and the
// mirrored tree for every resolved repository, and rolls back everything it
// created — in reverse order — the moment any step fails.
func Create(c container.C, entries []discover.Entry, name string, selected []string) (state.Task, error) {
	if _, err := state.Load(c.StateDir(), name); err == nil {
		return state.Task{}, wkterr.New("WKT_TASK_EXISTS", "task already exists").
			WithFound(name).WithRemedy("wkt path "+name, "wkt rm "+name)
	}

	treeRoot := c.TreePath(name)
	// os.MkdirAll succeeds silently on a directory that already exists, and
	// the rollback undo below then os.RemoveAll's the whole thing on any
	// later failure — destroying content wkt never created if that
	// directory was already there for an unrelated reason. Refusing up
	// front is simpler than tracking whether MkdirAll itself created the
	// directory, and it also stops a stale leftover tree from being
	// silently adopted.
	if _, err := os.Stat(treeRoot); err == nil {
		return state.Task{}, wkterr.New("WKT_TREE_EXISTS", "the task tree directory already exists").
			WithPath(treeRoot).WithRemedy("wkt status "+name, "wkt rm "+name)
	} else if !os.IsNotExist(err) {
		return state.Task{}, wkterr.New("WKT_CHECK_FAILED", "cannot check the task tree directory").WithPath(treeRoot)
	}

	repos, err := Validate(c, entries, name, selected)
	if err != nil {
		return state.Task{}, err
	}

	var undos []undo
	rollback := func() {
		for i := len(undos) - 1; i >= 0; i-- {
			undos[i]()
		}
	}

	if err := os.MkdirAll(treeRoot, 0o755); err != nil {
		return state.Task{}, wkterr.New("WKT_TREE_BUILD", "cannot create the task tree").WithPath(treeRoot)
	}
	undos = append(undos, func() { _ = os.RemoveAll(treeRoot) })

	for i := range repos {
		r := &repos[i]
		// store.Ensure writes the base pin into the workspace repository as its
		// unconditional first internal action — even on the idempotent
		// store-already-exists path, and even if it then fails later (e.g.
		// cloning into the store). The undo must therefore be registered
		// before calling it, not after it returns: deleting a ref that was
		// never written is a no-op in git, so registering early is safe, and
		// the undo only ever runs on rollback.
		repoAbs, pin := r.AbsPath, r.BasePinRef
		undos = append(undos, func() { _, _ = gitx.Run(repoAbs, "update-ref", "-d", pin) })

		sp, err := store.Ensure(c.StoreDir(), r.AbsPath, r.RelPath, name, r.BaseSHA)
		if err != nil {
			rollback()
			return state.Task{}, err
		}
		if !store.HasObject(sp, r.BaseSHA) {
			if err := store.FetchWorkspace(sp); err != nil {
				rollback()
				return state.Task{}, err
			}
		}

		if err := os.MkdirAll(filepath.Dir(r.WorktreePath), 0o755); err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_TREE_BUILD", "cannot create an ancestor directory").
				WithPath(r.WorktreePath)
		}
		storePath, wtPath := sp, r.WorktreePath
		// "worktree add -b" creates the branch before it checks anything
		// out, and can still fail on the checkout itself (a non-empty
		// destination, an unwritable one, content in the base commit the
		// filesystem refuses) — so the branch-delete undo must be
		// registered before the call, exactly like the pin undo above, or
		// a failed worktree add leaks a branch and the task name becomes
		// permanently unusable (WKT_BRANCH_EXISTS on every later attempt).
		undos = append(undos, func() { _, _ = gitx.Run(storePath, "branch", "-D", name) })
		if _, err := gitx.Run(sp, "worktree", "add", "-q", "-b", name, r.WorktreePath, r.BaseSHA); err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_WORKTREE_ADD", "cannot create the worktree").
				WithRepo(r.RelPath).WithPath(r.WorktreePath)
		}
		undos = append(undos, func() {
			// Force twice: a single --force removes a dirty worktree but still
			// refuses one that is locked, and by the time rollback runs, the
			// worktree lock below has usually already been taken. Registered
			// after the branch-delete undo above, so rollback (LIFO) removes
			// the worktree registration before it tries to delete the branch
			// it was checked out on — git refuses to delete a branch that is
			// still checked out anywhere.
			_, _ = gitx.Run(storePath, "worktree", "remove", "--force", "--force", wtPath)
		})
		if _, err := gitx.Run(sp, "worktree", "lock", "--reason", "held by wkt task "+name, r.WorktreePath); err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_WORKTREE_LOCK", "cannot lock the worktree").WithRepo(r.RelPath)
		}
		wtName, err := worktreeName(sp, r.WorktreePath)
		if err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_WORKTREE_NAME_UNRESOLVED",
				"cannot determine the store's worktree registration name; repair cannot work without it").
				WithRepo(r.RelPath).WithPath(r.WorktreePath)
		}
		r.StoreWorktreeName = wtName
	}

	plan, err := tree.PlanFor(c.Workspace, entries, selected)
	if err != nil {
		rollback()
		return state.Task{}, err
	}
	slots, err := tree.Materialise(treeRoot, c.Workspace, plan)
	if err != nil {
		rollback()
		return state.Task{}, err
	}

	t := state.Task{
		SchemaVersion: state.SchemaVersion, Name: name,
		Container: c.Root, Workspace: c.Workspace,
		WorkspaceSpellings: paths.Spellings(c.Workspace),
		BaseEpoch:          time.Now().UTC(),
		Repos:              repos, Links: slots,
	}
	if err := state.Save(c.StateDir(), t); err != nil {
		rollback()
		return state.Task{}, err
	}
	return t, nil
}

// worktreeName reads back the admin directory git chose, which it derives from
// the leaf basename and silently disambiguates (svc-a, svc-a1). repair cannot
// work without it (spec §5.4), so an unresolved name is an error, not a
// silently empty string persisted into state.
func worktreeName(storePath, worktreePath string) (string, error) {
	out, err := gitx.Run(storePath, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	want, _ := paths.Canonical(worktreePath)
	var current string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			current = strings.TrimPrefix(line, "worktree ")
			got, _ := paths.Canonical(current)
			if got == want {
				return filepath.Base(current), nil
			}
		}
	}
	return "", wkterr.New("WKT_WORKTREE_NAME_UNRESOLVED", "no worktree registration matches the created worktree").
		WithPath(worktreePath)
}
