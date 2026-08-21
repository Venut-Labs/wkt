# wkt v0.1 Implementation Plan — perimeter, hooks, doctor

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `wkt` generates the perimeter it has been promising, reports honestly
which directories that perimeter covers, reconciles state against disk with
`wkt doctor`, and plugs into Claude Code's worktree hooks so that
`claude --worktree` in a multi-repo workspace produces a wkt task tree.

**Architecture:** Three additions on top of the v0 foundation. A new
`internal/perimeter` package renders a settings document from a task's state and
writes it into the tree root and every materialised repository, recording each
copy's hash in state. `wkt perimeter`, `wkt status` and `wkt doctor` read those
hashes back. A `wkt hook …` verb group implements Claude Code's `WorktreeCreate`
and `WorktreeRemove` contracts by delegating to `wkt new` and `wkt rm`.

**Tech Stack:** Go 1.26 (stdlib only), `git` ≥ 2.29, `bash` for the battery.
Claude Code **2.1.238** is the pinned verification target.

**Spec:** `docs/superpowers/specs/2026-08-19-wkt-design.md` — §5.6 (perimeter),
§6 (command table), §7.2 (session-gated battery), §9 (why the perimeter was cut
from v0), and hazards H6a, H9, H13, H14, H16, H17.

**Predecessor:** `2026-08-20-wkt-foundation.md`, and the defects both executing
it and running it surfaced: `2026-08-20-wkt-foundation-plan-defects.md`. Read
that file before this one. It is the reason this plan is shaped the way it is.

---

## Why this plan is shaped differently

The foundation plan carried a complete implementation as listings, written
before any of it had run. Executing it surfaced **25 defects in that code and 7
tests that could not fail** — not transcription errors, but wrong answers
confidently written down. The listings have since been replaced with the code
that actually ran, which makes that plan a good *record* and a bad *template*.

So this plan does not pre-write the implementation. It fixes what is
load-bearing — interfaces, invariants, the exact cases each test must
distinguish, and the traps already paid for — and leaves the code to the
worker, who will have a compiler and a real Claude Code in front of them.

Two consequences, both mandatory:

- **Every task ends with a mutation check.** Delete or invert the check the test
  is supposed to guard, confirm the test goes red, restore it. Seven tests in
  the foundation passed against code with the guard removed entirely; a green
  suite is not evidence until a broken build makes it red.
- **No claim enters this plan unverified.** Task 1 exists because the last
  verification ran against Claude Code 2.1.220 and the installed release is
  2.1.238. Anything Task 1 disproves changes the tasks below it — that is the
  intended outcome, not a failure of the plan.

---

## Global Constraints

Everything the foundation plan constrained still holds (stdlib only, macOS and
Linux, git floor 2.29, never shell out to delete, never surface raw git output,
anything that deletes enumerates the filesystem, every path in all known
spellings, fail closed). Additionally:

- **No isolation claim, anywhere.** Not in the README, not in `status` output,
  not in an error message. The perimeter prevents accidents (spec §0). `status`
  reports *coverage*, never "isolated" (§5.6, §9).
- **One deny list covers both halves, and it is bounded.** Task 1 measured this
  (`2026-08-21-hazard-reverification.md`): `Edit(...)` deny rules are merged into
  the Bash sandbox profile, so they need not be restated under
  `sandbox.filesystem` — but every rule is compiled into the profile passed to
  *every* command. It works at ~5,000 paths; at ~9,000 the profile stops
  compiling and **all** Bash fails; at ~20,000 the spawn fails with `E2BIG`.
  Both failure modes are fail-closed, and the generator must keep the total
  proportional to the number of tasks (roughly 3 paths per sibling), never to
  anything unbounded. `sandbox.filesystem` still carries `allowWrite` on the
  store (H5) and `denyRead` on credential directories.
- **The Bash half may still be advisory, and must never be sold as a boundary.**
  On 2.1.220 the agent re-ran a refused command with
  `dangerouslyDisableSandbox: true` and the write landed, 4 runs in 6. On
  2.1.238 that did not reproduce in 7 headless runs (Task 1) — but not
  reproduced is not fixed, and interactive sessions were not tested.
- **A perimeter file covers only the directory it sits in** (H6a). Copies go to
  the tree root and to each materialised repository root. A session started
  deeper is uncovered, and `status` must say so rather than implying otherwise.
