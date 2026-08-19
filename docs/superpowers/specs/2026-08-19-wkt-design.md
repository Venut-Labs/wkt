# wkt — design

**Status:** design, not implemented. Written 2026-08-19.
**Working name:** `wkt`. Public name TBD (`wt` is taken by worktrunk).

One task, one branch, many repositories — materialised as a mirrored slice of a
multi-repo workspace, with a base that is coherent across the set.

---

## 1. What this is

A workspace is a plain directory — **not** a git repository — holding many
independent git repositories at different depths, plus non-git directories and
loose files:

```
workspace/
  services/svc-a/     git repo
  services/svc-b/     git repo
  docs/               git repo
  shared/             git repo
  notes/              plain directory
  CONVENTIONS.md      plain file
```

A task rarely fits in one repository. A typical change touches two or three
services plus docs, and the set has to stay consistent: one branch name, one
coherent base, one merge request per repository.

`wkt` gives each task its own tree where every participating repository is a real
worktree on the task branch, laid out **in the same shape as the workspace**, with
the rest of the workspace present read-only so an agent can read what it must not
change.

### 1.1 What it does not promise

State these in the README or users will assume them:

- **Not a security boundary.** The perimeter stops accidents, not a
  prompt-injected agent. Section 6 says exactly where it holds and where it does
  not — this was measured, not guessed.
- **No conflict prevention.** Two tasks editing the same file still conflict; the
  conflict moves from `git status` to the eventual rebase.
- **No cross-repo atomic merge.** GitHub has no primitive for it, Gerrit topics
  are explicitly non-atomic, GitLab merge trains are per-project. Nobody has
  solved this; we will not pretend to.
- **No runtime environment.** Ports, docker-compose project names, databases and
  dependency installation are out of scope. There is a documented seam
  (`post-create` hook, gitignored-file carry) and nothing more.
- **No Windows in v0.** The blocker is symlinks-plus-perimeter, not packaging.
- **Not a service multiplier.** One task owns the local stack at a time.

---

## 2. Prior art

Surveyed 2026-08-19 via web research and source reading. **Star counts and tool
names below come from automated research; verify any figure before quoting it
publicly.** Different research passes named different "category leaders", which
is itself a signal that the top of this market is unsettled.

### 2.1 Single-repo worktree managers for agents — saturated

