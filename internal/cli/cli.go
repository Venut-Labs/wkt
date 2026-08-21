// Package cli parses wkt's command line and calls into the packages that do
// the actual work. It is intentionally thin: its own value is the exit-code
// contract it exposes (0 consistent, 2 usage error or task already exists, 3
// drift detected, 4 container missing, 1 any other typed failure), because
// the acceptance battery drives the binary through exactly these verbs and
// codes.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Venut-Labs/wkt/internal/container"
	"github.com/Venut-Labs/wkt/internal/discover"
	"github.com/Venut-Labs/wkt/internal/doctor"
	"github.com/Venut-Labs/wkt/internal/gitx"
	"github.com/Venut-Labs/wkt/internal/hook"
	"github.com/Venut-Labs/wkt/internal/paths"
	"github.com/Venut-Labs/wkt/internal/perimeter"
	"github.com/Venut-Labs/wkt/internal/state"
	"github.com/Venut-Labs/wkt/internal/task"
	"github.com/Venut-Labs/wkt/internal/tree"
	"github.com/Venut-Labs/wkt/internal/wkterr"
)

const usage = `wkt — one task, one branch, many repositories

  wkt init   [--workspace DIR] [--dry-run] [--exclude a/inner,...]
  wkt new    TASK [--workspace DIR] [--repos a,b | --all]   (alias: create)
  wkt add    TASK --repos a,b [--workspace DIR]
  wkt sync   TASK [--workspace DIR]
  wkt repair TASK [--workspace DIR]
  wkt fetch  TASK [--as NAME] [--workspace DIR]
  wkt path   TASK [--workspace DIR]
  wkt status [TASK] [--workspace DIR]
  wkt rm     TASK [--workspace DIR] [--force]               (alias: cleanup)
  wkt perimeter [TASK] [--workspace DIR] [--check]
  wkt doctor [--workspace DIR] [--fix] [--all]
  wkt hook   install | worktree-create | worktree-remove | session-start
  wkt version
`

// Version is filled in by the binary at startup; see cmd/wkt.
var Version = "dev"