- **Every generated file is written atomically** — temp file in the same
  directory, then rename — and its hash recorded in state, exactly as
  `state.Save` already does.
- **Hook payloads are unstable** (H14): five keys on the `--worktree` startup
  path, six on the `EnterWorktree` path, differing by entry point. Parse
  defensively, tolerate unknown fields, never require a field the docs do not
  guarantee, and never read `transcript_path` — on the startup path it names a
  file that does not exist yet.

---

### Task 1: Re-verify the hazard register against Claude Code 2.1.238 — **DONE 2026-08-21**

**Result:** `docs/superpowers/specs/2026-08-21-hazard-reverification.md`. H6a,
H13 and H16 confirmed; H17 not reproduced in 7 runs; one load-bearing §5.6 claim
disproved (deny rules *are* merged into the Bash profile) and the spec amended.
The tasks below already reflect it.

**Why first:** every design decision below rests on behaviour last measured on
2.1.220. Spec §9 warns that this verification is a recurring tax and that "the
hooks and sandbox surfaces are exactly what changed". The installed release is
already two versions on.

**Files:**
- Create: `docs/superpowers/specs/2026-08-21-hazard-reverification.md`

**Interfaces:**
- Consumes: a scratch workspace, the installed `claude` binary, the built `wkt`.
- Produces: a dated record — one section per hazard, each stating the command
  run, the observed result verbatim, and whether the spec's claim survives.

**Traps:**

- Do not run these against a real workspace. Build a throwaway one; several
  cases deliberately provoke writes that must be refused.
- Record the exact version (`claude --version`) at the top. A result without a
  version is worthless three weeks from now.
- A hazard that cannot be reproduced is **not** thereby disproved — record
  "could not reproduce", not "fixed". H9 in particular fires only on one entry
  path.

- [x] **Step 1: Confirm the hook contract as shipped**

The installed binary documents `WorktreeCreate` as: input JSON carrying `name`
(a suggested slug), stdout carrying the absolute path to the created worktree,
exit 0 for success. `WorktreeRemove` takes `worktree_path` and falls back to
`git worktree remove` when no hook is configured. Confirm both against
2.1.238 — including that a **command** hook may print the path on stdout rather
than emitting `hookSpecificOutput`, which is what makes `wkt new`'s existing
output already contract-shaped — and record whether the payload still matches
H14's description.

- [x] **Step 2: Re-run H6a — the perimeter covers only its own directory**

With a perimeter file at a repository root, attempt the same denied write from
that root and from one directory below it. H6a claims the first is refused and
the second succeeds. Record both.

- [x] **Step 3: Re-run H16 — a narrower allow does not escape a broader deny**

This is what forces sibling trees to be enumerated by name rather than caught by
a glob. If it no longer holds, Task 3's deny list gets much smaller and the
20-task ceiling in §9 disappears — a result worth knowing before writing it.

- [x] **Step 4: Re-run H13 and H17**

H13: the agent cannot rewrite or delete a perimeter copy protected by
`sandbox.filesystem.denyWrite` — the foundation spec records this as verified
against the Write tool, a Bash redirect and `rm -f`. H17: after a refusal, does
the agent still retry with `dangerouslyDisableSandbox`? Run it enough times to
report a ratio, as H17 itself does, not a single anecdote.

- [x] **Step 5: Record and commit**

Write the findings file. Where a result contradicts the spec, amend the spec in
the same commit and say so in the message — a hazard register that disagrees
with its own spec is worse than neither.

---

### Task 2: `internal/perimeter` — render the document — **DONE 2026-08-21**

**Result:** `internal/perimeter`, 9 tests. The mutation check found two of them
blind and both were fixed before this was called done: the own-tree guard was
never reached because the fixture omitted the task's own name from the sibling
list, and the rule-spelling test needed a mutation that actually drops the `//`
prefix rather than one that merely moves a slash. Verified on 2.1.238 first:
`Edit(//Users/x/f)` and `Edit(///Users/x/f)` both deny, `Edit(/Users/x/f)` is
accepted and does nothing.

**Files:**
- Create: `internal/perimeter/perimeter.go`
- Test: `internal/perimeter/perimeter_test.go`

**Interfaces:**
- Consumes: `container`, `paths`, `state`, `wkterr`.
- Produces:
  - `perimeter.Document` — the settings object, marshalled with stable key and
    slice ordering so an unchanged task renders byte-identical every time.
  - `perimeter.For(c container.C, t state.Task, siblings []string) (Document, error)`
    — builds the document for one task, given the other task names.
  - `perimeter.Render(d Document) ([]byte, error)` — deterministic JSON.

