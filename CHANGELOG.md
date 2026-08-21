# Changelog

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
