# wkt — design

**Status:** design, not implemented. Written 2026-08-19, revised through three
adversarial review passes (decisions, document, and a final judge pass that ran
its own fixtures).
**Working name:** `wkt`. Public name TBD (`wt` is taken by worktrunk).
**Environment under test:** macOS Darwin 25.6.0 / APFS, git 2.50.1,
Claude Code 2.1.220 (current release at the time of writing: 2.1.235).

One task, one branch, many repositories — materialised as a mirrored slice of a
multi-repo workspace, with a base that is coherent across the set.

---

## 0. Threat model

The document does security engineering in §5.6, so it must say who the adversary
is. There isn't one. The perimeter exists to prevent **accidents**:

**In scope (accidents we prevent):**
- an agent edits a repository that is not part of its task;
- an agent commits to the wrong branch, or to a main checkout;
- an agent working on task A modifies task B's tree;
- a teardown deletes work the user did not know was there.

**Out of scope (we do not defend against):**
- a prompt-injected or hostile agent deliberately escaping. Every mechanism we
  use is defeatable — §5.6 lists how;
- **an agent that disables its own sandbox after a refusal.** Observed behaviour
  on 2.1.220, not a hypothetical (H17): the refused command is re-run with
  `dangerouslyDisableSandbox: true` and succeeds;
- exfiltration. The tree can read the workspace and reach the network;
- anything remote-side. A task can push to the real origin; branch protection on
  the forge is the only control there;
- a hostile repository (malicious hooks, `.gitattributes` filters).

Everything §5.6 does is defence in depth against mistakes, and the README must
say so in the first paragraph.

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
worktree on the task branch, laid out **in the same shape as the workspace**,
with the rest of the workspace reachable read-only.

### 1.1 What it does not promise

State these in the README or users will assume them:

- **Not a security boundary.** See §0 and §5.6.
- **No conflict prevention.** Two tasks editing one file still conflict; the
  conflict moves from `git status` to the eventual rebase.
- **No cross-repo atomic merge.** GitHub has no primitive, Gerrit topics are
  explicitly non-atomic, GitLab merge trains are per-project.
- **No runtime environment.** Ports, compose project names, databases and
  dependency installation are out of scope. There is a `post-create` seam and a
  gitignored-file carry, and nothing else. (No shared cache directory in v0 —
  see §5.1.)
- **Not a remote-side boundary.** A task tree can push to the repository's real
  origin.
- **No Windows in v0.** The blocker is symlinks (§5.3) plus deletion semantics
  (H3), not packaging.
- **Not a service multiplier.** One task owns the local stack at a time.

---

## 2. Prior art

Surveyed 2026-08-19. Figures below were verified with `gh api` on that date;
each is stamped. Anything that could not be verified has been removed rather
than hedged.

### 2.1 Single-repo worktree tooling — commoditised