**Requirements the tests must distinguish** (spec §5.6):

- `permissions.deny` covers the workspace, `state/`, `staging/`, every sibling
  tree by name, the store's `hooks/` and `config`, and the task's own `.claude/`
  and `.wkt/`.
- `sandbox.filesystem.allowWrite` **must** include the store: the task's gitdir
  lives there and denying it breaks `git add` (H5). The narrower deny entries for
  `hooks/` and `config` still win — that composition is the point, and H16 was
  re-confirmed in Task 1, so it is deny that wins, never the narrower allow.
- Deny paths are **not** duplicated under `sandbox.filesystem.denyWrite`: Task 1
  verified they are merged from the `Edit(...)` rules already. Duplicating them
  doubles the profile that every Bash command carries, for nothing.
- Every path appears in all known spellings (`paths.Spellings`), because deny
  globs are lexical and an aliased workspace defeats a single spelling.
- The total path count grows only with the number of siblings, about three per
  sibling. A test must render with 1 sibling and with 40 and assert the growth
  is linear and small — and a second test must assert the document stays under a
  hard cap (say 2,000 paths) or refuses with a typed error, because past ~9,000
  every Bash command in that session fails rather than degrading.
- `denyRead` covers `~/.ssh`, `~/.aws`, `~/.config/gh`, `~/.claude`.

**Traps:**

- Sorting is not cosmetic here: an unstable order makes every regeneration look
  like drift to Task 6's hash check.
- A test that only checks "the workspace appears somewhere" passes against a
  document that names it in the wrong section. Assert the section.
- The task's own tree must **not** appear in its own deny list — that is the
  mistake H16 exists to describe.

- [x] **Step 1: Write the failing tests, one per requirement above**
- [x] **Step 2: Run them; confirm each fails for its own reason, not a shared compile error**
- [x] **Step 3: Implement**
- [x] **Step 4: Mutation check** — drop the store from `allowWrite`, then the
      `/private` spelling, then the sibling enumeration. Each must turn a
      different test red. Restore.
- [x] **Step 5: Commit**

---

### Task 3: Write the copies, record the hashes — **DONE 2026-08-21**

**Result:** `internal/perimeter/write.go`, 6 tests. `Write` covers the tree root
and each materialised repository, checks every destination *before* writing any
of them, refuses a settings file wkt does not own (and refuses a symlink at that
path outright), and writes atomically. `Verify` reports missing and diverged
copies. All seven mutations were caught, including "copies go only to the tree
root", "coverage includes back-fill slots" and "the ownership check runs after
the write".

**Files:**
- Modify: `internal/perimeter/perimeter.go`
- Modify: `internal/state/state.go` (only if the existing fields prove
  insufficient — `Task.PerimeterCoverage` and `Task.PerimeterHashes` already
  exist and were designed for this)
- Test: `internal/perimeter/write_test.go`

**Interfaces:**
- Produces:
  - `perimeter.Write(c container.C, t state.Task, siblings []string) (coverage []string, hashes map[string]string, err error)`
    — writes `<tree>/.claude/settings.json` and
    `<tree>/<repo>/.claude/settings.json` for every materialised repository,
    atomically, returning what it covered and each copy's hash.
  - `perimeter.Verify(c container.C, t state.Task) ([]Divergence, error)` —
    which copies are missing, which have diverged.

**Requirements:**

- Coverage is the list of directories a session can start in and still be
  covered — the tree root and each materialised repository root. Nothing else
  may be listed as covered, because nothing else is (H6a).
- A repository that is back-filled (a symlink) gets **no** copy: writing there
  would write into the user's workspace.
- Writing must not clobber a `.claude/settings.json` the user put in a
  repository themselves. Detect it (no recorded hash, file present) and refuse
  with a typed error naming the file, rather than overwriting work.

**Traps:**

- The link-slot rule above is the one that loses data if wrong. Test it with a
  real back-filled repository, not a fixture that merely looks like one.
- `os.MkdirAll` on `<repo>/.claude` succeeds when the directory already exists;
  that is fine, but the *file* check must happen before the write, not after.

- [x] **Step 1: Write the failing tests**
- [x] **Step 2: Confirm they fail**
- [x] **Step 3: Implement**
- [x] **Step 4: Mutation check** — make `Write` follow a back-fill link; the
      workspace-write test must go red.
