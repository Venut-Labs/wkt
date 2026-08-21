# Changelog

## Unreleased

- **A task tree can be built in again.** The perimeter switches Claude Code's
  sandbox on, which confines writes to the working directory — and every
  toolchain keeps its cache outside it. Measured: an ordinary `go build` inside
  a task tree failed with `open ~/Library/Caches/go-build/…: operation not
  permitted`, and npm, cargo and pip fail the same way. A tree nobody can build
  in is a tree nobody works in, and the remedy people reach for is deleting the
  perimeter, which loses everything it is for. The cache roots are writable
  now — `~/.cache`, `~/Library/Caches`, Go's module cache, and whatever
  `GOCACHE`, `GOMODCACHE`, `CARGO_HOME` or `npm_config_cache` point at.
  Verified that the workspace, sibling trees and wkt's own state stay closed.

## v0.4.1 — 2026-08-21

Two reported defects, both about `--force` losing work, and one warning of my
own that fired on the ordinary case.

- **`--force` no longer deletes a repository created inside the tree**
  (issue #1). Someone starting a new service inside a task — `git init`, a few
  commits, nothing pushed — could lose that history entirely: the walk reported
  the directory as untracked content and stopped descending, so the repository
  below it was invisible to every later check. The walk now reports each
  untracked directory once and keeps going, and a repository whose history
  exists nowhere else is the one refusal `--force` does not cover.
- **A forced removal says where it kept your unpushed work** (issue #2). The
  objects always survived in the store, but nothing pointed at them, so
  recovery meant knowing the store's layout and digging for a dangling commit.
  `wkt rm --force` now parks the branch tip at `refs/wkt/removed/<task>` — only
  when there is something no remote has — and prints the command that reads it.
- `wkt init` no longer warns about repositories it just found. The
  below-the-bound warning fired on any directory *containing* a discovered
  repository, which is the product's ordinary shape (`services/svc-a`).

## v0.4.0 — 2026-08-21

Four defects, all found by measuring the design's own open questions rather
than by reading them. Two of the four were shipped in v0.3.0 and failed
silently.

Four fixes, all found by measuring the design's own open questions rather than
by reading them.

- **Commits made in a task now carry the repository's identity.** A bare clone
  copies no config and the store lives outside the workspace, so neither
  `.git/config` nor an `includeIf gitdir:` reached it: work done in a task was
  authored with whatever the *global* identity happened to be, silently, until
  a CI identity check or a DCO bot rejected the push. wkt now bridges
  `user.*`, the signing settings and `core.autocrlf`/`core.eol` into the store,
  reading what the repository *resolves* rather than what it stores — which is
  how corporate identities are usually configured. Nothing that can execute is
  carried: not `filter.*`, `core.sshCommand`, `gpg.program`, `credential.helper`,
  `trailer.*.command` or `url.*.insteadOf`.
- **Building a store no longer runs your template hooks.** Measured: a
  `reference-transaction` hook in `init.templateDir` fires four times during
  `git clone` — before any config can be written — and is copied into the
  store. The clone is now made with an empty template.
- **An unfinished store is never adopted.** A build interrupted after the clone
  left a directory that looked finished; wkt reused it, and the tree kept
  borrowing objects from your own repository, so a later `gc` or re-clone made
  every commit in the task unreadable. wkt now verifies the store's invariants
  before reuse and refuses with each broken one named — and never deletes or
  rebuilds it, because the store may hold the only copy of a task's unpushed
  commits.
- **`wkt sync` no longer says "up to date" when it could not look.** The origin
  fetch error was discarded, so a store that had never once reached its
  upstream reported success. It now says it could not reach origin, and exits 3.
- **A failed checkout says why.** `wkt new` and `wkt add` threw git's
  explanation away, so a repository whose checkout runs a content filter
  (git-lfs is the common one) failed with nothing but "cannot create the
  worktree". The reason is carried now — with the configured command redacted,
  because that is where people keep access tokens.

## v0.3.0 — 2026-08-21

The rest of the command table: a task can gain a repository, learn that its
base has moved, hand its work back, and survive the workspace being moved.

- `wkt repair TASK` **adopts a moved workspace.** State records absolute paths,
  so moving a project used to break every task in it beyond fixing: the
  worktrees detach, the back-fill links point at a workspace that is no longer
  there, and the perimeter denies directories that no longer exist. repair now
  rewrites the recorded paths, reattaches each worktree, re-aims the links and
  regenerates the perimeter. It never clears the way first — where a link slot
  has become a real directory, it says so and leaves the contents alone.

- `wkt add TASK --repos b` grafts another repository onto a live task **at the
  task's recorded base epoch**, not today's tip: a task is a coherent slice of
  time across a set of repositories, and a latecomer arriving at HEAD would be
  based on work the rest of the set has never seen. The repository's back-fill
  link becomes a real worktree, and a failed add puts the link back.
- `wkt sync TASK` fetches in every store and reports how far each repository's
  base has drifted — across **both** remotes, so a colleague's push and your own
  unpushed commits both count. It never advances the base itself.
- `wkt fetch TASK [--as NAME]` brings the task's branches back into the
  repositories you work in. Fast-forward only: if the branch there is not an
  ancestor, it refuses, names both commits, and offers `--as`. It never forces
  a ref you own, and it checks the whole set before moving any of them.

## v0.2.0 — 2026-08-21

Claude Code integration, and a command that reconciles what wkt thinks with
what is on disk.

### Claude Code worktree hooks

`claude --worktree` normally cuts a worktree from the one repository the
session was launched in. Point its hooks at wkt — `wkt hook install` prints the
block — and a session started in a multi-repo workspace lands in a task tree
covering every repository, all on one branch. Verified end to end against
Claude Code 2.1.238.

- Re-firing the event returns the same tree, since `--resume --worktree` fires
  it again.
- A suggested slug carrying a separator is sanitised rather than refused: the
  contract has no channel for "I renamed your slug".
- The removal path keeps every teardown refusal and puts the reason on stderr,
  where Claude Code shows it to you.
- Claude Code's own `.claude/.cc-writes` no longer blocks removal. It appears
  the first time an agent edits a file, so without this every task a session
  had actually worked in needed `--force`. Found by running the real thing.

### `wkt doctor`

Reconciles state against the disk: tasks whose tree is gone, directories in
`trees/` that no task claims, and base pins left in your repositories by tasks
that no longer exist. `--fix` repairs only what is unambiguous and never
removes anything that could hold work.

`wkt doctor --all` is the uninstall answer: it lists every `refs/wkt/*` wkt has
written into your own repositories, whether or not it is a problem. A tool that
writes into someone else's repository should be able to say exactly what it
put there.

### Fixes

- A command **waits** for another wkt process (up to 10s) instead of failing
  the moment two runs overlap. The premise is two agents at once, and a
  set-level operation takes well under a second — but it gives up rather than
  hanging forever on a stale holder.
- **Loose files over 1 MiB are linked into the tree, not copied.** Copying is
  right for what a task edits and wrong for what it only reads: the first real
  workspace this ran against had 19 slide images in an ancestor directory and
  copied all of them into every task. Measured there: 3.3M and 41 files down to
  1.7M and 35.
- `wkt init` **warns about a repository deeper than the discovery bound.** Such
  a repository makes its containing directory unlinkable — wkt will not share it
  writably with every task — and that refusal used to arrive at the first
  `wkt new` instead.

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