// stdin is where the hook verbs read their payload from. It is a variable so
// tests can drive the contract without a subprocess.
var stdin io.Reader = os.Stdin

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version" || args[0] == "-v") {
		fmt.Fprintln(stdout, "wkt "+Version)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if major, minor, err := gitx.Version(); err != nil || major < 2 || (major == 2 && minor < 29) {
		fmt.Fprintln(stderr, "WKT_GIT_TOO_OLD: git 2.29 or newer is required")
		return 1
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "create":
		cmd = "new"
	case "cleanup":
		cmd = "rm"
	}

	// One flag set per verb, not one shared by all of them. Sharing meant
	// "wkt path t --force --all" was accepted in silence, which is the same
	// failure as init succeeding on a workspace that does not exist: input
	// that means nothing must not read as success (finding F6).
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var ws, repos, exclude, as *string
	var all, force, dryRun, check, fix *bool
	nul := func() *string { v := ""; return &v }
	nulB := func() *bool { v := false; return &v }
	ws, repos, exclude, as = nul(), nul(), nul(), nul()
	all, force, dryRun, check, fix = nulB(), nulB(), nulB(), nulB(), nulB()

	ws = fs.String("workspace", ".", "workspace directory")
	switch cmd {
	case "init":
		dryRun = fs.Bool("dry-run", false, "report without writing anything")
		exclude = fs.String("exclude", "", "comma-separated nested repositories to exclude from adoption")
	case "new":
		repos = fs.String("repos", "", "comma-separated workspace-relative repository paths")
		all = fs.Bool("all", false, "select every discovered repository")
	case "add":
		repos = fs.String("repos", "", "comma-separated workspace-relative repository paths to graft on")
	case "fetch":
		as = fs.String("as", "", "bring the branch in under another name")
	case "rm":
		force = fs.Bool("force", false, "remove even though work would be lost")
	case "doctor":
		fix = fs.Bool("fix", false, "repair what is unambiguous")
		all = fs.Bool("all", false, "also list what wkt wrote on purpose")
	case "perimeter":
		check = fs.Bool("check", false, "report without writing anything")
	}

	// The task name may fall anywhere among the flags: before all of them,
	// after all of them, or split between two of them ("new --workspace WS
	// task --repos svc-a"). Go's flag.Parse only ever consumes a run of
	// flags up to the first non-flag token and then stops for good, so a
	// single Parse call cannot find a positional on the far side of a later
	// flag — it would leave that later flag sitting unparsed in Args(),
	// silently keeping its default instead of erroring. Concretely: "new
	// --workspace WS task1 --repos svc-a" left --repos unparsed, so
	// selection() fell back to its --all default and materialised every
	// repository the user never asked for, while still reporting success.
	// splitPositional extracts the one positional itself, by walking the
	// flags, before fs.Parse ever runs, so every flag on both sides reaches
	// it in one pass.
	positional, rest := splitPositional(fs, rest)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	// Belt and braces: splitPositional found at most one bare token and
	// removed it, so fs.Parse should never have anything left in Args().
	// If it does — two positionals, or a shape splitPositional could not
	// safely place — refuse rather than silently pick one and discard the
	// rest.
	if fs.NArg() > 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	c, err := container.Locate(*ws)
	if err != nil {
		return fail(stderr, err)
	}

	switch cmd {
	case "init":
		if info, statErr := os.Stat(c.Workspace); statErr != nil || !info.IsDir() {
			e := wkterr.New("WKT_NO_WORKSPACE", "the workspace does not exist or is not a directory").
				WithPath(c.Workspace)
			if statErr != nil {
				e = e.WithFound(statErr.Error())
			}
			return fail(stderr, e)
		}
		entries, err := discover.Walk(c.Workspace, 4)
		if err != nil {
			return fail(stderr, err)
		}
		// Exclusions are cumulative: what this run passes, plus whatever an
		// earlier run recorded (spec §5.3 rule 6, "recorded in container
		// state"). Without the recorded half, a workspace adopted once with
		// --exclude would refuse on every later init.
		prior, err := state.LoadContainer(c.ConfigDir())
		if err != nil {
			return fail(stderr, err)
		}
		excluded := map[string]bool{}
		for _, p := range prior.Excluded {
			excluded[p] = true
		}
		pairs := discover.NestedPairs(entries)
		for _, p := range splitList(*exclude) {
			nested := false
			for _, pair := range pairs {
				if pair[0] == p {
					nested = true
					break
				}
			}
			if !nested {
				// A typo, or an attempt to drop an ordinary repository —
				// which is not what this flag is for: an unexcluded
				// top-level repository still has to be discovered, or its
				// directory would be linked whole and share a repository
				// writably with every task (spec §5.3 rule 4).
				return fail(stderr, wkterr.New("WKT_NO_SUCH_NESTED_REPO", "not a nested repository in this workspace").
					WithRepo(p).
					WithRemedy("run wkt init to see which repositories are nested"))
			}
			excluded[p] = true
		}
		var stillNested [][2]string
		for _, p := range pairs {
			if !excluded[p[0]] {
				stillNested = append(stillNested, p)
			}
		}
		if len(stillNested) > 0 {
			e := wkterr.New("WKT_NESTED_REPO", "nested repositories are not supported")
			for _, p := range stillNested {
				e = e.WithProblem(wkterr.Problem{Code: "WKT_NESTED_REPO", Repo: p[0], Detail: "inside " + p[1]})
			}
			return fail(stderr, e.WithRemedy(
				"move the inner repository out of the outer one",
				"or adopt the workspace without it: wkt init --exclude "+stillNested[0][0]))
		}
		repoCount := 0
		known := map[string]bool{}
		for _, e := range entries {
			if e.Kind == discover.KindRepo {
				fmt.Fprintln(stdout, e.RelPath)
				known[e.RelPath] = true
				repoCount++
			}
		}

		// A repository deeper than the discovery bound makes its containing
		// directory unlinkable: wkt refuses to share it writably with every
		// task (spec §5.3 rule 4). That refusal arrives at "wkt new", one
		// command after the one that walked the workspace — so warn here,
		// where the user is still deciding what this workspace looks like.
		var linkCandidates []string
		if ents, err := os.ReadDir(c.Workspace); err == nil {
			for _, e := range ents {
				if e.IsDir() && !known[e.Name()] {
					linkCandidates = append(linkCandidates, filepath.Join(c.Workspace, e.Name()))
				}
			}
		}
		for _, hidden := range tree.HiddenRepos(linkCandidates) {
			fmt.Fprintf(stderr, "warning: WKT_REPO_BELOW_BOUND %s lies deeper than the discovery bound; wkt new will refuse to link the directory above it rather than share that repository with every task\n", hidden)
		}
		if repoCount == 0 {
			return fail(stderr, wkterr.New("WKT_NO_REPOS", "no repositories were found under the workspace").
				WithPath(c.Workspace).
				WithRemedy("check that --workspace points at the intended directory"))
		}
		if *dryRun {
			return 0
		}
		if err := container.Create(c); err != nil {
			return fail(stderr, err)
		}
		if err := recordExclusions(c, excluded); err != nil {
			return fail(stderr, err)
		}
		return 0

	case "new":
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		// A container that init already built is required from here on;
		// without this check, an uninitialised workspace fails on the lock
		// file it cannot open (WKT_CONTAINER_UNUSABLE, exit 1) instead of
		// the documented exit 4.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		release, err := container.Lock(c)
		if err != nil {
			return fail(stderr, err)
		}
		defer release()
		entries, err := discover.Walk(c.Workspace, 4)
		if err != nil {
			return fail(stderr, err)
		}
		selected := selection(entries, *repos, *all)
		// Spec §5.7: warn before the work starts, because rm refuses on a
		// submodule even with --force, so a task created over one cannot be
		// removed by any wkt command until the submodule is deinitialised.
		for _, w := range task.SubmoduleWarnings(entries, selected) {
			fmt.Fprintf(stderr, "warning: %s %s carries the submodule %q; wkt rm will refuse to remove this task, --force included\n",
				w.Code, w.Repo, w.Detail)
		}
		t, err := task.Create(c, entries, positional, selected)
		if err != nil {
			return fail(stderr, err) // fail() maps WKT_TASK_EXISTS to 2
		}
		fmt.Fprintln(stdout, c.TreePath(t.Name))
		return 0

	case "add":
		// Grafting a repository onto an existing task, at the task's epoch
		// rather than today's tip (spec §6).
		if positional == "" || *repos == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		release, err := container.Lock(c)
		if err != nil {
			return fail(stderr, err)
		}
		defer release()
		entries, err := discover.Walk(c.Workspace, 4)
		if err != nil {
			return fail(stderr, err)
		}
		for _, w := range task.SubmoduleWarnings(entries, splitList(*repos)) {
			fmt.Fprintf(stderr, "warning: %s %s carries the submodule %q; wkt rm will refuse to remove this task, --force included\n",
				w.Code, w.Repo, w.Detail)
		}
		for _, rel := range splitList(*repos) {
			if err := task.Add(c, entries, positional, rel); err != nil {
				return fail(stderr, err)
			}
			fmt.Fprintln(stdout, rel)
		}
		return 0

	case "sync":
		// Fetch in every store and report drift. It never advances the base
		// itself: that is what the task was cut from, and moving it under
		// half-finished work is a decision, not a refresh (spec §6).
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		report, err := task.Sync(c, positional)
		if err != nil {
			return fail(stderr, err)
		}
		drifted := false
		for _, rep := range report {
			switch {
			case rep.Unreachable != "":
				// Not "up to date": wkt did not manage to look, and saying
				// otherwise is the one answer that misleads.
				drifted = true
				fmt.Fprintf(stdout, "  %-28s cannot reach origin: %s\n", rep.Repo, rep.Unreachable)
			case rep.Drifted:
				drifted = true
				fmt.Fprintf(stdout, "  %-28s %d commit(s) behind %s\n", rep.Repo, rep.Behind, short(rep.Tip))
			default:
				fmt.Fprintf(stdout, "  %-28s up to date\n", rep.Repo)
			}
		}
		if drifted {
			return 3
		}
		return 0

	case "fetch":
		// Bring the task's branches back into the repositories the developer
		// works in. Fast-forward only; a branch they own is never forced.
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		results, err := task.Fetch(c, positional, *as)
		if err != nil {
			return fail(stderr, err)
		}
		for _, res := range results {
			if res.Updated {
				fmt.Fprintf(stdout, "  %-28s %s -> %s\n", res.Repo, short(res.SHA), res.Ref)
			} else {
				fmt.Fprintf(stdout, "  %-28s nothing to bring over\n", res.Repo)
			}
		}
		return 0

	case "repair":
		// Fix the pointers a moved workspace breaks. It fixes; it never
		// clears the way first (spec §6).
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		report, err := task.Repair(c, positional)
		if err != nil {
			return fail(stderr, err)
		}
		for _, rep := range report {
			marker := " "
			if rep.Repaired {
				marker = "fixed"
			}
			fmt.Fprintf(stdout, "  %-5s %-28s %s\n", marker, rep.Repo, rep.Detail)
		}
		return 0

	case "hook":
		// Claude Code's worktree contracts. stdout carries the created
		// worktree's path and nothing else: any other line would be read as
		// part of it, which is why warnings go to stderr here as everywhere.
		sub := positional
		switch sub {
		case "install":
			printHookSettings(stdout)
			return 0

		case "worktree-create":
			payload, err := hook.ParseCreate(stdin)
			if err != nil {
				return fail(stderr, err)
			}
			if err := requireContainer(c); err != nil {
				return fail(stderr, err)
			}
			name := hook.Slug(payload.Name)
			// Reattach by default: --resume --worktree re-fires the event
			// (H14), and a second create for the same name must hand back the
			// same tree rather than failing.
			if t, err := state.Load(c.StateDir(), name); err == nil {
				if _, statErr := os.Stat(c.TreePath(t.Name)); statErr == nil {
					fmt.Fprintln(stdout, c.TreePath(t.Name))
					return 0
				}
			}
			release, err := container.Lock(c)
			if err != nil {
				return fail(stderr, err)
			}
			defer release()
			entries, err := discover.Walk(c.Workspace, 4)
			if err != nil {
				return fail(stderr, err)
			}
			selected := selection(entries, "", true)
			for _, w := range task.SubmoduleWarnings(entries, selected) {
				fmt.Fprintf(stderr, "warning: %s %s carries the submodule %q; wkt rm will refuse to remove this task, --force included\n",
					w.Code, w.Repo, w.Detail)
			}
			t, err := task.Create(c, entries, name, selected)
			if err != nil {
				return fail(stderr, err)
			}
			fmt.Fprintln(stdout, c.TreePath(t.Name))
			return 0

		case "worktree-remove":
			payload, err := hook.ParseRemove(stdin)
			if err != nil {
				return fail(stderr, err)
			}
			if err := requireContainer(c); err != nil {
				return fail(stderr, err)
			}
			name, err := taskForTree(c, payload.WorktreePath)
			if err != nil {
				return fail(stderr, err)
			}
			// The refusals stay: this is a different entry point, not a way
			// around teardown. A non-zero exit shows stderr to the user, so
			// the reason reaches them.
			if err := task.Remove(c, name, false); err != nil {
				return fail(stderr, err)
			}
			return 0

		case "session-start":
			// Regenerate this task's perimeter, since WorktreeCreate only
			// fires for the tree being created and a sibling made since then
			// is not named in this one's deny list (H16).
			if err := requireContainer(c); err != nil {
				return 0 // a session outside a container is not an error here
			}
			cwd, err := os.Getwd()
			if err != nil {
				return 0
			}
			name, err := taskForTree(c, cwd)
			if err != nil {
				return 0
			}
			t, err := state.Load(c.StateDir(), name)
			if err != nil {
				return 0
			}
			names, _ := state.List(c.StateDir())
			coverage, hashes, err := perimeter.Write(c, t, names)
			if err != nil {
				return 0
			}
			t.PerimeterCoverage, t.PerimeterHashes = coverage, hashes
			_ = state.Save(c.StateDir(), t)
			return 0
		}
		fmt.Fprint(stderr, usage)
		return 2

	case "doctor":
		// Reconcile, and with --fix repair only what is unambiguous. Also the
		// uninstall path: --all lists every ref wkt has written into the
		// user's own repositories, whether or not it is a problem, because a
		// tool that writes into someone else's repository has to be able to
		// answer that completely.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		findings, err := doctor.Run(c, *fix)
		if err != nil {
			return fail(stderr, err)
		}
		problems := 0
		for _, f := range findings {
			if f.Info && !*all {
				continue
			}
			marker := "!"
			switch {
			case f.Fixed:
				marker = "fixed"
			case f.Info:
				marker = "i"
			}
			fmt.Fprintf(stdout, "  %-5s %-20s %s\n    %s\n", marker, f.Code, f.Path, f.Detail)
			if !f.Info && !f.Fixed {
				problems++
			}
		}
		if problems > 0 {
			return 3
		}
		return 0

	case "perimeter":
		// Regenerate, or with --check report and write nothing. Drift exits 3,
		// matching status's contract, so a script can gate on either.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		names, _ := state.List(c.StateDir())
		if positional != "" {
			names = []string{positional}
		}
		drift := false
		for _, n := range names {
			t, err := state.Load(c.StateDir(), n)
			if err != nil {
				return fail(stderr, err)
			}
			if *check {
				// A task with no recorded coverage is not "nothing to check":
				// it is a task with no perimeter, which is what every task
				// created before this feature existed looks like. Reporting
				// clean there would make the check useless exactly where it
				// is needed.
				if len(t.PerimeterCoverage) == 0 {
					drift = true
					fmt.Fprintf(stdout, "%s  uncovered  no perimeter recorded\n", n)
					continue
				}
				div, err := perimeter.Verify(c, t)
				if err != nil {
					return fail(stderr, err)
				}
				for _, d := range div {
					drift = true
					fmt.Fprintf(stdout, "%s  %s  %s\n", n, d.Reason, d.Dir)
				}
				if len(div) == 0 {
					fmt.Fprintf(stdout, "%s  covered  %d directories\n", n, len(t.PerimeterCoverage))
				}
				continue
			}
			all, _ := state.List(c.StateDir())
			coverage, hashes, err := perimeter.Write(c, t, all)
			if err != nil {
				return fail(stderr, err)
			}
			t.PerimeterCoverage, t.PerimeterHashes = coverage, hashes
			if err := state.Save(c.StateDir(), t); err != nil {
				return fail(stderr, err)
			}
			fmt.Fprintf(stdout, "%s  %d directories\n", n, len(coverage))
		}
		if drift {
			return 3
		}
		return 0

	case "path":
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		// Without this, a task looked up in a container that was never
		// created silently reads as "no such task" (WKT_NO_TASK, exit 1)
		// rather than the documented exit 4 for a missing container.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		t, err := state.Load(c.StateDir(), positional)
		if err != nil {
			return fail(stderr, err)
		}
		// State says the task exists; the disk says whether its tree does.
		// Printing a path state remembers but disk no longer has is worse
		// than refusing, so verify before ever writing to stdout.
		treePath := c.TreePath(t.Name)
		if _, statErr := os.Stat(treePath); statErr != nil {
			if os.IsNotExist(statErr) {
				// "wkt rm <task> --force" used to be the advice here, but a
				// missing tree can never reach the staging fence --force
				// gates: Remove now goes straight to store/state cleanup
				// for a missing tree, so plain "wkt rm <task>" is both
				// sufficient and the honest remedy — there is nothing left
				// to force through.
				e := wkterr.New("WKT_TREE_MISSING", "task state exists but its tree is missing from disk").
					WithPath(treePath).
					WithRemedy("wkt status "+positional, "wkt rm "+positional)
				return fail(stderr, e)
			}
			return fail(stderr, wkterr.New("WKT_CHECK_FAILED", "cannot verify the tree").
				WithPath(treePath).WithFound(statErr.Error()))
		}
		fmt.Fprintln(stdout, treePath)
		return 0

	case "status":
		// status takes an optional task name, so there is no usage error to
		// check first; the container check is the first thing done. Without
		// it, an uninitialised workspace's absent state directory reads as
		// "zero tasks" and status silently exits 0 instead of the
		// documented exit 4.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		// Two different lists: which tasks to report on, and which tasks
		// exist. The staleness check needs the second — narrowing it to the
		// task being reported compares a perimeter against itself and always
		// says fine.
		allNames, _ := state.List(c.StateDir())
		names := allNames
		if positional != "" {
			names = []string{positional}
		}
		drift := false
		for _, n := range names {
			t, err := state.Load(c.StateDir(), n)
			if err != nil {
				return fail(stderr, err)
			}
			blockers, err := task.Preflight(c, t)
			if err != nil {
				return fail(stderr, err)
			}
			fmt.Fprintf(stdout, "%s  base epoch %s\n", t.Name, t.BaseEpoch.Format("2006-01-02 15:04:05Z"))
			// Size the column to the widest path present rather than a fixed
			// 28: real workspaces carry paths like
			// "DVS/Research/excalidraw-diagram-skill", which overran it and
			// pushed the branch column out of line (live-run finding L3).
			width := 0
			for _, r := range t.Repos {
				if len(r.RelPath) > width {
					width = len(r.RelPath)
				}
			}
			for _, r := range t.Repos {
				fmt.Fprintf(stdout, "  %-*s %s\n", width, r.RelPath, r.Branch)
			}
			// Coverage, then drift. A user needs to know which directories
			// this file actually governs, because it is never all of them:
			// a session started below a covered directory has no perimeter
			// at all (H6a), and saying otherwise would be the one lie this
			// feature cannot afford.
			fmt.Fprintf(stdout, "  perimeter  %d directories covered\n", len(t.PerimeterCoverage))
			div, err := perimeter.Verify(c, t)
			if err != nil {
				return fail(stderr, err)
			}
			for _, d := range div {
				drift = true
				fmt.Fprintf(stdout, "  ! WKT_PERIMETER_%s %s\n", strings.ToUpper(d.Reason), d.Dir)
			}
			if stale, err := perimeter.Stale(c, t, allNames); err == nil && stale && len(div) == 0 {
				drift = true
				fmt.Fprintf(stdout, "  ! WKT_PERIMETER_STALE   does not match the current task list; run wkt perimeter\n")
			}
			for _, b := range blockers {
				// Only blocking-severity entries are drift. Info-severity
				// entries (regenerable ignored content — node_modules,
				// dist, and friends) are printed too, marked informational,
				// so seeing "these twelve things are build output" is what
				// stops --force becoming reflexive, but an ordinary tree
				// that merely has a node_modules in it must not exit 3.
				marker := "!"
				if b.Severity == "info" {
					marker = "i"
				} else {
					drift = true
				}
				fmt.Fprintf(stdout, "  %s %-20s %s %s %s\n", marker, b.Code, b.Repo, b.Path, b.Detail)
			}
		}
		if drift {
			return 3
		}
		return 0

	case "rm":
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		// Without this, an uninitialised workspace fails on the lock file
		// it cannot open (WKT_CONTAINER_UNUSABLE, exit 1) instead of the
		// documented exit 4.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		release, err := container.Lock(c)
		if err != nil {
			return fail(stderr, err)
		}
		defer release()
		if err := task.Remove(c, positional, *force); err != nil {
			return fail(stderr, err)
		}
		return 0
	}

	fmt.Fprint(stderr, usage)
	return 2
}

