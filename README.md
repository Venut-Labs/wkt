# wkt

[![CI](https://github.com/Venut-Labs/wkt/actions/workflows/ci.yml/badge.svg)](https://github.com/Venut-Labs/wkt/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Venut-Labs/wkt.svg)](https://pkg.go.dev/github.com/Venut-Labs/wkt)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

One task, one branch, many repositories.

A change rarely fits in one repository. `wkt` gives each task its own tree
where every participating repository is a real git worktree on the task
branch, **laid out in the same shape as the workspace**, so relative paths
between repositories keep resolving. Repositories left out of the task are
symlinked at their mirrored positions, so a partial selection still gives a
complete tree.

The workspace itself is never written to: task worktrees are cut from
per-repository bare stores kept in a container beside the workspace.

```
~/work/                        the workspace, a plain directory
  services/svc-a/              a repository
  services/svc-b/              another
  shared/                      another
  CONVENTIONS.md               a loose file

~/work.worktrees/trees/feat-42/
  services/svc-a/              worktree from the store, branch feat-42
  services/svc-b -> ~/work/services/svc-b     not in this task
  shared         -> ~/work/shared             not in this task
  CONVENTIONS.md               copy, hash-recorded
```

## Status

v0: `init`, `new`, `path`, `status`, `rm`. Removal is **refuse-only** — it
enumerates the filesystem rather than trusting its own state file, and
refuses while anything in the tree would lose work.

macOS and Linux. Windows is out of scope (symlinks plus deletion semantics).

## Install

```sh
go install github.com/Venut-Labs/wkt/cmd/wkt@latest
```

Or build from a clone — standard library only, no third-party dependencies:

```sh
go build -o wkt ./cmd/wkt
go test ./...                                  # unit tests
WT_CMD=$PWD/wkt bash test/run.sh               # acceptance battery
```

Requires `git` 2.29 or newer (`git worktree repair`), macOS or Linux.

## Use

```sh
wkt init --workspace ~/work            # adopt: discover repositories, build the container
cd ~/work                              # every verb defaults to --workspace .
wkt new feat-42 --all                  # a task over every repository
wkt new feat-42 --repos services/svc-a,shared
cd "$(wkt path feat-42)"               # work here
wkt status                             # what exists, and what has drifted
wkt rm feat-42                         # refuses while anything would be lost
wkt rm feat-42 --force                 # override, behind a staging fence

wkt perimeter --check                  # is each task's perimeter current?
wkt doctor                             # does state still match the disk?
wkt doctor --all                       # everything wkt has written into your repositories
```

`wkt doctor --all` is also the uninstall answer. wkt writes exactly one thing
into a repository of yours — a base pin under `refs/wkt/` — and `doctor` lists
every one of them, whether or not it is a problem, so trying the tool is not a
one-way door.

`init` refuses a genuine nested repository; `--exclude services/svc-a/vendored`
adopts the workspace without it and records the decision.

Exit codes: `0` consistent, `2` usage error or task already exists, `3` drift
detected, `4` no container (run `init`), `1` any other typed failure. Errors
are one line of JSON on stderr; a refusal that has several causes lists them
under `problems` (what is in the way), with `remedy` reserved for what to do.

## With Claude Code

`claude --worktree` normally cuts a worktree from the one repository the
session was launched in. Point its worktree hooks at wkt and it asks wkt
instead, so a session started in a multi-repo workspace lands in a task tree
covering every repository, all on one branch:

```sh
wkt hook install        # prints the block to paste into ~/.claude/settings.json
cd ~/work && claude --worktree
```

Verified end to end against Claude Code 2.1.238. The teardown keeps its
refusals on that path too: `WorktreeRemove` will not delete a tree holding
uncommitted work or unpushed commits, and says why on stderr, where Claude
Code shows it to you.

## What it does not promise

- **Not a security boundary**, and **no read-only workspace in v0**: an agent
  inside a task tree can write into the workspace through a back-fill link.
  `wkt` never writes there itself, and teardown never follows a link.
- **No conflict prevention.** Two tasks editing one file still conflict; the
  conflict moves to the eventual rebase.
- **No cross-repo atomic merge**, and **not a remote-side boundary** — a task
  tree can push to the real origin.
- **No runtime environment**: ports, compose projects, databases and
  dependency installation are out of scope.
- **A task over a repository with submodules cannot be removed** until the
  submodule is deinitialised — `new` warns when it sees one.
- **Loose files over 1 MiB are linked into the tree rather than copied**, so a
  directory of images or datasets is not duplicated per task. Small files are
  still copied, and a diverged copy blocks teardown.

## License

Apache License 2.0 — see [LICENSE](LICENSE). Copyright 2026 Venut Labs.

Apache-2.0 over MIT for one reason that matters to a tool people run at work:
it grants patent rights explicitly and terminates them for anyone who sues over
the software. Everything MIT permits — use, modify, redistribute, ship inside a
closed product — this permits too.