`worktrunk` (`wt`, Rust, ~6.5k★) is the best-engineered pure CLI and hard-fails
outside a git repository (issue #3369) — which is exactly our workspace root.
Others in the same band: `claude-squad`, `gastown`, `vibe-kanban` (sunsetting),
`crystal` (deprecated → closed-source Nimbalyst), `uzi` (stale), `catnip` (gone).
Anthropic commoditised this layer with `claude --worktree` in Feb 2026.

**Conclusion: no differentiation left on the single-repo axis.**

### 2.2 Multi-repo worktree tools — attempted ~12 times, no traction

| Tool | Discovery on create | Layout | Removal safety |
|---|---|---|---|
| `nicksenap/grove` (~78★, Go) | depth 1 | flat, collides on basename | refuses dirty, `--force` destroys |
| `spawnpoint` (~9★, Python) | depth 2 | flat | **destructive by design**: auto `--force`, `rmtree` fallback, `branch -d`→`-D` |
| `etz`, `orbit`, `wsp`, `aw`, `devslot`, `trek`, `mwt`, `WorkForests`, `nanasess/gwm` | manifest or depth 1–2 (one does depth 8) | flat | dirty-check at best |

Nobody preserves the workspace's tree shape. Nobody handles non-git workspace
content (one exception at 2★). Nobody checks unpushed commits before removal
(one exception at 0★). Nobody confines the agent.

**The ceiling of this entire category is ~78 stars.** Read that in both
directions: the winner slot may be open, or two dozen people discovered the
audience is smaller than it feels from inside.

### 2.3 Adjacent

`jj` (`jj workspace`) is multiple working copies of **one** repo. Sapling: same.
Google `repo`: manifest-driven, `repo start` gives one branch across projects but
no worktrees, no isolation. JetBrains documents worktree support as
single-repository only (multi-root request open). VS Code has an open
workspace-level worktree request. Container tools (`container-use`, `sculptor`)
isolate one repo per agent.

### 2.4 Demand — honest

Two things are true at once.

**Intensity is real.** `openai/codex#11956` (multi-repo support, open, ~75
reactions, "the only blocker stopping me switching from Cursor"),
`anthropics/claude-code#23627` (~83–92 reactions, open), `opencode#4251` (user
with 30+ repos: agents "started working on other repositories where the other
three sessions were working… I had to revert the last few commits" — closed, not
planned).

**Volume is small.** Multi-repo is roughly 4% of the parallel-agent conversation.
Inside `anthropics/claude-code` the ratio of "worktree" to "multi-repo" issue
titles is about 30:1. Best-ever Show HN in this niche: 14 points, against
200–280 for single-repo equivalents. No funded company in the space. The 2026
industry narrative is pushing *toward* monorepos precisely because agents want
one context root.

**Realistic outcome if we win: 1–3k stars. More likely 100–500.** Kill criterion
in §9.

---

## 3. Verified hazards

Everything here was reproduced on this machine (macOS Darwin 25.6.0 / APFS,
git 2.50.1, Claude Code 2.1.220) during design. Each one is both a design
constraint and an acceptance test.

### H1 — `git worktree remove` destroys ignored files with no `--force`

A worktree whose only remaining content is a gitignored `.env` is deleted, exit
code 0. Git does not consider ignored files work. Generalises to ignored
*directories*. **Delegating removal to git is not safe.**

### H2 — `git status --porcelain` is empty during rebase, bisect and on detached HEAD

Git's own non-force refusal only fires on modified-tracked or
untracked-non-ignored content. An interactive rebase in progress, a bisect, or
commits made on a detached HEAD present as clean. Removal then takes
`.git/worktrees/<wt>/logs/HEAD` with it, and a later `gc --prune=now` erases the
commits. **Preflight cannot be `git status`.**

### H3 — `rm -rf link/` follows the symlink

```
rm -rf tree/        # symlinked target survives
rm -rf tree/link/   # target directory is DESTROYED, dangling link remains
```
The tree still looks intact afterwards. Go's `os.RemoveAll` is immune in every
variant including a joined trailing slash — one reason for choosing Go.

### H4 — stashes carry no worktree identity

A stash created in task A's worktree is listed from task B's worktree of the same
repository. The only signal in the object or reflog is git's auto-generated
`On <branch>:`, which degrades to `On (no branch):` when detached. Index-based
`stash drop` was demonstrated deleting a third task's stash under concurrency.
**Never resolve a stash by index; resolve to a SHA and re-verify.**

### H5 — the linked worktree's gitdir lives inside the workspace

`<workspace>/<repo>/.git/worktrees/<task>/`. Deny writes to the workspace and
`git add` fails with `Unable to create … index.lock: Operation not permitted`.
The agent can edit and cannot commit. Reproduced independently three times.

Carve-outs are dead twice over: Claude Code will not honour `allowWrite` beneath
a `denyWrite` subpath, and at OS level any hole big enough to commit
(`objects/` plus a writable ref path) yields branch hijack via `packed-refs`,
RCE via `core.pager`, and a `post-commit` hook that fires later in the
developer's *unsandboxed* shell. **This forces the store design in §5.2.**

### H6 — the settings perimeter is discovered from cwd

Generated deny rules hold at the tree root and stop applying one directory into
any symlink pointing out of the tree: the config root relocates, and the
workspace's `CLAUDE.md` loads in place of the task's. Same prompt, same target,
only cwd differs.

### H7 — the container directory is a cross-task injection channel

With the tree at `<container>/<task>/`, an agent writes `../CLAUDE.md` and a
fresh session in a **sibling task** loads it. Sibling task trees were also fully
writable. This is the exact collision the tool exists to prevent, moved one level
up.

### H8 — Claude Code's own worktree isolation did not fire

From a session whose `pwd` was `<repo>/.claude/worktrees/t3`, both a `Write` tool
call and a Bash redirect wrote into the main checkout. Reproduced twice
independently, including under `--permission-mode acceptEdits`. The four
documented enforcement checks are not something to build on.

### H9 — `WorktreeRemove` never fires

Neither on the hook path nor on the built-in path. `ExitWorktree(remove)` in a
non-git workspace refuses permanently. Any plugin-driven task leaks its tree,
branches and worktree registrations. **A removal guarantee cannot live in a
hook.**

### H10 — one branch name breaks five ways

Existing divergent branch (silently reused → repos on different bases);
default branch not `main` (aborts mid-set, no rollback, partial tree left);
branch already checked out elsewhere; D/F ref conflict (`feat/42` permanently
blocks `feat`); case-fold collapse on APFS (macOS and Linux disagree).

### H11 — `--shared` alternates die to a workspace `gc`

A bare mirror created with `git clone --shared` lets a task worktree commit while
the workspace is fully write-denied — but once the base commit becomes
unreachable in the workspace and `git gc --prune=now` runs there, the tree
reports `fatal: bad object HEAD` and store `fsck` reports an invalid sha1
pointer. Mitigation, verified: pin `refs/wkt/base/<task>` **in the workspace
repo** before the store fetch, delete only at teardown.

### H12 — atomic-save severs a symlink

`mv`, `perl -i`, `jq > tmp && mv`, and any editor's write-temp-then-rename
replace the *link* with a regular file. The perimeter protects the target, not
the link slot — and a blocked write teaches the agent to do exactly this.

### H13 — the perimeter can protect itself

Adding `Edit(<tree>/.claude/**)` to deny and `<tree>/.claude` to
`sandbox.filesystem.denyWrite` blocked the Write tool, a Bash redirect **and**
`rm -f` against the settings file, while normal writes inside the tree still
worked. Use it.

### H14 — the `WorktreeCreate` payload differs from the public docs

Observed on 2.1.220: `{session_id, transcript_path, cwd, hook_event_name, name}`,
with `CLAUDE_PROJECT_DIR` in the environment. `transcript_path` names a file that
is never created. `--resume --worktree` re-fires the event. Treat the payload
shape as unstable and pin a tested version range.

### H15 — the store design works (verified end to end)

Not a hazard but the load-bearing positive result, reproduced here rather than
taken on trust:

```
git clone --shared --bare <ws>/services/svc-a  <container>/store/svc-a.git   # 88K
git -C <container>/store/svc-a.git remote set-url origin <real origin>
git -C <ws>/services/svc-a update-ref refs/wkt/base/feat-42 <base-sha>
git -C <container>/store/svc-a.git worktree add -b feat-42 <tree>/services/svc-a main
chmod -R a-w <ws>/services/svc-a          # workspace fully read-only
# in the tree:
git add -A && git commit   -> succeeds
git push -u origin feat-42 -> succeeds, branch lands on the real remote
# then, back in the workspace:
git reflog expire --expire=now --all && git gc --prune=now
# tree still healthy, log intact
```

The worktree's gitdir resolves to `<container>/store/svc-a.git/worktrees/…`,
which is why the commit succeeds while the workspace is unwritable.

---

## 4. Positioning

**Headline:** one task, one branch, many repositories — a mirrored slice of your
workspace with a coherent base.

**Line two:** each task tree keeps agents inside it by default, so a run in one
task does not land in another task's checkout.

Isolation is deliberately *not* the headline. We measured our own perimeter and
it is defeated by `cd` into a symlink, by a path alias, by a shared parent
directory, and by severing a link. Selling it as a boundary invites a comparison
with containers that we lose, and a comparison with vendor-native sandboxing that
we also lose. Selling the shape is defensible: no vendor is going to ship
"materialise a mirrored slice of a plain directory full of repositories".

Single-repo is supported as a degenerate case but is not pitched. For one
repository, `claude --worktree` or `worktrunk` is the honest recommendation.

---

## 5. Architecture

### 5.1 Container

```
<container>/
  store/<repo-id>.git    bare mirror per repository
  trees/<task>/          task trees
  state/tasks/<task>.json  authoritative task state
  staging/               teardown fence
  cache/                 shared package caches (pnpm store, GOMODCACHE, …)
```

Default location `<workspace>.worktrees/`, **configurable**, with fallback to
`~/.local/state/wkt/<workspace-id>/` when the workspace's parent is not writable
(`$HOME` and volume roots are root-owned — reproduced) or when the workspace
lives under a known sync root (iCloud, Dropbox).

`wkt` owns the container level and guarantees no `CLAUDE.md` / `AGENTS.md` there,
re-asserting that guarantee on every start (H7).

### 5.2 Store — the foundation, not an optimisation

Per repository:

```
git clone --shared --bare <workspace>/<repo> <container>/store/<id>.git
git -C <container>/store/<id>.git remote set-url origin <repo's real origin>
git -C <workspace>/<repo> update-ref refs/wkt/base/<task> <base-sha>   # H11 pin
```

Task worktrees are cut from the store. Consequences:

- the worktree's gitdir is inside the store, which is writable → `git add`,
  `commit`, `rebase` work with the **workspace fully write-denied** (fixes H5);
- the developer's `hooks/`, `config`, `refs/heads` are out of the agent's reach —
  no RCE path, no branch hijack;
- teardown's blast radius is a directory `wkt` owns;
- the task survives the developer deleting or re-cloning the workspace repo;
- history is read through alternates, so the store costs kilobytes.

**Accepted cost:** the task branch does not appear in the developer's
`git branch` until fetched back. This is a real UX change; `wkt status` must
state it, and `wkt fetch <task>` brings the branch into the workspace repo on
demand.

**Mandatory guard:** the `refs/wkt/base/<task>` pin, created before the store
fetch and removed only at teardown, plus `gc.auto` guarded in the store.

### 5.3 Tree layout

```
<container>/trees/feat-42/
  services/svc-a/    worktree from store, branch feat-42     writable
  docs/              worktree from store, branch feat-42     writable
  services/svc-b  -> <workspace>/services/svc-b              not in the task
  shared          -> <workspace>/shared                      not in the task
  notes           -> <workspace>/notes                       non-git directory
  CONVENTIONS.md     copy (reconciled at teardown)           loose file
  .wkt/task.json     authoritative state
  .claude/settings.json  generated perimeter, self-protecting (H13)
```

Rules:

1. **Mirror the shape.** `services/svc-a` lands at `services/svc-a`. Never
   flattened, no option to flatten.
2. **Directories on the path to a selected repo are materialised**, not
   symlinked — `services/` is a real directory. Only leaves are linked.
3. **Un-materialised repositories are symlinked at their mirrored positions.**
   Without this, mirroring delivers nothing under a partial repo set:
   `../../shared` from `services/svc-a` points at a hole. Verified: with the
   symlink in place the reference resolves, and this works *only* under
   mirroring.
4. **The "is this a repository" scan is unbounded in depth**, even though the
   "is this one of my repositories" scan is not. A repo hiding below the
   discovery depth inside a symlinked directory would otherwise be shared by
   every task — the original collision, reproduced inside the tool.
5. **Loose files are copied**, not symlinked, and reconciled at teardown (H12).
   Every link slot is `lstat`-checked at teardown; a slot that is no longer a
   symlink blocks removal.
6. **Nested repositories (one repo inside another) are refused** at `init` with
   a named error. Mirroring can otherwise place one worktree inside another's
   ignored subtree, where removing the outer destroys the inner silently.

Why mirroring, precisely: flattening preserves a cross-repo reference A→B **iff
`dirname(A) == dirname(B)`**. For a workspace whose repos are all top-level, flat
is a no-op; for `services/svc-a` → `shared` it silently breaks. Two unconditional
wins flat cannot match: basename collisions (`services/api` and `tools/api`), and
back-filled un-materialised repos. Do not claim more than that in the README.

### 5.4 State

`state/tasks/<task>.json` is **authoritative and versioned**, not a hint:

- repo set, and per repository: absolute path, store id, branch name, base SHA,
  base ref name, worktree path;
- the base epoch for the whole task;
- link slots created, with type and target;
- copied loose files, with content hash;
- perimeter file hash;
- container and workspace canonical paths, all known spellings.

"Hint only, never authoritative" reads as humility and is in practice a decision
to have no recovery story — every unrecoverable failure found in review was
unrecoverable precisely because nothing authoritative was recorded. Git's own
metadata is not a substitute: `worktree remove` takes the reflog, a re-clone
takes any refs we wrote.

**But:** anything that *deletes* still enumerates the filesystem. State says what
should be there; the disk says what is there; `wkt status` reports disagreement
rather than trusting either.

### 5.5 Branch model

The branch name is the default task label — but **not** the task key. State holds
`task → {repo: branch}`, which allows per-repo suffixes when a name is taken.

Create is **two-phase**:

1. **Resolve and validate across the whole set** before touching anything: base
   per repository (`origin/HEAD` → `init.defaultBranch` → current HEAD, never a
   hardcoded `main`), branch existence locally and on the remote, ancestry
   against the base, `worktree list` occupancy, D/F ref conflicts, case-fold
   collisions (checked on every platform, not only case-insensitive ones),
   `check-ref-format`.
2. **Execute**, with rollback of everything created on any failure.

Never silently reuse an existing branch. Never surface raw git errors — they
contain absolute paths belonging to other tasks.

### 5.6 Perimeter

Generated into the tree, self-protecting per H13.

The two halves are not symmetrical, and the asymmetry must be understood before
writing the generator.

**Bash — already whitelist-shaped.** With `sandbox.enabled`, writes are limited
to the working directory and the session temp directory by default. Sibling task
trees, the container, the workspace and the home directory are therefore closed
to Bash without listing anything. Only `denyRead` needs enumerating.

**File-editing tools — deny only.** `Edit(…)` rules cover every file-editing
tool, but there is no allow-list form that survives: a `deny` beats a narrower
`allow`, so `deny <container>/**` would lock the task out of its own tree.
The generator therefore emits an explicit, regenerated-at-every-start deny list:

```json
{
  "permissions": {
    "deny": [
      "Edit(//<workspace>/**)",
      "Edit(//<container>/store/**)",
      "Edit(//<container>/state/**)",
      "Edit(//<container>/trees/<other-task>/**)",
      "Edit(//<tree>/.claude/**)"
    ]
  },
  "sandbox": {
    "enabled": true,
    "filesystem": {
      "denyWrite": ["<workspace>", "<container>/store", "<container>/state",
                    "<tree>/.claude"],
      "denyRead":  ["<container>/trees/<other-task>", "~/.ssh", "~/.aws",
                    "~/.config/gh", "~/.claude"]
    }
  }
}
```

Every path is listed in **all known spellings** — as typed, `realpath`, and the
macOS `/private` form. Deny globs are lexical; an alias such as
`~/work -> /Volumes/Data/work` defeats a single spelling entirely.

Sibling trees are enumerated by name, not covered by a glob, because a glob wide
enough to catch them also catches the task's own tree. That means a tree created
**after** this one is not in the list until the perimeter is regenerated —
`wkt status` reports the drift, and the hook regenerates on every session start.
This is a real limitation, not a rounding error, and belongs in the README.

`wkt status` hash-checks the perimeter file and refuses to report "isolated" on
drift.

### 5.7 Teardown

v0 is **refuse-only**. `wkt rm` enumerates from the filesystem and refuses while
any of the following exists, naming each:

- uncommitted or untracked content;
- ignored-but-precious content (`.env*`, credentials, report directories) —
  regenerable paths (`dist/`, `node_modules/`, `.venv/`) are listed and not
  blocking, or the guard stops meaning anything;
- unpushed commits, including the no-upstream and detached-HEAD cases
  (`rev-list --count HEAD --not --remotes` plus a per-worktree reflog scan);
- in-progress rebase / merge / cherry-pick / revert / bisect;
- a submodule with commits whose objects live under the doomed worktree;
- **any `.git` in the tree that does not belong to this task's repo set** —
  refuse even with `--force`; its history exists nowhere else;
- a link slot whose type changed (H12);
- a failed check of any kind — treat "cannot tell" as "would lose work".

`--force` in v0 does not destroy in place: it `mv`s the whole tree into
`staging/` in one rename (the fence), then removes from staging. Rollback is
`mv` back plus `git worktree repair`. Salvage refs and quarantine are deferred to
v0.2 — a mechanism that restores perfectly is worthless while it fires on the
wrong condition.

Removal order is innermost-first. All checks for every repository complete before
anything is removed.

---

## 6. Commands (v0)

| Command | Contract |
|---|---|
| `wkt init` | Canonicalise the workspace, discover repositories at unbounded depth (`.git` as **file or directory**), refuse nested repos and repos reached through a symlink, verify container writability, create the container, build stores lazily. |
| `wkt new <task> [--repos a,b] [--all]` | Two-phase create per §5.5. Materialise per §5.3. Write state, perimeter, base pins. Exit non-zero and leave nothing behind on failure. |
| `wkt add <task> <repo>` | Graft a repository at the task's **recorded base epoch**, not today's `origin/HEAD`. Resolve paths with symlinks expanded; refuse anything outside the tree. |
| `wkt status [<task>]` | Per repository: branch, base epoch and drift, dirty, ahead/behind, orphaned gitdir, link-slot integrity, perimeter hash. Reports state-vs-disk disagreement. Machine-readable with `--json`. |
| `wkt rm <task> [--force]` | §5.7. |
| `wkt repair <task>` | Fix gitdir back-pointers and relative symlinks after a workspace move or a repo re-clone. |
| `wkt fetch <task>` | Bring the task branches back into the workspace repositories for local inspection. |
| `WorktreeCreate` hook | Idempotent, reattach-by-default, tolerant of payload shape, materialises `.claude/settings.json` **before** printing the tree path. No `WorktreeRemove` hook — it never fires (H9). |

Deferred: `wkt run`, salvage/quarantine, `push`/`pr`, `adopt`, size-based
defaults, learned scope presets, plugin packaging, the `/wkt` skill.

`wkt run` is not merely deferred — as specified it cannot be built. Claude Code's
Bash tool dies on three undocumented private paths inside an external sandbox;
making `~/.claude` writable hands the agent hooks, `CLAUDE.md` and a live OAuth
token, which is strictly worse than no sandbox; and nested sandboxing is refused
by the kernel, with the agent's observed recovery being to disable its own
sandbox. Revisit only if the store design changes the premise.

---

## 7. Acceptance battery

Extends the five tests from the starter pack. Tool-agnostic, pointed at any
implementation via `WT_CMD`. Every hazard in §3 becomes a test.

**From the starter pack:** nested discovery; isolation; destructive cleanup;
flat-workspace control; foreign repository survives `--force`.

**Added, each derived from a reproduced failure:**

1. Ignored `.env` survives a plain `rm` (H1).
2. Interactive rebase in progress blocks removal (H2).
3. Detached-HEAD commit blocks removal (H2).
4. Symlink target survives teardown, including the trailing-slash path (H3).
5. A stash made by plain `git stash` in task A is not touched by task B's
   teardown (H4).
6. `git commit` succeeds inside the tree with the workspace fully write-denied
   (H5) — the single most important test in the suite.
7. Writing `../CLAUDE.md` from inside a tree does not reach the container (H7).
8. A sibling task tree is neither readable nor writable from another task (H7),
   **including a sibling created after this task's tree** — the case the deny
   enumeration in §5.6 does not cover until the perimeter is regenerated.
9. Existing divergent branch in one repo of the set aborts create and leaves
   nothing behind (H10).
10. Non-`main` default branch in one repo of the set aborts create cleanly (H10).
11. `gc --prune=now` in the workspace does not break a live task tree (H11).
12. An atomic-save that severs a link slot blocks removal (H12).
13. The agent cannot rewrite or delete its own perimeter file (H13).
14. Partial repo set: `../../shared` resolves from `services/svc-a` through the
    back-filled symlink.
15. A repository deleted from the workspace while its tree lives: `repair`
    recovers or refuses, and never silently destroys.

---

## 8. Form factor and distribution

Single repository containing:

- **the binary** (Go, static, macOS + Linux) — the product;
- **a `WorktreeCreate` hook script** giving native `claude --worktree <task>`;
- README carrying §1.1 verbatim.

Go over Rust: single static binary, trivial cross-compilation, low contribution
barrier, and `os.RemoveAll` immunity to H3. The workload is filesystem walking,
shelling to git and JSON — no part needs Rust's guarantees more than it needs
contributors.

Plugin packaging and the `/wkt` skill are v0.1. The plugin marketplaces contain
no worktree/multi-repo workspace manager at all, so the channel stays open.

Distribution: GitHub Releases and `go install` at launch; Homebrew when the
notability gate (75★ third-party / 225★ self-submitted) is met.

Launch: post into the threads where the demand is already documented and named —
`openai/codex#11956`, `anthropics/claude-code#23627`, `microsoft/vscode#318526`,
`worktrunk#3501` — rather than a Show HN with "worktree" in the title, which
measurably does not work in this category.

---

## 9. Plan

**v0 — 6–8 developer-weeks.** §6 minus deferrals, battery green.

Build order: `init` → store + base pins → `new` (two-phase) → `status` → `rm`
(refuse-only) → perimeter → `repair` → hook. Keep the flat-workspace control test
green from the first commit.

**Kill criterion.** If installs and stars are negligible 30 days after launch,
the 78-star ceiling was real. Stop, and publish the battery plus the hazard
register as a standalone artifact — that has value even if the tool does not.

**Deliberately unfunded:** the full design as first drafted was estimated at
22–30 developer-weeks, and `wkt run` never finishes at all.

---

## 10. Open questions

1. Container naming and the exact fallback rule when the parent is unwritable,
   the workspace is `$HOME`, or it sits under a sync root.
2. State file format and migration policy.
3. Branch namespacing: bare `<task>` or `wkt/<task>`. Namespacing removes D/F
   collisions between `feat` and `feat/42` but makes branches uglier in the
   forge.
4. Path-length warning threshold (macOS `AF_UNIX` caps at 103 bytes, which
   mirroring plus the container level can exceed; a short exported `TMPDIR` is
   the likely mitigation).
5. Whether `wkt init` should offer to add the container to the workspace
   repositories' `.gitignore` files.
6. Whether the perimeter should be opt-out for users who dislike generated
   settings files.

---

## 11. Decision log

| # | Decision | Status after adversarial review |
|---|---|---|
| D1 | Trees outside the workspace | Kept; container location made configurable, container guaranteed free of agent instruction files |
| D2 | Mirror the workspace shape, never flatten | **Survives**; strengthened by back-filling un-materialised repos |
| D3 | Non-git content symlinked | Changed: ancestor directories materialised, loose files copied and reconciled, repo scan unbounded in depth |
| D4 | Atomic teardown, `--force` salvages | Changed: v0 refuse-only, staging-rename fence, salvage deferred |
| D5a | Perimeter via generated settings | Changed: canonicalised, self-protecting, described as accident prevention |
| D5b | `wkt run` OS sandbox | **Killed** — not buildable as specified |
| D6 | Whole workspace readable | Changed: sibling trees and credential paths deny-read |
| D7 | Mutable repo set | Changed: base epoch recorded per repo; size-defaults and presets cut |
| D8 | One branch name across repos | Changed: default label, not the key; per-repo mapping; two-phase create |
| D9 | Own engine | Kept; worktrees addressed by absolute path only; `adopt` deferred |
| D10 | Go, macOS + Linux | Kept |
| D11 | Binary + plugin + skill | Changed: binary + `WorktreeCreate` hook in v0; `WorktreeRemove` removed entirely |
| D12 | `status` + `push` + PR | Changed: `status` in v0, `push`/PR deferred to v0.1 |
| D13 | Isolation as the headline | **Inverted**: multi-repo shape leads, isolation is line two |
| D14 | *(new)* Bare mirrors in the store | Added — without it `commit` and the perimeter are mutually exclusive |
