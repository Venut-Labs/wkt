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
	// The task name is a branch name *and* a path segment: trees/<name>,
	// state/tasks/<name>.json, staging/<name>. check-ref-format accepts
	// "feature/x" because it is a perfectly good branch name, but as a path
	// it makes the state write fail on a directory that does not exist —
	// after the tree was already built — and the rollback then leaves an
	// empty trees/feature behind that blocks the plain name "feature"
	// forever. Refuse the separator here, before anything is created.
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, wkterr.New("WKT_BAD_TASK_NAME", "the task name is also a directory name, so it cannot contain a path separator").
			WithFound(name).
			WithRemedy("use a single-segment name such as " + flatten(name))
	}
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
		// Not "wkt rm <name>": this branch is only reachable when no task
		// state exists (a task with state fails above as WKT_TASK_EXISTS),
		// and rm on a stateless directory answers WKT_NO_TASK — a dead end
		// that left the user with no documented way out.
		return state.Task{}, wkterr.New("WKT_TREE_EXISTS", "the task tree directory already exists, but no task owns it").
			WithPath(treeRoot).
			WithRemedy("inspect the directory: it is left over from an interrupted create",
				"remove it once you are sure it holds nothing you need, then retry")
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
		wtName, err := worktreeName(r.WorktreePath)
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

// worktreeName reads back the admin directory git actually chose, which it
// derives from the leaf basename and silently disambiguates on collision
// (svc-a, svc-a1). repair cannot work without it (spec §5.4), so an
// unresolved name is an error, not a silently empty string persisted into
// state.
//
// Two different tasks on the same repository collide by construction: both
// task trees mirror the workspace shape, so both worktrees sit at a path
// whose basename is the repository's own leaf name (".../feat-a/svc-a" and
// ".../feat-b/svc-a"), even though git registers the second one under the
// store as "svc-a1". filepath.Base(worktreePath) — or, equivalently,
// filepath.Base of the *worktree path* reported by "git worktree list
// --porcelain" — is therefore always "svc-a" for both, which is simply
// wrong for the second task. The registration name has to be read back
// from the worktree's own gitdir instead: "git -C <worktree> rev-parse
// --git-dir" resolves to ".../store/<id>.git/worktrees/svc-a1", and its
// basename is git's actual choice. Confirmed empirically against real git
// before this fix: two worktrees added at paths that share a leaf basename
// register as "svc-a" and "svc-a1" under the store, verified via both
// "worktree list --porcelain" (which only ever reports the worktree
// *path*, never the admin name) and each worktree's own ".git" gitdir
// pointer.
func worktreeName(worktreePath string) (string, error) {
	gitDir, err := gitx.Run(worktreePath, "rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}
	name := filepath.Base(filepath.Clean(gitDir))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", wkterr.New("WKT_WORKTREE_NAME_UNRESOLVED", "cannot determine the store's worktree registration name").
			WithPath(worktreePath)
	}
	return name, nil
}

// flatten suggests a single-segment spelling of a name that carried
// separators, so the refusal above names a usable alternative.
func flatten(name string) string {
	f := strings.NewReplacer("/", "-", "\\", "-").Replace(strings.Trim(name, `/\`))
	if f == "" || f == "." || f == ".." {
		return "task"
	}
	return f
}

// SubmoduleWarnings names every selected repository that carries a submodule.
// Spec §5.7 requires the warning because rm refuses on a submodule even with
// --force — its object store lives under the doomed worktree — so a task
// created over one cannot be removed by any wkt command until the submodule
// is deinitialised. Warning at create time is the difference between knowing
// that before the work starts and discovering it at teardown.
func SubmoduleWarnings(entries []discover.Entry, selected []string) []Blocker {
	byRel := map[string]discover.Entry{}
	for _, e := range entries {
		byRel[e.RelPath] = e
	}
	var out []Blocker
	for _, rel := range selected {
		e, ok := byRel[rel]
		if !ok || e.Kind != discover.KindRepo {
			continue
		}
		sm, err := gitx.Run(e.AbsPath, "submodule", "status", "--recursive")
		if err != nil || strings.TrimSpace(sm) == "" {
			continue
		}
		out = append(out, Blocker{
			Code: "WKT_SUBMODULE", Repo: rel, Path: e.AbsPath,
			Detail: submodulePath(sm), Severity: "info",
		})
	}
	return out
}

// submodulePath pulls the submodule's path out of a "git submodule status"
// line ("<status><sha> <path> (<describe>)") instead of surfacing the raw
// line, which leads with a bare SHA and reads as noise.
func submodulePath(status string) string {
	if f := strings.Fields(firstLine(status)); len(f) >= 2 {
		return f[1]
	}
	return strings.TrimSpace(firstLine(status))
}
