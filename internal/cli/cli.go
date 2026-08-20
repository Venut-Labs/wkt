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
  wkt status [TASK] [--workspace DIR] [--json]
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

	var positional string
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		positional, rest = rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}

	c, err := container.Locate(*ws)
	if err != nil {
		return fail(stderr, err)
	}

	switch cmd {
	case "init":
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
		for _, e := range entries {
			if e.Kind == discover.KindRepo {
				fmt.Fprintln(stdout, e.RelPath)
			}
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
				e := wkterr.New("WKT_TREE_MISSING", "task state exists but its tree is missing from disk").
					WithPath(treePath).
					WithRemedy("wkt status "+positional, "wkt rm "+positional+" --force")
				return fail(stderr, e)
			}
			return fail(stderr, wkterr.New("WKT_CHECK_FAILED", "cannot verify the tree").
				WithPath(treePath).WithFound(statErr.Error()))
		}
		fmt.Fprintln(stdout, treePath)
		return 0

	case "status":
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