- [x] **Step 5: Commit**

---

### Task 4: `new` writes the perimeter — **DONE 2026-08-21**

**Result:** phase two writes the perimeter before the state that describes it,
and records coverage and hashes in the same state write. The copies live inside
the tree, so the tree's own undo — registered before the tree existed — already
removes them on rollback; no separate undo was added, and a test pins that.
Creating a task refreshes every sibling's perimeter, and a sibling that cannot
be refreshed is stale rather than fatal.

**Measured, not assumed** (the plan required a number): creating 20 tasks over
3 repositories, the 20th `new` took 0.50s against the 1st's 0.65s — sibling
regeneration is not visible at this scale. The 20th task's perimeter carries 66
deny rules in 10.9 KB, against the ~5,000-rule bound measured in Task 1.

**The collision this surfaced.** The perimeter is a file wkt writes *into* the
tree, so every teardown check immediately saw it as the user's uncommitted
work: `?? .claude/` in each repository and untracked content at the tree root.
`Preflight` now knows which files it owns — the per-repository status check runs
with `-uall` so an untracked directory does not collapse to one line, and the
tree walk descends into `.claude` instead of blocking on it. A mutation that
exempted the whole `.claude` directory rather than the single file survived the
suite, so `TestUserContentBesideThePerimeterStillBlocks` was added: a user's
`agents/reviewer.md` beside the perimeter must still block removal.

**Files:**
- Modify: `internal/task/create.go`
- Test: `internal/task/create_test.go`

**Requirements:**

- The perimeter is written in phase two, after the tree is materialised, and its
  coverage and hashes are recorded in the same state write. Spec §9 puts the
  generator before `new` in the build order precisely because "`new` writes the
  file, so it cannot come later".
- A failure to write the perimeter rolls the task back like any other phase-two
  failure. It does not leave a task with a half-written perimeter.
- Creating task B must regenerate task A's perimeter, or A stops covering the
  new sibling. If that regeneration fails, say so — but do **not** fail B's
  creation over it: B is correct, A is merely stale, and `status` reports stale.

**Traps:**

- The undo for the perimeter write must be registered before the write, exactly
  like the base pin and the branch delete (foundation defects 18 and 19).
- The "regenerate the siblings" step is where a 20-task workspace becomes
  quadratic. Measure it once at 20 tasks and record the number.

