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
	"strings"

	"wkt/internal/container"
	"wkt/internal/discover"
	"wkt/internal/gitx"
	"wkt/internal/state"
	"wkt/internal/task"
	"wkt/internal/wkterr"
)

const usage = `wkt — one task, one branch, many repositories

  wkt init   [--workspace DIR] [--dry-run]
  wkt new    TASK [--workspace DIR] [--repos a,b | --all]   (alias: create)
  wkt path   TASK [--workspace DIR]
  wkt status [TASK] [--workspace DIR]
  wkt rm     TASK [--workspace DIR] [--force]               (alias: cleanup)
`

func Run(args []string, stdout, stderr io.Writer) int {
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

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	ws := fs.String("workspace", ".", "workspace directory")
	repos := fs.String("repos", "", "comma-separated workspace-relative repository paths")
	all := fs.Bool("all", false, "select every discovered repository")
	force := fs.Bool("force", false, "remove even though work would be lost")
	dryRun := fs.Bool("dry-run", false, "report without writing anything")

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
		if pairs := discover.NestedPairs(entries); len(pairs) > 0 {
			e := wkterr.New("WKT_NESTED_REPO", "nested repositories are not supported")
			for _, p := range pairs {
				e = e.WithRemedy(p[0] + " is inside " + p[1])
			}
			return fail(stderr, e)
		}
		repoCount := 0
		for _, e := range entries {
			if e.Kind == discover.KindRepo {
				fmt.Fprintln(stdout, e.RelPath)
				repoCount++
			}
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
		t, err := task.Create(c, entries, positional, selected)
		if err != nil {
			return fail(stderr, err) // fail() maps WKT_TASK_EXISTS to 2
		}
		fmt.Fprintln(stdout, c.TreePath(t.Name))
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
			blockers, err := task.Preflight(c, t)
			if err != nil {
				return fail(stderr, err)
			}
			fmt.Fprintf(stdout, "%s  base epoch %s\n", t.Name, t.BaseEpoch.Format("2006-01-02 15:04:05Z"))
			for _, r := range t.Repos {
				fmt.Fprintf(stdout, "  %-28s %s\n", r.RelPath, r.Branch)
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