`max-sixty/worktrunk` (`wt`) — **6,545★, Rust, pushed 2026-08-19** — is the
most-starred pure worktree CLI and the only one in the set with a documented JSON
automation contract. Its worktree commands require a repository context;
`wt -C <repo>` supplies it explicitly. A request to relax that
(issue #3369, *"Feature request: Allow running wt commands outside of a git
repository"*) was **closed unimplemented**.

The wider agent-session layer is much larger than the CLI: `BloopAI/vibe-kanban`
**27,849★** (last push 2026-04-24, sunsetting), `smtg-ai/claude-squad`
**8,342★**, `dagger/container-use` **4,014★**, `stravu/crystal` **3,108★**
(superseded 2026-02 by Nimbalyst, which is **not** closed source — it is a
public MIT repository), `imbue-ai/sculptor` **218★**.

Anthropic shipped `claude --worktree` natively. **No differentiation is left on
the single-repo axis.**

### 2.2 Multi-repo worktree tools — many attempts, no traction

| Tool | Verified | Discovery on create | Layout | Removal safety |
|---|---|---|---|---|
| `nicksenap/grove` (`gw`) | **78★**, Go, MIT, pushed 2026-08-14 | depth 1 (`os.ReadDir`) | flat, collides on basename | refuses dirty, `--force` destroys |
| `mihirgupta0900/spawnpoint` | **9★**, Python, MIT, pushed 2026-08-10 | depth 2, hardcoded | flat | **destructive by design**: auto `--force`, `rmtree` fallback, `branch -d`→`-D` |
| `nanasess/git-worktree-manager` | **2★**, Shell, pushed 2026-07-27 | immediate children | `<project>.worktrees/<task>/<repo>` | refuses dirty; the only tool found that symlinks non-git workspace content |
| `etz-dev/etz`, `orbcli/orbit`, `jganoff/wsp`, `lldxflwb/aw`, `yammerjp/devslot`, `marsvogel/WorkForests` and others | 0–15★ | manifest, or depth 1–2 (one does depth 8) | flat | dirty-check at best |

`gastownhall/gastown` (**17,668★**) is the large exception and belongs in this
section, not §2.1: a "town" holds many "rigs", each wrapping one repository, and
agent workspaces are worktrees. But it **owns the layout** — rigs are added by
cloning into its own tree — rather than adopting an existing directory, and
nested repositories inside a town are a known unresolved problem in its tracker.

**What the table shows.** Among tools that adopt an existing directory of
repositories, the largest is 78★. That is a narrow category and the boundary is
doing real work, so it is not evidence that the ceiling for *this idea* is 78
stars — `gastown` is a counterexample at 17.7k for the adjacent, layout-owning
shape. What holds unconditionally: no tool in the table preserves the workspace's
tree shape, none handles the workspace's non-git content except the 2★ one, and
none checks unpushed commits before removal.

### 2.3 Adjacent

`jj workspace` is several working copies of **one** repo; Sapling likewise.
Google `repo` gives one branch across projects via `repo start` but no worktrees
and no isolation. JetBrains documents worktree support as single-repository only.
`microsoft/vscode#318526` *"Workspace-level Worktree Support"* is **open** and is
close to a specification of this tool.

### 2.4 Demand — honest

**Intensity is real, and verified.**

- `openai/codex#11956` *"Multi-repo support"* — **open**. Neighbouring open
  issues in the same repo describe the same class:
  `#33813` *"Codex sandbox mounts empty .git at non-repo workspace root, breaking
  Git discovery in multi-repo workspaces"*, `#10817` *"Codex App Diff View fails
  to work with multi-repo directory"*.
- `anthropics/claude-code#23627` *"[FEATURE] Multi-repository support for
  remote/web sessions"* — **open, 92 reactions**, 2026-02-06. This is the
  highest-signal multi-repo demand artefact found anywhere, and it is the only
  citation here with real traffic behind it.
- `anthropics/claude-code#80442` (open, 2026-07-23) is the sharpest *description
  of the failure mode* — not evidence of demand, it carries 0 reactions and 1
  comment — and it is exactly our scenario: *"The session ran in a
  `.claude/worktrees/` worktree for repo A (correctly). The task then required
  Terraform changes in a second repo configured as an additional working
  directory. The agent edited files and attempted to create a branch and commit
  **directly in that second repo's primary checkout, on `main`**, instead of
  creating a worktree there as well."*
- Also open: `#73824` *"EnterWorktree fails for sub-repo worktrees in multi-repo
  workspaces"*, `#78505` *"Cloud multi-repo sessions never load repo
  `.claude/settings.json`"*, `#56853` *"Multi-folder workspace support in Claude
  Desktop for cross-repo dev"*, `#85448` (subagent worktree isolation binds to
  the caller's cwd, not the target repo).

**Volume is small.** Multi-repo terms are a low-single-digit percentage of
worktree-related issue titles in `anthropics/claude-code`. Show HN posts in this
niche top out in the tens of points, against 200+ for single-repo equivalents. No
funded company is building the one-branch-across-many-repositories shape; funded
companies occupy the single-repo and container-per-agent shapes.

**Realistic outcome if we win: low thousands of stars at best, more likely low
hundreds.** Kill criterion in §9.

---

## 3. Verified hazards

Reproduced on the environment named at the top. Each is a design constraint and
an acceptance test. Where a hazard is a property of a specific Claude Code
version, that version is named — those are observations to re-verify per release,
not permanent facts.

### H1 — `git worktree remove` destroys ignored files without `--force`

A worktree whose only remaining content is a gitignored `.env` is deleted, exit
0. Generalises to ignored directories. Git's non-force refusal fires only on
modified-tracked or untracked-non-ignored content. **Removal cannot be delegated
to git.**

### H2 — `git status --porcelain` is empty during rebase, bisect and on detached HEAD

An interactive rebase, a bisect, or commits made on a detached HEAD present as
clean, so removal proceeds and takes `.git/worktrees/<wt>/logs/HEAD` with it; a
later `gc --prune=now` erases the commits. **Preflight cannot be `git status`.**

Related and useful: `git worktree lock` makes a worktree refuse both `remove` and
`remove --force` (needs `-f -f`). `wkt` locks every worktree it creates.

### H3 — deletion primitives that follow symlinks

```
rm -rf tree/        # symlinked target survives
rm -rf tree/link/   # DESTROYS content under the target (macOS: the target
                    # directory itself is unlinked, dangling link left behind)
```
`find -L <tree> -delete` destroys the target too. Go's `os.RemoveAll` is immune
in every variant tested including a joined trailing slash — one reason for
choosing Go. **Immunity is a property of the deletion primitive, not of the
language: no shell-out for removal, ever, and never a symlink-following walker.**

### H4 — stashes carry no worktree identity

A stash created in task A's worktree is listed from task B's worktree of the same
repository. The only signal in the object or reflog is git's auto-generated
`On <branch>:`, which degrades to `On (no branch):` when detached. Index-based
`stash drop` was demonstrated deleting a third task's stash under concurrency.
**Never resolve a stash by index; resolve to a SHA and re-verify.**

A per-worktree ref namespace *does* exist — `refs/worktree/*` is genuinely scoped
per worktree — so a stash **wkt creates itself** can be attributed. A stash the
user or agent creates with plain `git stash` cannot.

### H5 — the linked worktree's gitdir lives inside the workspace

`<workspace>/<repo>/.git/worktrees/<task>/`. Deny writes to the workspace and
`git add` fails at `…/index.lock` — `Operation not permitted` under a sandbox
deny rule, `Permission denied` under `chmod -R a-w`. Same failure, different
errno. The agent can edit and cannot commit. Reproduced independently three
times. **This forces the store design in §5.2.**

Note on carve-outs: a hole sized exactly to permit committing
(`objects/`, `refs/`, `logs/`, `packed-refs`, `.git/worktrees/<task>/`) already
yields branch hijack over the developer's own branches, which is enough to reject
it. It does *not* by itself yield `core.pager` RCE or a `post-commit` hook —
those live in `config` and `hooks/`, which such a carve-out excludes. Claude
Code's own sandbox ships a linked-worktree carve-out of this shape. We reject the
approach anyway because it leaves the developer's refs writable by an agent, and
because the store removes the need for any hole at all.

### H6 — the perimeter is discovered at cwd, and instruction files relocate

Two distinct failures, previously conflated:

(a) Claude Code loads `.claude/settings.json` **from the session's cwd**. A
session whose cwd is a subdirectory of the tree does not pick up a perimeter file
written at the tree root — this is true for ordinary subdirectories, not only for
symlinks.

(b) `CLAUDE.md` discovery walks parents of the *physical* path, so a cwd inside a
symlink that points out of the tree loads the workspace's instructions instead of
the task's.

Consequences for §5.6: the perimeter must be written where the session will
actually look, for every plausible session cwd, and its coverage must be
verifiable at runtime rather than assumed.

### H7 — the container directory is a cross-task injection channel

With the tree at `<container>/trees/<task>/`, an agent writes `../CLAUDE.md` and
a fresh session in a **sibling task** loads it. Sibling trees were also fully
writable. The collision the tool exists to prevent, moved one level up.

### H8 — vendor worktree isolation was broken through 2.1.220, fixed in 2.1.222

On 2.1.220, from a session whose `pwd` was `<repo>/.claude/worktrees/t3`, both a
`Write` call and a Bash redirect wrote into the main checkout; reproduced twice.
The Claude Code changelog for **2.1.222** reads: *"Fixed worktree-isolated
sessions and their subagents being able to run destructive git commands against
the main checkout; isolation now applies to file edits and Bash in every session
type."* Independently, `anthropics/claude-code#80442` (open) reports the same
class of failure from the desktop app.

**So this is not a live vendor defect and must not be presented as one.** What
remains true, and is the actual argument: the vendor's isolation protects exactly
**one** main checkout — the repository the session was launched from. In a
workspace of four repositories, the other three are unprotected by construction,
which is what `#80442` describes. Re-verify this hazard on the current release
before publishing, and record the version.

### H9 — `WorktreeRemove` did not fire in the non-git workspace path

On 2.1.220, in a non-git workspace driven by a `WorktreeCreate` hook,
`ExitWorktree(remove)` refuses on the first call and names `discard_changes:
true` as the retry; the retry succeeds and **does** fire `WorktreeRemove` with a
well-formed `worktree_path` payload. It does not fire on git's built-in removal
path, at `-p` session exit, or when a subagent's worktree is swept — so a
plugin-driven task can still leak its tree, branches and registrations. **Either way, a removal guarantee cannot live in a hook: the documented
contract says `WorktreeRemove` cannot block, and its failures are logged in debug
mode only.**

### H10 — one branch name breaks five ways

Existing divergent branch — silently reused by `git worktree add <path> <branch>`
(the `-b` form used in §5.2 and H15 hard-errors instead) → repositories on
different bases; default branch not `main` → aborts mid-set with no rollback and
a partial tree; branch already checked out elsewhere; D/F ref conflict (`feat/42`
blocks `feat` for as long as the ref exists, and symmetrically; `pack-refs` does
not lift it); case-fold collapse on APFS, so macOS and Linux disagree.

### H11 — `--shared` alternates die to a workspace `gc`

Verified both halves. A store created with `git clone --shared` borrows objects
from the workspace repository. Once the base commit becomes unreachable there and
`git gc --prune=now` runs, the tree reports `fatal: bad object HEAD`. Pinning
`refs/wkt/base/<task>` **in the workspace repository, before the store is
created**, prevents it.

The pin does not make the store independent — see §5.2, which states precisely
what the store does and does not survive.

### H12 — atomic-save severs a symlink

`mv`, `perl -i`, `jq > tmp && mv`, and any editor's write-temp-then-rename
replace the *link* with a regular file. The perimeter protects the target, not
the link slot — and a blocked write teaches the agent to do exactly this. `vim`
and BSD `sed -i` behave better (write through, or fail loudly).

### H13 — the perimeter can be made to protect itself

Claude Code already treats settings files as protected; adding
`Edit(<tree>/.claude/**)` to deny and `<tree>/.claude` to
`sandbox.filesystem.denyWrite` was verified to block the Write tool, a Bash
redirect **and** `rm -f` against the settings file, while ordinary writes inside
the tree still worked.

### H14 — the `WorktreeCreate` payload differs from the public docs

Observed on 2.1.220, and the shape differs by entry path: five keys
(`session_id, transcript_path, cwd, hook_event_name, name`) on the `--worktree`
startup path, six on the `EnterWorktree` path (plus `prompt_id`), with
`CLAUDE_PROJECT_DIR` in the environment. `transcript_path` names a file that does
not yet exist at hook time on the startup path, but does exist with content on
the `EnterWorktree` path — so it must not be used as a readable path.
`--resume --worktree` re-fires the event. Treat the payload as unstable, tolerate
unknown fields, and pin a tested version range.

### H15 — the store design works (verified end to end)

The load-bearing positive result, reproduced rather than taken on trust. Note the
`chmod` toggles: the write-denied commit and the workspace `gc` were tested as
two separate conditions.

```
# pin FIRST, so no gc window exists before the store references the base
git -C <ws>/services/svc-a update-ref refs/wkt/base/feat-42 <base-sha>
git clone --shared --bare <ws>/services/svc-a <container>/store/<id>.git    # 88K
git -C <container>/store/<id>.git remote set-url origin <repo's real origin>
git -C <container>/store/<id>.git config remote.origin.fetch \
    '+refs/heads/*:refs/remotes/origin/*'          # bare clones set NO refspec
git -C <container>/store/<id>.git worktree add -b feat-42 <tree>/services/svc-a main

chmod -R a-w <ws>/services/svc-a                   # workspace read-only
#   in the tree: git add -A && git commit  -> succeeds
#                git push -u origin feat-42 -> succeeds, lands on the real remote
chmod -R u+w <ws>/services/svc-a
#   in the workspace: git reflog expire --expire=now --all && git gc --prune=now
#   tree still healthy, log intact
```

The worktree's gitdir resolves under `<container>/store/<id>.git/worktrees/…`,
which is why the commit succeeds while the workspace is unwritable.

**The refspec line is not optional.** `git clone --bare` writes no
`remote.origin.fetch`, so without it the store never creates `refs/remotes/*` —
which breaks both the sync path (§5.2) and the unpushed-commit check (§5.7),
silently and in the dangerous direction.

### H16 — the store and the perimeter compose (verified)

The first draft of §5.6 denied writes to the store, which would have re-broken
H5. The corrected shape was verified with a live Claude Code session in a tree
whose perimeter carried `allowWrite: [<container>/store]` together with narrower
`denyWrite` entries for the store's `hooks/`:

| Attempt from inside the tree | Result |
|---|---|
| `git add -A && git commit` in a materialised repo | **succeeds** |
| `echo x > <store>/svc-a.git/hooks/post-commit` | `operation not permitted` |
| `echo x > <workspace>/services/svc-a/src/index.js` | `operation not permitted` |

So a narrower `denyWrite` does win over a broader `allowWrite`, which is what
makes the store writable for commits while its hook directory stays closed.

**The converse does not hold**, and it matters for §5.6's size budget: a narrower
`allowWrite` inside a broader `denyWrite` does **not** re-open the region.
Verified — with `denyWrite: [<container>/trees]` and
`allowWrite: [<container>/trees/feat-42]`, a write into the task's **own** tree
was refused (`operation not permitted`). Deny wins in both directions, so
"one glob for all siblings plus an allow for mine" is not available.

### H17 — the Bash perimeter fails open, and it has a hard size ceiling

Two measured properties of Claude Code 2.1.220, either of which alone downgrades
the perimeter from "defeatable by a determined agent" to "routinely defeated by
an undetermined one".

**Fail-open.** After the sandbox refuses a write with `operation not permitted`,
Claude Code re-runs the same command with `dangerouslyDisableSandbox: true` on
its own initiative, and the write lands. Observed in 4 of 6 runs; in 2 the model
declined. This is the agent's normal recovery path, not an exploit.

**E2BIG.** The sandbox profile is passed to every command through `exec`. On
macOS `kern.argmax` is 1 MiB. A clean session already carries ~158 deny paths;
above roughly 234 total the profile exceeds the limit and **no Bash command runs
at all** — `Could not start /bin/zsh: the command line plus environment exceed
the OS exec argument limit (E2BIG)`. Measured growth: 150 added paths → 1.6 MB;
600 → 4.3 MB; 1500 → 9.8 MB. The observed recovery is, again, to retry with the
sandbox off.

**Consequence for the design.** Any perimeter whose entry count grows with the
number of tasks hits the ceiling at roughly 20–25 concurrent tasks — in a tool
whose premise is concurrency. §5.6 is therefore constrained to a **constant-size**
deny list, which rules out enumerating sibling trees by name.

---

## 4. Positioning

**Headline:** one task, one branch, many repositories — a mirrored slice of your
workspace with a coherent base.

**Line two:** each task tree carries a generated settings file that keeps an
agent inside it by default. §0 states what that is and is not; v0 makes no
isolation claim beyond it (§9).

Isolation is deliberately not the headline. We measured our own perimeter and it
is defeated by a cwd change, by a path alias, by a shared parent directory and by
severing a link. Selling it as a boundary invites a container comparison we lose
and a vendor comparison we lose. Selling the shape is defensible: no vendor is
going to ship "materialise a mirrored slice of a plain directory full of
repositories".

Single-repo is supported as a degenerate case but not pitched. For one
repository, `claude --worktree` or `worktrunk` is the honest recommendation.

### 4.1 Why the small-audience reading might be wrong

§2.4 says the audience is small, and §2.2 says a dozen people already tried. Both
are true, and here is the checkable proposition that would make this attempt
different: **every prior attempt flattened the tree, and none refused to lose
work.** If the reason none of them retained users is that a flattened tree breaks
cross-repo references and their teardown destroyed something, then shape and
safety are the product and the audience was never the problem. If instead people
tried them, found them fine, and simply stopped needing them, the audience is the
problem and no amount of correctness fixes it.

That is a testable difference, and the first thirty days after launch answer it.

---

## 5. Architecture

### 5.1 Container

```
<container>/
  store/<repo-id>.git      bare mirror per repository
  trees/<task>/            task trees
  state/tasks/<task>.json  authoritative task state
  staging/                 teardown fence
```

Default location: the workspace path with `.worktrees` appended as a **sibling**
— `/Users/me/work` → `/Users/me/work.worktrees`. The container must never be a
descendant of the workspace. Configurable per §5.9, with fallback to
`~/.local/state/wkt/<workspace-id>/` when the workspace's parent is not writable
(`$HOME` and volume roots are root-owned — reproduced) or when the workspace sits
under a known sync root.

`wkt` owns the container and guarantees no `CLAUDE.md` / `AGENTS.md` at that
level, re-asserting the guarantee on every start (H7).

No shared `cache/` in v0: a writable directory shared by all tasks is a
cross-task channel by the same argument as H7, and §1.1 says runtime environment
is out of scope.

### 5.2 Store — the foundation, not an optimisation

Per repository, in this order (H15):

1. `git update-ref refs/wkt/base/<task> <base-sha>` **in the workspace repo**;
2. `git clone --shared --bare <workspace>/<repo> <container>/store/<repo-id>.git`;
3. `git remote set-url origin <the repository's real origin>`;
4. `git config remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*'`;
5. `git config gc.auto 0`, `git config core.hooksPath /dev/null` (the store never
   runs hooks; the workspace's hooks are not inherited and none are wanted);
6. `git worktree lock` on every worktree created.

`<repo-id>` is a collision-free function of the workspace-relative path, **never
the basename**: `slug(rel-path) + "-" + hex(sha256(canonical-abs-path))[:8]`, so
`services/api` and `tools/api` cannot collide.

**What this buys.** The worktree's gitdir is inside the store, which is writable,
so `git add`, `commit` and `rebase` work with the workspace fully write-denied
(fixes H5). The *workspace* repository's `hooks/`, `config` and `refs/heads` are
out of the agent's reach — no hijack of the developer's branches. Teardown's
blast radius is a directory `wkt` owns.

**What this does not buy.** The store has its own `hooks/` and `config`, and they
sit in a directory the agent can write; §5.6 denies them explicitly and step 5
disarms hooks.

**Two remotes, not one.** A de-borrowed store is a snapshot: it does **not** see
commits the developer has made locally in the workspace and not pushed.
Demonstrated — `git worktree add -b feat-c <path> <local-sha>` in a de-borrowed
store fails with `fatal: invalid reference: <sha>`, while the same command in a
`--shared` store succeeds. Since "branch this task from what I am working on
right now" is a normal request, the store carries a second remote pointing at the
workspace repository:

```
git -C <store> remote add workspace <workspace>/<repo>
git -C <store> fetch workspace '+refs/heads/*:refs/remotes/ws/*'
```

`wkt new` fetches from `workspace` before resolving a base that is not already
present. Verified: after that fetch the local-only commit is available in the
store. `origin` remains the real remote and is the only thing `wkt push` ever
writes to.

**De-borrowing is the default, and it is nearly free.** `--shared` makes the
store *borrow* objects through `objects/info/alternates`, so a borrowed store
does not survive the workspace repository being deleted or re-cloned — only a
workspace `gc`, and only with the pin. Measured on a 60-commit fixture: borrowed
store 88K, de-borrowed store 128K, `git repack -a -d` took 0.06s, and after
deleting the source repository the de-borrowed store still logged normally while
the borrowed one failed with `unable to normalize alternate object path`. The
cost scales with repository size rather than staying at 40K, so `wkt init`
reports the projected store size and a threshold makes borrowing the fallback for
very large repositories — but the default is the durable one, because the failure
mode of the cheap option is silent, unrecoverable, and lands on a user who
deleted a directory they were entitled to think was theirs.

**Sync.** With step 4 in place, `git -C <store> fetch origin` populates
`refs/remotes/origin/*`, which makes both `wkt sync` and the unpushed-commit
check work. `wkt sync <task>` fetches in every store of the set and reports how
far each repository's base has drifted; advancing the base is the user's call and
is done for the whole set together, never per repository.

**Accepted cost.** The task branch lives in the store, so it does not appear in
the developer's `git branch` until fetched back. `wkt status` states this, and
`wkt fetch <task>` brings it into the workspace repositories on demand —
refusing, never forcing, when the workspace already has a diverged ref of that
name (§6).

### 5.3 Tree layout

```
<container>/trees/feat-42/
  services/svc-a/    worktree from store, branch feat-42     writable
  docs/              worktree from store, branch feat-42     writable
  services/svc-b  -> <workspace>/services/svc-b              not in the task
  shared          -> <workspace>/shared                      not in the task
  notes           -> <workspace>/notes                       non-git directory
  CONVENTIONS.md     copy (hash-recorded, reconciled at teardown)
  .wkt/task.json     read-only mirror of state/tasks/feat-42.json
  .claude/settings.json  generated perimeter, self-protecting (H13)
```

Rules:

1. **Mirror the shape.** `services/svc-a` lands at `services/svc-a`. Never
   flattened, no option to flatten.
2. **Directories on the path to a selected repo are materialised**, not
   symlinked — `services/` is a real directory. Only leaves are linked.
3. **Un-materialised repositories are symlinked at their mirrored positions**,
   with **absolute** targets. Without this, mirroring delivers nothing under a
   partial repo set: `../../shared` from `services/svc-a` points at a hole.
   Verified: with the symlink in place the reference resolves, and this works
   *only* under mirroring.
4. **Two different scans, two different bounds.** Repository *enumeration* runs
   to a configurable depth (default 4) and does **not** follow symlinks. The
   *nested-repo and foreign-`.git` scans* are unbounded in depth, also without
   following symlinks, and run at `new` and at `rm`. Every symlink target that
   `wkt` is about to create is separately resolved and rejected if it contains a
   repository at any depth — that, not depth-unboundedness, is what prevents a
   hidden repo being shared by every task.
5. **Loose files are copied**, with the content hash recorded. Teardown refuses
   if a copy diverged (§5.7). Link slots are `lstat`-checked at teardown; a slot
   that is no longer a symlink blocks removal (H12).
6. **`.git` markers are classified, not refused.** A `gitdir:` file resolving to
   `<parent>/.git/worktrees/*` is a linked worktree — skipped, never treated as a
   repository (otherwise `init` fails on any workspace where `claude --worktree`
   has ever run). A submodule is recorded as part of its superproject. A genuine
   repository nested inside another repository is refused by name, with
   `wkt init --exclude <path>` as the escape hatch, recorded in container state.

Why mirroring, precisely: flattening preserves a cross-repo reference A→B **iff
`dirname(A) == dirname(B)`**. For a workspace whose repositories are all
top-level, flat is a no-op; for `services/svc-a` → `shared` it silently breaks.
Two unconditional wins flat cannot match: basename collisions (`services/api`
and `tools/api`), and back-filled un-materialised repositories.

### 5.4 State

`state/tasks/<task>.json` is **authoritative and versioned**. Per repository:
workspace-relative path, canonical absolute path, `<repo-id>`, branch name, base
SHA, base ref name, worktree path, **store worktree registration name** (git
derives it from the leaf basename and silently disambiguates with a numeric
suffix — `repair` cannot work without it), and the `refs/wkt/base` pin written.

Per task: schema version, the base epoch (an RFC3339 instant recorded at create —
the wall-clock moment the whole set was resolved), link slots with type and
absolute target, copied loose files with content hashes, **perimeter coverage —
the list of directories a perimeter copy exists in, not merely its hash** (H6a
makes coverage the question a user will actually ask), the emitted sandbox deny
path count against the configured cap (H17), container and workspace canonical
paths with every known spelling.

"Hint only, never authoritative" was the original position and it was wrong:
every unrecoverable failure found in review was unrecoverable because nothing
authoritative was recorded, and git's own metadata is destroyed by exactly the
operations `wkt` must survive.

**But:** anything that deletes still enumerates the filesystem. State says what
should be there; the disk says what is there; `wkt status` reports disagreement
rather than trusting either. The copy at `<tree>/.wkt/task.json` is a read-only
mirror for tools running inside the tree, and is deny-listed in §5.6.

### 5.5 Branch model

The branch name is the default task label, **not** the task key. State holds
`task → {repo: branch}`, which allows per-repo suffixes when a name is taken.

Create is two-phase:

1. **Resolve and validate across the whole set** before touching anything: base
   per repository (`origin/HEAD` → `init.defaultBranch` → current HEAD, never a
   hardcoded `main`), branch existence locally and on the remote, ancestry
   against the base, `worktree list` occupancy, D/F ref conflicts, case-fold
   collisions checked on every platform, `git check-ref-format`.
2. **Execute**, rolling back everything created on any failure.

Never silently reuse an existing branch. Never surface raw git errors — they
contain absolute paths belonging to other tasks.

### 5.6 Perimeter

Generated per §0: defence in depth against accidents, not a boundary.

The two halves are not symmetrical.

**Bash — whitelist-shaped already.** With `sandbox.enabled`, writes are confined
to the working directory and the session temp directory by default, so sibling
trees, the workspace and the home directory are closed without listing anything.
The store is *outside* the working directory and must be reopened explicitly.

**File-editing tools — deny only.** `Edit(…)` rules cover every file-editing
tool, but a `deny` beats a narrower `allow`, so there is no allow-list form; the
generator emits an explicit deny list, regenerated on every session start.

```json
{
  "permissions": {
    "deny": [
      "Edit(//<workspace>/**)",
      "Edit(//<container>/state/**)",
      "Edit(//<container>/staging/**)",
      "Edit(//<container>/trees/<each-other-task>/**)",   /* permissions only — see below */
      "Edit(//<container>/store/*.git/hooks/**)",
      "Edit(//<container>/store/*.git/config)",
      "Edit(//<tree>/.claude/**)",
      "Edit(//<tree>/.wkt/**)"
    ]
  },
  "sandbox": {
    "enabled": true,
    "filesystem": {
      "allowWrite": ["<container>/store"],
      "denyWrite":  ["<workspace>", "<container>/state", "<container>/staging",
                     "<container>/store/*.git/hooks", "<container>/store/*.git/config",
                     "<tree>/.claude", "<tree>/.wkt"],
      "denyRead":   ["~/.ssh", "~/.aws", "~/.config/gh", "~/.claude"]
    },
    "credentials": { "envVars": [ /* deny the obvious token variables */ ] }
  }
}
```

`allowWrite` on the store is **mandatory** — the task's gitdir lives there, and
denying it breaks `git add` exactly as H5 describes. The narrower `denyWrite`
entries for the store's `hooks/` and `config` still win over the broader allow,
which is the behaviour we want; `core.hooksPath` is disarmed at store creation as
a second line.

Every path appears in **all known spellings** — as typed, `realpath`, and the
macOS `/private` form. Deny globs are lexical: an alias such as
`~/work -> /Volumes/Data/work` defeats a single spelling entirely.

**The two lists have different cost models, and this is load-bearing (H17).**
The `sandbox.filesystem` paths are compiled into a profile passed to *every*
Bash command through `exec`, so their count is bounded by `kern.argmax`; that
list must stay **constant-size** — seven paths in three spellings, never growing
with the number of tasks. Sibling trees are therefore absent from it, and they do
not need to be: sandboxed Bash writes are already confined to the working
directory by default, so a sibling tree is closed without being named.

The `permissions.deny` entries are evaluated inside Claude Code and are not
passed through `exec`, so enumerating sibling trees there is safe at any count.
That asymmetry is the only reason a per-task deny is affordable at all.

**Stated limitations**, all of which belong in the README:

- **The Bash half is advisory (H17).** After a refusal, Claude Code was observed
  re-running the command with `dangerouslyDisableSandbox: true` on its own
  initiative, and the write landed — 4 runs in 6. Nothing `wkt` generates
  prevents this.
- **A perimeter file covers only the directory it sits in (H6a).** `wkt` writes a
  copy at the tree root and at each materialised repository root; a session
  started deeper — `<tree>/services/svc-a/src` — has **no perimeter at all**.
  Verified: the same workspace write is refused from the repository root and
  succeeds one directory below it. Materialising a copy into every directory is
  unbounded and litters the user's repositories, so v0 documents the limit and
  `wkt status` reports which directories are covered.
- Sibling trees are enumerated by name in `permissions.deny`, not by a glob,
  because any glob wide enough to catch them catches the task's own tree — and
  the narrower-`allow`-inside-broader-`deny` escape does not work (H16). A tree
  created *after* this
  one is uncovered until the perimeter is regenerated. `wkt status` reports the
  drift; regenerating is the `WorktreeCreate` hook's job in v0 (the
  `wkt perimeter` command is v0.1, §9).
- The file lives at the tree root, but Claude Code reads settings from the
  session's cwd (H6a). A session started inside `<tree>/services/svc-a` does not
  see it. `wkt` therefore writes the perimeter at the tree root **and** into each
  materialised repository, and `wkt status` verifies all copies.
- Link slots point outside the tree. Writes *through* them are denied by the
  target rules, but the slot itself lives in writable space (H12).

`wkt status` hash-checks every perimeter copy and reports coverage and drift.
It never reports "isolated" — v0 makes no isolation claim at all (§9).

### 5.7 Teardown

v0 is **refuse-only**. `wkt rm` enumerates from the filesystem — a walk of real
directories that never descends link slots — and refuses while any of the
following exists, naming each:

- uncommitted or untracked content;
- ignored-but-precious content (`.env*`, credentials, report directories);
  regenerable paths (`dist/`, `node_modules/`, `.venv/`) are listed, not blocking
  — otherwise `--force` becomes reflexive and stops meaning anything;
- unpushed commits, checked against `refs/remotes/origin/*` (which exists only
  because of the refspec in §5.2), including the no-upstream and detached-HEAD
  cases;
- in-progress rebase / merge / cherry-pick / revert / bisect;
- **any submodule at all** — see below; this is a hard block, not a heuristic;
- any `.git` found **without following symlinks** whose `--git-common-dir`
  resolves inside the task tree — refuse even with `--force`; its history exists
  nowhere else;
- a link slot whose type changed, or a copied loose file whose hash diverged;
- a failed check of any kind — "cannot tell" is treated as "would lose work".

`--force` does not destroy in place: it `mv`s the whole tree into `staging/` in
one rename (the fence), then removes from staging. **Before the mv**, the store's
`gc.auto` is already 0 and every worktree is locked (§5.2), because a moved
worktree becomes immediately prunable and `git worktree prune` has no grace
period. Rollback is `mv` back followed by
`git -C <store> worktree repair <restored-path>` **per repository** — the path
argument is mandatory; bare `worktree repair` does not rediscover a moved tree.
If `staging/` would be a cross-device move, `wkt` **refuses** and says so.
Degrading the fence to a non-atomic per-repo sequence defeats the only thing the
fence is for: a still-running agent's cwd has to vanish in one rename, or the
agent keeps writing into a tree that is halfway deleted.

Removal is innermost-first, and every check for every repository completes before
anything is removed.

**Submodules need their own path, and the store does not save them.** Measured:
`git worktree remove` on a worktree containing a submodule refuses
unconditionally — `fatal: working trees containing submodules cannot be moved or
removed` — so the safe path cannot remove such a tree *at all*, clean or not. And
`--force` deletes the whole `<store>/<id>.git/worktrees/<name>/` directory, which
is where the submodule's object store lives
(`…/worktrees/<name>/modules/<path>`), so a commit made inside the submodule is
destroyed: after a forced removal it was reachable neither from the store nor
from the original submodule repository. Moving the gitdir into the store fixed
H5; it does **not** fix this.

Therefore: `wkt rm` blocks on any submodule and names it. The supported route is
to push or salvage the submodule's commits first, `git submodule deinit` it, and
then remove — and until that route is implemented and tested (v0.1), `wkt new`
warns when a selected repository contains submodules.

Salvage refs and quarantine are deferred to v0.2: a mechanism that restores
perfectly is worthless while it fires on the wrong condition.

### 5.8 Concurrency

The tool exists for two agents working at once, so the critical sections must be
named:

- **container lock** (`flock` on `<container>/.wkt.lock`) around any set-level
  create or remove, so two `wkt new` runs cannot interleave two-phase validation
  and execution (a textbook TOCTOU otherwise);
- **per-store lock** around teardown and anything gc-sensitive;
- **atomic state writes** — write to a temporary file in the same directory, then
  rename;
- `git worktree lock` on every worktree for its whole life, released only by
  `wkt rm`;
- stale-lock sweep by PID, with a `mkdir`-based fallback where `flock` is
  unavailable.

### 5.9 Configuration

File: `<workspace>/.wkt.toml`, plus `~/.config/wkt/config.toml`.
Precedence: flag > `WKT_*` environment variable > workspace file > user file >
built-in default. Keys, all optional: container location, discovery depth,
excluded paths, branch template, precious/regenerable path classifiers, perimeter
on/off, tested-git-version override. `wkt config --show` prints the effective
configuration with the origin of each value.

### 5.10 Errors

Every refusal is structured, because the primary consumer is an agent:

```json
{"code":"WKT_UNPUSHED","repo":"services/svc-a","path":"…",
 "expected":"all commits pushed","found":"3 commits ahead of origin/feat-42",
 "remedy":["wkt push feat-42","wkt rm feat-42 --force"]}
```

A stable code namespace (`WKT_NESTED_REPO`, `WKT_BRANCH_EXISTS`,
`WKT_BRANCH_DIVERGED`, `WKT_UNPUSHED`, `WKT_PRECIOUS_IGNORED`,
`WKT_LINK_SLOT_CHANGED`, `WKT_FOREIGN_REPO`, `WKT_LOCKED`, …), a documented exit
code per class, and `--json` on every command. Raw git stderr is never
propagated.

---

## 6. Commands (v0)

| Command | Contract |
|---|---|
| `wkt init` | Canonicalise the workspace; enumerate repositories to the configured depth without following symlinks; classify `.git` markers per §5.3 rule 6; refuse genuine nested repositories (`--exclude` to override); verify container writability; create the container. `--dry-run` reports before writing anything. |
| `wkt new <task> [--repos …] [--all]` | Two-phase create (§5.5). `--repos` takes workspace-relative paths; a bare basename is accepted only when unique. With neither flag the default is `--all`. Exits 2 if the task exists. Leaves nothing behind on failure. |
| `wkt add <task> <repo>` | Grafts at the task's recorded base epoch, not today's `origin/HEAD`. Refuses if the repository is already in the task, the task does not exist, the repository is not in the discovered set, or the base epoch SHA is no longer reachable. |
| `wkt path <task>` | Prints the absolute tree path, one line; non-zero if the task does not exist. (Required by the acceptance battery.) |
| `wkt status [<task>]` | Per repository: branch, base epoch and drift, dirty, ahead/behind, orphaned gitdir, link-slot integrity, perimeter coverage and hashes (no isolation verdict — §9). Exit 0 consistent, 3 drift, 4 container missing. `--json`. |
| `wkt sync <task>` | Fetches in every store of the set, reports base drift for the whole set. Does not advance the base by itself. |
| `wkt fetch <task>` | Brings task branches into the workspace repositories. Fast-forward only: refuses when the workspace ref exists and is not an ancestor, names both SHAs, offers `--as <name>`. Never a forcing refspec. |
| `wkt rm <task> [--force]` | §5.7. |
| ~~`wkt perimeter <task>`~~ | **v0.1.** Regenerating perimeter copies is only worth a command once the perimeter is worth claiming; see §9. |
| `wkt repair <task>` | Fixes gitdir back-pointers and link slots after a workspace move or a repository re-clone. Deterministic outcomes per case, specified in the battery. |
| `wkt doctor` | Reconciles state against disk, store registrations and workspace pins; `--fix`. Also the uninstall path: reports every `refs/wkt/*` written into the user's repositories. |
| `WorktreeCreate` hook | Idempotent, reattach-by-default, tolerant of payload shape (H14). Materialises the perimeter **before** returning, then emits the documented `hookSpecificOutput` JSON. No `WorktreeRemove` hook (H9). |

Deferred: `wkt run`, salvage/quarantine, `push`/`pr`, `adopt`, size-based
defaults, learned scope presets, plugin packaging, the `/wkt` skill.

`wkt run` is not merely deferred — as specified it cannot be built. Claude Code's
Bash tool dies on undocumented private paths inside an external sandbox; making
`~/.claude` writable hands the agent hooks, `CLAUDE.md` and a live OAuth token,
which is worse than no sandbox; and nested sandboxing is refused by the kernel,
with the agent's observed recovery being to disable its own sandbox.

---

## 7. Acceptance battery

Two batteries, because they need different things.

### 7.1 Mechanical battery — no credentials, no network, CI on macOS and Linux

Pointed at any implementation via `WT_CMD`, in the style of the starter pack. The
pack drives `create`, `cleanup` and `path`; `wkt` ships `create` and `cleanup` as
documented aliases for `new` and `rm`, and `WT_TASK_DIR_TEMPLATE` goes unused
because `wkt path` is authoritative.
Note that the starter pack's `is_worktree_of` helper — which asserts that the
tree's `--git-common-dir` equals the *workspace repository's* — is invalidated by
the store design and must be replaced with: the tree's `--git-common-dir`
resolves under `<container>/store/`, and the commit is reachable from that store.
Likewise its "reachable from the original repository" assertion becomes
"reachable from the store, and from the workspace repository after `wkt fetch`".
The battery also requires `wkt path`.

Tests 1–3 have a paired positive phase, and tests 8–10, 12, 13 and 15 **must
grow one before the battery is trusted** — as written, a tool whose `rm` always
refuses passes 12 and 13, and a tool whose `new` always fails passes 8, 9 and 10.

1. Ignored `.env` survives a plain `rm` (H1); then, once cleaned, `rm` succeeds
   and leaves no registration in the store.
2. Interactive rebase in progress blocks removal; then, once concluded, removal
   succeeds (H2).
3. Detached-HEAD commit blocks removal; then, once merged or pushed, succeeds (H2).
4. Symlink target survives teardown, including the trailing-slash path (H3).
5. A stash made with plain `git stash` in task A is untouched by task B's
   teardown (H4).
6. `git commit` succeeds inside the tree with the workspace fully write-denied
   (H5) — the single most important test.
7. After `new`, every store's `origin` equals the workspace repository's origin,
   and a push from the tree lands the branch on that remote. Without this an
   implementation that omits one line passes everything else while pushing
   nowhere.
8. Existing divergent branch in one repository aborts create, leaving nothing (H10).
9. Non-`main` default branch in one repository aborts create cleanly (H10).
10. D/F ref conflict and case-fold collision each abort create (H10).
11. `gc --prune=now` in the workspace does not break a live tree (H11).
12. An atomic-save that severs a link slot blocks removal (H12).
13. A diverged copied loose file blocks removal (§5.3 rule 5).
14. Partial repo set: `../../shared` resolves from `services/svc-a` through the
    back-filled symlink.
15. A repository deleted from the workspace: `repair` exits non-zero with
    `WKT_STORE_ORPHANED` and destroys nothing; a repository *moved*: `repair`
    exits 0 and the tree's HEAD is unchanged.
16. Concurrency: two `wkt new` runs on overlapping repository sets started
    simultaneously — exactly one wins per repository, the loser exits non-zero
    and leaves nothing.
17. Interrupted create (`kill -9` mid-run) leaves nothing behind, and `doctor`
    reports a clean container.
18. A workspace containing a pre-existing linked worktree (`.git` as a file) is
    discovered correctly and not treated as a repository.
19. Flat-workspace control — the whole suite against a flat layout, green from
    the first commit.
20m. **Two tasks, one repository, one store.** Both worktrees are created; a
    commit in task A is invisible in task B; the workspace stays on its own
    branch and clean; removing A leaves B working and A's commit reachable in
    the store. Verified by hand during design — this is the tool's core promise
    and must not regress.
21m. **A repository with a submodule** blocks `wkt rm` by name, and `wkt new`
    warns at creation. Guards the measured fact that `worktree remove` refuses
    unconditionally on such a tree while `--force` destroys the submodule's
    objects.
22m. **A task branched from an unpushed local commit** in the workspace
    succeeds, proving the `workspace` remote is wired (§5.2). Without it the
    de-borrowed store fails with `invalid reference`.

### 7.2 Perimeter battery — v0.1, not v0

Not part of v0 (§9). Retained here as the v0.1 plan and as the procedure for
re-verifying the hazard register against a new Claude Code release. Requires a
session, therefore credentials and a network; gated, pinned to a version range,
re-run per release:

20. A write into the workspace is refused from the tree root and from a
    materialised repository root; **and the same write from
    `<tree>/<repo>/<subdir>` is recorded as uncovered** — a known-limitation
    test, not a pass/fail isolation test (H6a).
20b. A session whose cwd is a back-filled link slot (`<tree>/shared`) loads the
    task's instructions, not the workspace's (H6b).
21. Writing `../CLAUDE.md` does not reach the container, and a sibling task's
    session does not load it (H7).
22. A sibling tree is neither readable nor writable — including a sibling created
    after this tree, where `wkt status` must report drift and regeneration (hook
    in v0, `wkt perimeter` in v0.1) must close it (§5.6).
23. The agent cannot rewrite or delete any perimeter copy (H13).
24. H8 re-verification against the current release: what the vendor's own
    isolation covers, and what it leaves uncovered in a multi-repo workspace.
25. **The H16 composition test.** From a session at the tree root under the
    generated perimeter: `git add && git commit` in a materialised repository
    **succeeds**; a write to `<store>/<id>.git/hooks/<file>` is refused; a write
    elsewhere in `<container>/store` succeeds. This is the reason `allowWrite` on
    the store is safe. (Probe with a benign filename — a file literally named
    `post-commit` is refused by the model before it reaches the sandbox, which
    will confuse whoever re-runs this.)
26. **Fail-open budget (H17).** Assert on filesystem state after N repetitions of
    a denied write, with a stated flake budget — not on a single session's
    refusal, because the agent's sandbox-disabling retry makes refusals
    non-deterministic.
27. **Perimeter size cap (H17).** With M sibling tasks present, the emitted
    sandbox deny path count stays under the cap and Bash still runs; past the cap
    `wkt status` refuses to report "isolated" rather than emitting a profile that
    bricks Bash with `E2BIG`.

**Coverage map.** H1→1, H2→2/3, H3→4, H4→5, H5→6, H6a→20, H6b→20b, H7→21,
H8→24, H9→(observation; the hook cannot block by contract), H10→8/9/10,
H11→11, H12→12, H13→23, H14→(observation, pinned version range), H15→6/7,
H16→25, H17→26/27. Core-promise and store-remote coverage: 20m, 21m, 22m.

---

## 8. Form factor and distribution

Single repository containing the binary (Go, static, macOS + Linux), the
`WorktreeCreate` and session-start hook scripts, and a README carrying §0 and
§1.1 verbatim.

Go over Rust: single static binary, trivial cross-compilation, low contribution
barrier, and `os.RemoveAll` immunity to H3. The workload is filesystem walking,
shelling to git and JSON.

**Minimum git:** 2.29 (`worktree repair` landed in 2.29.0); tested on 2.50.1.
`wkt doctor` refuses on anything below the floor.

Plugin packaging and the `/wkt` skill are v0.1. Note that the Claude Code plugin
marketplaces already carry worktree plugins — including worktrunk's own — so the
channel is not empty; what is absent is a mirrored multi-repo workspace manager.

Distribution: GitHub Releases and `go install` at launch; Homebrew when the
notability gate is met — 30 forks / 30 watchers / 75 stars for a third-party
submission, 90 / 90 / 225 for a self-submission, and the repository must be at
least 30 days old.

Launch: post where traffic actually exists — `openai/codex#11956` (open, 75
reactions) and `anthropics/claude-code#23627` (open, 92 reactions). The other
issues cited in §2.4 (`#80442`, `#73824`, `#78505`, `#85448`,
`microsoft/vscode#318526`) carry 0–4 reactions each; they are evidence of the
failure mode, not distribution channels. Not `worktrunk#3501` either: closed,
self-filed and self-closed.

---

## 9. Plan

**v0 — 10–14 developer-weeks. The perimeter is out of v0. Decided.**

In scope: store with de-borrowed mirrors and base pins, mirrored tree, two-phase
create, refuse-only `rm`, `status`, `path`, `add`, `fetch`, `sync`, `repair`,
`doctor`, the `WorktreeCreate` hook, and the mechanical battery (§7.1).

The perimeter file is still generated — it costs little and prevents real
accidents — but there is **no `wkt perimeter` command, no session-gated battery
(§7.2) and no isolation claim in the README beyond §0's honest statement.** §7.2
is retained in this document as the v0.1 plan and as the re-verification
procedure for the hazard register.

**Why.** §4 says isolation is deliberately not the headline and §0 says the
perimeter only prevents accidents — yet §5.6, §7.2 and parts of §5.4 and §6
existed to serve it, and H17 shows it does not hold in the configuration that
matters. The same reasoning that killed `wkt run` in §6 — *the agent's recovery
is to disable its own sandbox* — applies to the Bash half of §5.6, and the
document did not notice for a full revision. Spending the largest engineering
budget on the explicitly de-prioritised feature was incoherent.

For reference, the version that kept it was estimated at 18–26 weeks, and §7.2
carries an ongoing per-release verification tax: Claude Code shipped 2.1.220 →
2.1.236 in under four weeks, and the hooks and sandbox surfaces are exactly what
changed.

The hazard register is more credible saying "here is where isolation breaks and
here is why we did not sell it" than shipping a perimeter command whose guarantee
has a 20-task ceiling.

Build order: `init` → store, two remotes and base pins → perimeter generator
(`new` writes the file, so it cannot come later) → `new` (two-phase) →
`status` → `rm` (refuse-only) → `add` → `fetch` → `sync` → `repair` →
`doctor` → hook. Keep the flat-workspace control green from the
first commit.

**Kill criterion.** If installs and stars are negligible 30 days after launch,
§4.1's second reading was right. Stop, and publish the battery and the hazard
register as a standalone artifact — that has value even if the tool does not.

---

## 10. Open questions

1. Branch namespacing: bare `<task>` or `wkt/<task>`. Namespacing removes D/F
   collisions but makes branches uglier in the forge.
2. Path-length policy (macOS `AF_UNIX` caps at 103 bytes; mirroring plus the
   container level can exceed it). A short exported `TMPDIR` is the likely
   mitigation.
3. Whether `wkt init` should offer to add the container to the workspace
   repositories' `.gitignore` files.
4. Whether the perimeter is opt-out.
5. Credential model for push and PR from inside a task: SSH agent forwarding, a
   scoped token, or an out-of-tree helper. H15 pushed to a local-path remote;
   a real `git@` origin is untested under the perimeter.
6. Which git config the store inherits from the workspace repository (`user.*`,
   `commit.gpgsign`, `gpg.*`, `core.sshCommand`, `url.*.insteadOf` — but
   explicitly not `core.hooksPath`).
7. **git-LFS through a bare store.** LFS objects live in `.git/lfs`, which is
   neither shared by alternates nor copied by `repack -a -d`. Could not be
   tested — `git-lfs` is not installed on the machine used for §3. For a tool
   aimed at multi-service workspaces this is a plausible day-one bug.
8. Licence and contribution policy before the first public commit.

---

## 11. Decision log

| # | Decision | Status after two adversarial passes |
|---|---|---|
| D1 | Trees outside the workspace | Kept; container configurable, guaranteed free of agent instruction files |
| D2 | Mirror the workspace shape, never flatten | **Survives**; strengthened by back-filling un-materialised repos |
| D3 | Non-git content symlinked | Changed: ancestors materialised, loose files copied and hash-reconciled, scans split by purpose |
| D4 | Atomic teardown, `--force` salvages | Changed: v0 refuse-only, staging fence with locks, salvage deferred |
| D5a | Perimeter via generated settings | **Descoped from v0** after H17 — the file is still generated, but no command, no gated battery and no isolation claim; §5.6's limits are documented instead |
| D5b | `wkt run` OS sandbox | **Killed** — not buildable as specified |
| D6 | Whole workspace readable | Changed: sibling trees and credential paths deny-read |
| D7 | Mutable repo set | Changed: base epoch defined as a recorded instant; size-defaults and presets cut |
| D8 | One branch name across repos | Changed: default label, not the key; per-repo mapping; two-phase create |
| D9 | Own engine | Kept; worktrees addressed by absolute path; `adopt` deferred |
| D10 | Go, macOS + Linux | Kept; git floor 2.30 stated |
| D11 | Binary + plugin + skill | Changed: binary + hooks in v0; no `WorktreeRemove` |
| D12 | `status` + `push` + PR | Changed: `status`/`sync`/`fetch` in v0; `push`/PR deferred |
| D13 | Isolation as the headline | **Inverted**: multi-repo shape leads |
| D14 | Bare mirrors in the store | Added — without it `commit` and the perimeter are mutually exclusive |
| D16 | Perimeter constant-size, coverage recorded | Added after H17 — sandbox deny paths are `exec`-bound and fail open past a ceiling |
| D15 | State authoritative, not a hint | **Reversed** from the first draft — every unrecoverable failure in review was unrecoverable because nothing authoritative was recorded |
