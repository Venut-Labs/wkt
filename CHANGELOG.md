# Changelog

## Unreleased

- A command now **waits** for another wkt process to finish (up to 10s) instead
  of failing outright the moment their runs overlap. The whole premise is two
  agents working at once, and a set-level operation takes well under a second.
- **Large loose files are linked, not copied.** Copying is right for the files a
  task edits and wrong for the ones it only reads: the first real workspace this
  ran against had 19 slide images in an ancestor directory, and every task
  copied all of them. Above 1 MiB the file appears in the tree as a link.
- `wkt init` **warns about a repository deeper than the discovery bound**. Such
  a repository makes its containing directory unlinkable — wkt will not share it
  writably with every task — and that refusal used to arrive at the first
  `wkt new`, one command after the one that walked the workspace.

- **Claude Code worktree hooks.** `wkt hook install` prints the settings block;
  after that `claude --worktree` in a multi-repo workspace produces a wkt task
  tree instead of a single repository's worktree. Re-firing the event returns
  the same tree, a suggested slug carrying a separator is sanitised rather than
  refused, and the removal path keeps every teardown refusal.
- Claude Code's own `.claude/.cc-writes` no longer blocks removal. It appears
  the first time an agent edits a file, so without this every task driven
  through the hooks needed `--force` — found by running the real thing.

- `wkt doctor` — reconciles state against the disk: tasks whose tree is gone,
  directories in `trees/` that no task claims, and base pins left in your
  repositories by tasks that no longer exist. `--fix` repairs only what is
  unambiguous and never removes anything that could hold work. `--all` lists
  every `refs/wkt/*` wkt has written into your own repositories, which is the
  answer you need before deciding to keep the tool.

## v0.1.1 — 2026-08-21

- `wkt version` now reports the tag a `go install github.com/Venut-Labs/wkt/cmd/wkt@v0.1.1`
  build came from. v0.1.0 said `dev` there, because that install path passes no
  ldflags; the version now falls back to the toolchain's own build info.

## v0.1.0 — 2026-08-21

First release. `wkt` adopts a multi-repo workspace, gives each task its own
tree of real git worktrees laid out in the workspace's shape, and refuses to
remove anything that would lose work.

### Commands

- `wkt init` — discover repositories, refuse genuine nested ones
  (`--exclude` adopts the workspace without them, recorded in container state),
  create the container. `--dry-run` writes nothing.
- `wkt new` — two-phase create with full rollback: nothing is left behind on
  failure. Warns when a selected repository carries a submodule, because `rm`
  refuses on one even with `--force`.
- `wkt path`, `wkt status` — where a task lives, and what has drifted.
- `wkt rm` — refuse-only teardown. Enumerates the filesystem rather than
  trusting its own state, and `--force` moves the tree into staging in one
  rename before deleting anything.
- `wkt perimeter` — generate or check the per-task settings that keep an agent
  from casually writing into the workspace or another task's tree.

### What it deliberately does not claim

Not a security boundary, and no read-only workspace: an agent inside a task
tree can still write into the workspace through a back-fill link. A perimeter
file covers only the directory it sits in, and `status` reports which
directories those are rather than implying protection.

### Verified rather than asserted

The behaviour of the Claude Code surfaces this depends on was measured against
release 2.1.238, not taken from documentation: deny rules reach the Bash
sandbox, a perimeter does not cover subdirectories, a narrower allow does not
escape a broader deny, and the deny list works to roughly 5,000 paths before
the profile stops compiling.

Tested by 12 packages of unit tests and a 12-scenario acceptance battery that
drives the real binary through real git repositories.