// requireContainer reports whether init has already built the container
// this workspace resolves to. Locate only computes where the container
// would live; it never checks whether Create has actually run there, so
// every command that depends on the container's directories existing must
// check for itself or fail with whatever unrelated error it happens to hit
// first (an unopenable lock file, a state directory that silently yields no
// tasks).
func requireContainer(c container.C) error {
	if info, err := os.Stat(c.Root); err != nil || !info.IsDir() {
		return wkterr.New("WKT_NO_CONTAINER", "no container for this workspace").
			WithPath(c.Root).
			WithRemedy("wkt init --workspace " + c.Workspace)
	}
	return nil
}

// splitPositional finds wkt's one positional argument (the task name)
// wherever it falls among rest's flags — before them, after them, or split
// between two of them — and returns it separately from every flag token, in
// their original relative order, so a single fs.Parse afterwards sees every
// flag regardless of which side of the positional it was typed on.
//
// It must never guess wrong in the unsafe direction. If a value flag's
// separately-typed value cannot be told apart from a positional by looking
// at it alone (nothing here inspects a value's shape — that is deliberate:
// a workspace path or a repo list can itself start with anything), the
// value is skipped over structurally, by the flag preceding it, and is
// therefore never considered as a positional candidate. If no bare token is
// ever found, it returns rest untouched rather than fabricating one, so an
// unparsable shape falls through to fs.Parse's own error handling, or to the
// caller's fs.NArg()>0 check, instead of a wrong guess being silently acted
// on. See the round 3 regression this replaces: a single fs.Parse call left
// a flag typed after the task name sitting unparsed in Args(), silently
// keeping its zero value instead of erroring.
//
// Which flags consume a separately-typed value is derived from fs itself via
// VisitAll, not hand-maintained: a flag is boolean iff its Value implements
// the standard library's unexported-but-checkable "IsBoolFlag() bool"
// convention (flag.Bool's Value does; flag.String's does not). A future flag
// added to fs and forgotten here — the exact shape of a bug already fixed
// once on this branch — now classifies itself correctly with no separate
// list to keep in sync.
func splitPositional(fs *flag.FlagSet, rest []string) (positional string, remaining []string) {
	valueFlags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			return
		}
		valueFlags["-"+f.Name] = true
		valueFlags["--"+f.Name] = true
	})
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		if !strings.HasPrefix(tok, "-") {
			out := make([]string, 0, len(rest)-1)
			out = append(out, rest[:i]...)
			out = append(out, rest[i+1:]...)
			return tok, out
		}
		if strings.Contains(tok, "=") {
			continue // "--flag=value" is one token; nothing extra to skip
		}
		if valueFlags[tok] {
			i++ // skip the value flag's separately-typed value
		}
	}
	return "", rest
}