- [x] **Step 1: Write the failing tests** (creation writes it; rollback removes
      it; creating B refreshes A; A's failure does not fail B)
- [x] **Step 2: Confirm they fail**
- [x] **Step 3: Implement**
- [x] **Step 4: Mutation check** — 5 mutations, one survived and produced a new
      test before it was called done.
- [x] **Step 5: Commit**

---

### Task 5: `wkt perimeter [<task>] [--check]` — **DONE 2026-08-21**

**Result:** the verb regenerates every task, or one named task, and `--check`
reports without writing (exit 3 on drift, matching `status`). A task with no
recorded coverage reports `uncovered` rather than clean — that is exactly what
every task created before this feature looks like, and reporting it clean would
make the check useless where it is most needed.

**F6 decided: fixed, not deferred.** Each verb now builds its own flag set, so
`wkt path t --force`, `wkt init --repos a` and `wkt new t --check` exit 2
instead of being accepted in silence.

**A guard that blocked its own repair.** The first run of the new test hit
`WKT_PERIMETER_FOREIGN`: a task whose state had lost its hashes could not have
its perimeter regenerated, because `Write` mistook its own output for the user's
file. The document now carries a `"$wkt"` marker — verified on 2.1.238 that
Claude Code ignores unknown top-level keys and still enforces the deny rules —
so ownership is a property of the file rather than of state, which can be lost.
Adoption keys on the marker, never on the filename: an unmarked settings file is
still refused.

**Files:**
- Modify: `internal/cli/cli.go`
- Test: `internal/cli/cli_test.go`

**Requirements:**

- With no task name: regenerate every task's perimeter.
- `--check`: report without writing; exit 3 on drift, matching `status`'s
  contract. Exit 0 when everything matches.
- The verb appears in `usage`. A flag the tool recommends but never documents is
  a flag nobody finds (finding F4's lesson).

**Traps:**

- Per-command flag sets are still not implemented (finding F6): do not add
  `--check` to the shared set where `wkt path --check` would silently accept it.
  Either fix F6 here or scope the flag deliberately and say which.

- [x] **Step 1: Write the failing tests** — including that `--check` writes nothing
- [x] **Step 2: Confirm they fail**
- [x] **Step 3: Implement**
- [x] **Step 4: Mutation check** — 5 mutations; one survived twice and produced
      both a new test and the marker design before this was called done
- [x] **Step 5: Commit**

---

### Task 6: Coverage and drift in `status`

**Files:**
- Modify: `internal/cli/cli.go`, `internal/task/remove.go` (Preflight already
  walks every copy's neighbourhood)
- Test: `internal/cli/cli_test.go`

**Requirements:**

- Per task: which directories are covered, which copies are missing, which have
  diverged, and whether a sibling tree exists that the perimeter does not name.
- Drift in a perimeter copy is exit 3, like any other drift.
- The output must never use the word "isolated" (§5.6, §9). Add a test that
  greps the whole `status` output for it and fails — cheap, and it pins a
  promise the project has already broken once (finding F2).

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Confirm they fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Mutation check** — mark a diverged copy as clean; the drift test must go red
- [ ] **Step 5: Commit**

---

### Task 7: `wkt doctor [--fix]`

**Files:**
- Create: `internal/doctor/doctor.go`
- Test: `internal/doctor/doctor_test.go`
- Modify: `internal/cli/cli.go`

**Interfaces:**
- Produces: `doctor.Run(c container.C, fix bool) ([]Finding, error)`.

**Requirements** (spec §6):

- Reconcile state against disk: tasks with no tree, trees with no task (the
  debris finding F1 produced), stores with no task, worktree registrations in a
  store that point nowhere.
- Reconcile against the workspace: every `refs/wkt/*` written into the user's
  repositories. This is also the **uninstall path** — `doctor` must be able to
  answer "what has wkt put in my repositories" completely, because that answer
  is what makes the tool safe to try.
- `--fix` repairs only what is unambiguous: prune a store registration for a
  tree that does not exist, delete a base pin for a task that does not exist,
  remove an empty leftover tree directory. It never deletes anything that could
  hold work — that is `rm`'s job, with `rm`'s refusals.

**Traps:**

- `doctor --fix` is the second command in this codebase that deletes. Every
  constraint from §5.7 applies to it: enumerate from the filesystem, never
  follow a link, fail closed, and never `rm -rf`.
- A "leftover tree directory" is only safe to remove when it is **empty**. F1's
  debris was empty; a half-created tree is not.

- [ ] **Step 1: Write the failing tests, one per reconciliation case**
- [ ] **Step 2: Confirm they fail**
- [ ] **Step 3: Implement the read-only half; commit it before `--fix` exists**
- [ ] **Step 4: Implement `--fix`**
- [ ] **Step 5: Mutation check** — let `--fix` remove a non-empty directory; that test must go red
- [ ] **Step 6: Commit**

---

### Task 8: `wkt hook worktree-create|worktree-remove|session-start`

**Files:**
- Create: `internal/hook/hook.go`
- Test: `internal/hook/hook_test.go`
- Modify: `internal/cli/cli.go`

**Interfaces:**
- Produces:
  - `wkt hook worktree-create` — reads the payload JSON on stdin, takes `name`,
    creates (or reattaches to) the task, prints the tree's absolute path on
    stdout, exits 0.
  - `wkt hook worktree-remove` — reads `worktree_path`, maps it back to a task,
    removes it with `rm`'s refusals intact.
  - `wkt hook session-start` — regenerates the perimeter for the task whose tree
    contains the session's cwd. This is how a perimeter stays current, since
    `WorktreeCreate` fires only for the tree being created.
  - `wkt hook install` — prints the settings block to paste, with real absolute
    paths filled in. Printing beats writing: it is the user's `~/.claude/settings.json`.

**Requirements:**

- **Idempotent and reattach-by-default** (§6). `--resume --worktree` re-fires
  `WorktreeCreate` (H14); firing twice for the same name must return the same
  path, not fail with `WKT_TASK_EXISTS`.
- The suggested `name` is not a validated task name. Sanitise it to one path
  segment (finding F1's rule) and, if that changes it, still succeed — the hook
  contract has no channel for "I renamed your slug".
- The emitted path must contain no dot segments; the binary rejects those
  explicitly.
- `worktree-remove` must not be the tool's only removal path for a task: if it
  refuses (uncommitted work), the user still has `wkt rm`. Say so on stderr,
  which the binary shows to the user on a non-zero exit.
- Tolerate unknown payload fields; never read `transcript_path` (H14).

**Traps:**

- Anything printed on stdout other than the path breaks the contract. The
  submodule warning added by finding F3 goes to stderr — verify it still does
  under the hook, because a warning on stdout would be read as the worktree path.
- `worktree_path` may arrive in any spelling; map it back through
  `paths.Spellings`, not by string equality against `TreePath`.
- H9: `WorktreeRemove` does not fire on every removal path, so a hook-driven
  task can still leak. `doctor` is the backstop, and this is why Task 7 comes
  first.

- [ ] **Step 1: Write the failing tests** — payload parsing (both shapes), the
      stdout contract, idempotent re-fire, slug sanitisation, spelling-tolerant
      reverse lookup
- [ ] **Step 2: Confirm they fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Drive it end to end with the real binary** — configure the hook
      in a scratch `~/.claude/settings.json` copy, run `claude --worktree`
      against a scratch multi-repo workspace, confirm the session lands in a wkt
      tree. This is the task's actual acceptance criterion; the unit tests only
      guard the parts that do not need a live agent.
- [ ] **Step 5: Mutation check** — print an extra line on stdout before the
      path; the contract test must go red
- [ ] **Step 6: Commit**

---

### Task 9: Battery scenarios 12–16

**Files:**
- Create: `test/12_perimeter_written.sh`, `test/13_perimeter_coverage.sh`,
  `test/14_perimeter_siblings.sh`, `test/15_doctor.sh`, `test/16_hook_contract.sh`

**Requirements:**

- 12: `new` writes a perimeter at the tree root and in each materialised
  repository, and **not** through a back-fill link.
- 13: `status` reports coverage, and reports drift after a copy is edited.
- 14: creating a second task refreshes the first's deny list; `wkt perimeter`
  regenerates; `--check` reports without writing.
- 15: `doctor` finds a leftover tree, an orphaned store registration and a
  stray base pin; `--fix` clears exactly those and nothing else.
- 16: `wkt hook worktree-create` emits one line, that line is a directory, and
  re-firing with the same name emits the same line.

**Traps:**

- Scenario 16 must assert `wc -l = 1` on stdout. The whole contract is that one
  line, and every warning the tool grows in future is a chance to break it.
- Do not drive everything with `--all` (foundation defect 32): scenario 12 needs
  a partial selection, or the back-fill rule it exists to check is never exercised.

- [ ] **Step 1: Write the scenarios**
- [ ] **Step 2: Run the whole battery**
- [ ] **Step 3: Mutation check** — for each scenario, break the behaviour it
      guards and confirm that scenario, and only that scenario, fails
- [ ] **Step 4: Commit**

---

### Task 10: Documentation — say exactly what this buys

**Files:**
- Modify: `README.md`, `docs/superpowers/specs/2026-08-19-wkt-design.md`

**Requirements:**

- README: the perimeter section states that the Edit half is enforced by Claude
  Code, that the Bash half is advisory because the agent can disable its own
  sandbox (H17, with the observed ratio), and that a session started below a
  covered directory has no perimeter (H6a). No isolation claim.
- Spec §1.1's "No read-only workspace in v0" entry becomes accurate for v0.1:
  writes through a link slot are denied by the target rules **once a covered
  session is in force**, and not otherwise.
- Spec §6: `wkt perimeter` moves out of the struck-through row. The
  `WorktreeCreate` row gains the contract as verified in Task 1.

- [ ] **Step 1: Update both, using Task 1's recorded results as the source**
- [ ] **Step 2: Re-read every isolation-adjacent sentence in both files and check it against what now ships**
- [ ] **Step 3: Commit**

---

## What this plan deliberately leaves out

`add`, `fetch`, `sync` and `repair` — the remaining v0 command table — are the
next plan. They are mechanical next to this one and depend on nothing here.

Findings F6 (per-verb flag sets), F7 (blocking container lock), L1 (a threshold
on copying loose files) and L4 (`init` warning about a repository below the
discovery bound) stay open and are recorded in the defects document. Task 5
touches F6 and must state what it decided.

Salvage refs, quarantine, `push`/`pr`, `adopt` and `wkt run` remain out of scope
entirely (spec §6, §9). `wkt run` is not merely deferred: as specified it cannot
be built.
