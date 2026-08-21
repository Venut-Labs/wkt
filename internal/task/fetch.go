package task

import (
	"path/filepath"

	"github.com/Venut-Labs/wkt/internal/container"
	"github.com/Venut-Labs/wkt/internal/gitx"
	"github.com/Venut-Labs/wkt/internal/state"
	"github.com/Venut-Labs/wkt/internal/wkterr"
)

// FetchResult is one repository's outcome.
type FetchResult struct {
	Repo    string
	Updated bool
	Ref     string
	SHA     string
}

// Fetch brings each repository's task branch back into the workspace
// repository the developer actually works in.
//
// Fast-forward only, and never a forcing refspec (spec §6). The branch in
// their repository is theirs: if it has moved somewhere this fetch cannot
// reach, the answer is to say so — naming both commits — and offer another
// name through as, not to overwrite it and mention it afterwards.
func Fetch(c container.C, name, as string) ([]FetchResult, error) {
	t, err := state.Load(c.StateDir(), name)
	if err != nil {
		return nil, err
	}
	target := name
	if as != "" {
		target = as
	}

	// Check the whole set before moving any ref: a fetch that updates two
	// repositories and then refuses on the third leaves the developer holding
	// half of a task.
	type plan struct {
		repo state.Repo
		sha  string
		skip bool
	}
	plans := make([]plan, 0, len(t.Repos))
	for _, r := range t.Repos {
		storePath := filepath.Join(c.StoreDir(), r.StoreID+".git")
		sha, err := gitx.Run(storePath, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
		if err != nil || sha == "" {
			// The task has no branch here yet — nothing was committed in this
			// repository. Not an error.
			plans = append(plans, plan{repo: r, skip: true})
			continue
		}
		existing, err := gitx.Run(r.AbsPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+target)
		if err == nil && existing != "" {
			if existing == sha {
				plans = append(plans, plan{repo: r, sha: sha, skip: true})
				continue
			}
			// Fast-forward means the workspace's commit is an ancestor of the
			// task's. Anything else would need --force, which this command
			// does not have and will not grow.
			if !gitx.RunOK(r.AbsPath, "merge-base", "--is-ancestor", existing, sha) {
				return nil, wkterr.New("WKT_NOT_FAST_FORWARD",
					"the branch in the workspace is not an ancestor of the task's").
					WithRepo(r.RelPath).
					WithExpected("workspace "+target+" at "+short(existing)).
					WithFound("task "+name+" at "+short(sha)).
					WithRemedy("wkt fetch "+name+" --as <name> brings it in under another name",
						"or merge or rebase the two yourself; wkt will not force a ref you own")
			}
		}
		plans = append(plans, plan{repo: r, sha: sha})
	}

	var out []FetchResult
	for _, p := range plans {
		if p.skip {
			out = append(out, FetchResult{Repo: p.repo.RelPath})
			continue
		}
		storePath := filepath.Join(c.StoreDir(), p.repo.StoreID+".git")
		// Update the ref directly rather than fetching into a checked-out
		// branch: the developer may be standing on it, and a fetch that moves
		// the branch under their working copy is exactly the surprise this
		// command exists to avoid. update-ref refuses on a checked-out branch,
		// which is the behaviour we want.
		if _, err := gitx.Run(p.repo.AbsPath, "fetch", "--quiet", storePath,
			"refs/heads/"+name+":refs/heads/"+target); err != nil {
			return out, wkterr.New("WKT_FETCH_FAILED", "cannot bring the branch into the workspace repository").
				WithRepo(p.repo.RelPath).WithPath(p.repo.AbsPath).
				WithRemedy("if the branch is checked out there, switch away from it first")
		}
		out = append(out, FetchResult{
			Repo: p.repo.RelPath, Updated: true,
			Ref: "refs/heads/" + target, SHA: p.sha,
		})
	}
	return out, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