func selection(entries []discover.Entry, repos string, all bool) []string {
	if repos != "" {
		return strings.Split(repos, ",")
	}
	var out []string
	for _, e := range entries {
		if e.Kind == discover.KindRepo {
			out = append(out, e.RelPath)
		}
	}
	return out // --all is the default when neither flag is given (spec §6)
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, string(wkterr.JSON(err)))
	if e, ok := err.(*wkterr.E); ok {
		switch e.Code {
		case "WKT_TASK_EXISTS":
			return 2
		case "WKT_NO_CONTAINER":
			return 4
		case "WKT_TREE_MISSING":
			return 3
		}
	}
	return 1
}

// splitList parses a comma-separated flag value, ignoring empty fields so a
// trailing comma is not read as a repository named "".
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// recordExclusions persists init's --exclude decisions in a stable order, so
// a later init honours them without the flag being repeated.
func recordExclusions(c container.C, excluded map[string]bool) error {
	if len(excluded) == 0 {
		return nil
	}
	list := make([]string, 0, len(excluded))
	for p := range excluded {
		list = append(list, p)
	}
	sort.Strings(list)
	return state.SaveContainer(c.ConfigDir(), state.Container{Excluded: list})
}

// taskForTree maps a worktree path back to the task that owns it. The payload
// may spell the path any way — as typed, resolved, or the macOS /private
// form — so this compares through every known spelling rather than by string
// equality (spec §5.6).
func taskForTree(c container.C, worktreePath string) (string, error) {
	names, err := state.List(c.StateDir())
	if err != nil {
		return "", err
	}
	want := paths.Spellings(worktreePath)
	for _, n := range names {
		for _, have := range paths.Spellings(c.TreePath(n)) {
			for _, w := range want {
				if have == w {
					return n, nil
				}
			}
		}
	}
	return "", wkterr.New("WKT_NO_TASK", "no task owns that worktree path").
		WithPath(worktreePath).
		WithRemedy("wkt status lists the tasks this container knows about")
}

// printHookSettings emits the block to paste into ~/.claude/settings.json.
// Printing beats writing: that file is the user's, and wkt has no business
// editing it behind their back.
func printHookSettings(stdout io.Writer) {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "wkt"
	}
	fmt.Fprintf(stdout, `Add this to ~/.claude/settings.json, then run "claude --worktree" from a
workspace wkt has adopted. Claude Code will ask wkt for the worktree instead
of calling git, so the session lands in a task tree covering every repository.

{
  "hooks": {
    "WorktreeCreate": [
      { "hooks": [ { "type": "command", "command": "%s hook worktree-create" } ] }
    ],
    "WorktreeRemove": [
      { "hooks": [ { "type": "command", "command": "%s hook worktree-remove" } ] }
    ],
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "%s hook session-start" } ] }
    ]
  }
}

The SessionStart entry keeps a task's perimeter current: WorktreeCreate only
fires for the tree being created, so a task made later is not named in an
older task's deny list until something regenerates it.
`, exe, exe, exe)
}

// short is the seven-character prefix git itself shows.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
