# Defects found in the foundation plan while executing it

The plan at `2026-08-20-wkt-foundation.md` was written before any of its code had
run. Executing it surfaced the following defects **in the plan itself** — not in
the implementations, which transcribed it faithfully. Every one was reproduced
against a real binary or real git before being fixed.

**Status: ported.** Every listing in the plan now carries the code that actually
ran, each task states the traps that produced these entries, and the two spec
corrections at the bottom are applied. This file stays as the record of *what*
was wrong and why, which the plan itself does not preserve.

## Code the plan got wrong

| # | Task | Defect |
|---|---|---|
| 1 | 1 | Step order ran `go test` before `go mod init`, so the first test could not execute. |
| 2 | 2 | The literal `Canonical` fails the plan's own `IsUnder` test on macOS: `$TMPDIR` lives under `/var`, a symlink to `/private/var`, so an existing path fully resolves while a non-existent child does not, and `filepath.Rel` compares mismatched roots. Needs recursive parent resolution. |
| 3 | 3 | `fs.SkipDir` returned for a symlink entry skips the symlink's **siblings**, not its subtree — a symlink `DirEntry` reports `IsDir() == false`. Repositories sorting after a symlink vanished from discovery. |
| 4 | 3 | The same defect a second time, at the `.git`-as-file site in the same walk. |
| 5 | 3 | `classify`'s tail had two arms both returning `KindRepo`, spawning a git subprocess for nothing. |
| 6 | 3 | Depth comparison `>= maxDepth` discovered only `maxDepth - 1` directory segments. |
| 7 | 3 | Substring matching on the `gitdir:` target misclassifies a submodule whose own path contains a `worktrees` segment. The structural discriminator is a file named `gitdir` inside the admin directory. |
| 8 | 3 | `markNested` reported the outermost ancestor rather than the nearest, via mutate-while-iterating. |
| 9 | 4 | The test compared `c.Root` against a raw path while `Locate` canonicalises — unsatisfiable on macOS. |
| 10 | 4 | The fallback container can land **inside** the workspace when the workspace is `$HOME`, which is the spec's own primary trigger for the fallback. |
| 11 | 4 | `release` unlinked the lock file, so a party that opened the path before the unlink could flock an orphaned inode while a fresh `Lock` locked a new one — two holders of one lock. |
| 12 | 5 | The atomicity test asserted only that no `.tmp` file survived, which a naive direct write also satisfies. |
| 13 | 5 | `List` had no `IsDir` guard, so a directory named `something.json` was reported as a task. |
| 14 | 5 | `Load` never validated `SchemaVersion`. |
| 15 | 7 | `ancestors` was computed only from the materialised set, so a back-filled repository at depth ≥2 got its group directory created as a real directory, the later `os.Symlink` hit `EEXIST`, the error was swallowed — and a `LinkSlot{Type:"symlink"}` was recorded anyway. State misdescribed the disk. |
| 16 | 7 | `PlanFor` scanned only the workspace's immediate children, so non-repo content inside an ancestor directory silently vanished from the tree. |
| 17 | 7 | `copyFile` hardcoded `0o644`, dropping the execute bit from copied scripts. |
| 18 | 8 | The rollback used `worktree remove --force` (single), which fails on a **locked** worktree — and `Create` locks every worktree immediately after adding it. The rollback could not roll back. |
| 19 | 8 | The base pin is written by `store.Ensure` as its first action, but the undo was registered only after `Ensure` **and** the fetch block returned — so two ordinary failure paths leaked a ref into the user's own repository permanently. |
| 20 | 8 | `worktreeName` returned `""` on no match and the caller stored it unchecked, silently breaking the repair feature its own comment says depends on it. |
| 21 | 9 | `isPrecious` matched five substrings against a basename, so a gitignored `server.key` was deleted with no `--force`; and a bulk-ignored directory collapses to one status line, hiding everything inside it. The allowlist had to be inverted to known-regenerable. |
| 22 | 10 | `return fail(stderr, err) | 2` — a bitwise OR where an exit code was meant. |
| 23 | 10 | Exit 4 was unreachable for the ordinary "init was never run" case, though the spec's command table promises it and the battery drives it. |
| 24 | 10 | `wkt init` on a nonexistent or repo-free directory succeeded silently, so a typo in `--workspace` looked like success. |
| 25 | 10 | Positional-before-flags parsing discarded the task name and every later flag when a flag came first. |

## Tests the plan wrote that could not fail

| # | Task | Defect |
|---|---|---|
| 26 | 1 | The stderr-truncation test used a command emitting one line of stderr, so it passed whether or not truncation happened. |
| 27 | 1 | `TestVersionMeetsFloor` skipped rather than failed when the parser returned zeros. |
| 28 | 3 | Both discovery tests were blind to total non-discovery, because `Kind`'s zero value **is** `KindRepo`, so `found[key] != KindRepo` is false for a missing key. |
| 29 | 8 | The rollback test never reached phase two at all — validation rejected the batch first, so the undo stack was never exercised. |
| 30 | 11 | The `.env` test planted an unpushed commit alongside it, so the refusal came from the commit and the test passed with the precious-file check deleted entirely. |
| 31 | 11 | The battery had no observer for the staging fence: deleting the rename-before-delete step left 30/30 green. |
| 32 | 11 | Every battery scenario used `--all`, so the back-fill — the product's main differentiator — had zero coverage. |

## Placeholders the plan shipped

`var _ = fmt.Sprintf`, `var _ = strings.TrimSpace`, `var _ = os.Exit` — three
imports propped up rather than removed, in three different files. The plan's
global constraints now forbid the shape outright.

## Spec corrections the execution forced (applied)

- §5.7 promises the staging fence "degrades to a per-repo sequence" across
  filesystems. Degrading a fence to a non-atomic sequence defeats its purpose;
  the implementation refuses instead, and the spec should say so.
- §5.3 rule 4's symlink-target check was omitted from the plan entirely. A
  repository below the discovery bound was consequently shared writable by every
  task until the final review caught it. The spec was already right; the plan now
  carries the rule in its global constraints and in task 7.

---

# Findings from the adversarial pass after the merge

The port above closed the defects that *executing* the plan surfaced. This
second pass attacked the merged product instead: 15 mutations against the test
suite, and live experiments against real `git`, a real second filesystem and
real submodules.

## What held

Every one of the 15 valid mutations was caught by a test — staging fence,
back-fill, the regenerable allowlist, three fail-closed checks, the exit-code
map, both undo orderings, the execute bit, the depth bound, `SkipDir` on a
symlink, the structural `gitdir` discriminator, `SchemaVersion` validation, the
fallback-container refusal, the lock-file unlink, the `FlagSet`-derived flag
split, and the symlink-target repository scan. Two mutations appeared to survive
until the patches themselves turned out to be incomplete, not the tests blind.

Verified against reality rather than argued:

- A submodule's admin directory has **no** `gitdir` file; a linked worktree's
  does (git 2.50). The structural discriminator is sound.
- H3 holds: writing through a back-fill link and then `rm --force` leaves the
  workspace untouched — the deletion never follows a symlink.
- The cross-device refusal was driven with an actual second APFS volume: it
  refuses, the tree survives, `staging/` stays empty, the task stays usable.
  This is what §5.7 now promises, and it is now a reproduced fact.
- Path traversal (`../escape`, `a/../../escape`) is refused; two concurrent
  `wkt new` runs cannot interleave; the base pin is written and removed without
  residue; `--dry-run` writes nothing; spaces and non-ASCII in repository names
  work.

## Defects found (fixed)

| # | Defect | Fix |
|---|---|---|
| F1 | `feature/x` — a valid *branch* name — passed validation, the tree was built, the state write failed on a missing directory, the rollback left an empty `trees/feature`, and that debris blocked the plain name `feature` forever. `WKT_TREE_EXISTS` recommended `wkt rm feature`, which answers `WKT_NO_TASK`: a dead end. | Refuse a task name that is not one path segment, in phase one; make the leftover-directory remedy name the directory. |
| F3 | Spec §5.7 requires `wkt new` to warn when a selected repository carries a submodule, because `rm` refuses on one **even with `--force`**. The warning was never implemented, so such a task was created silently and could then not be removed by any wkt command. | `task.SubmoduleWarnings`, printed by `new` on stderr before anything is created. |
| F2 | Spec §1 promised the workspace "reachable **read-only**", a guarantee only the perimeter provides — and the perimeter was descoped from v0. Verified by writing through a back-fill link into the workspace. | §1 now says "reachable in place", with the v0 limitation stated in §1.1 and the README. The mechanism itself is v0.1. |
| F4 | `wkt init --exclude <path>` was promised twice (§5.3 rule 6, §7.1) and existed nowhere, so a workspace containing a genuine nested repository could not be adopted at all. | Implemented, cumulative across runs via `state/container.json`, refusing a path that is not nested. Battery scenario 10 covers it. |
| F5 | Every blocker was rendered into the error's `remedy` field as `Code Repo Path Detail`, so "what to do" listed what was wrong, with an empty path and raw `git submodule status` output inside it. | `wkterr.Problem` (zero value blocks, so callers fail closed) carries the blockers; `remedy` now names actions per blocking code. `WKT_DIRTY` and `WKT_SUBMODULE` report prose. |
| F8 | The plan's interface list promised a "stale-PID sweep" on `container.Lock` that does not exist and is not needed. | Corrected: the kernel drops an `flock` when its holder dies; the PID is written only so a refusal can name the holder. |
| F9 | The plan's "Consumes" lines disagreed with the real import graph in three tasks. | Corrected against the actual imports, per file for the two tasks that share the `task` package. |

## Defects found (open)

| # | Defect | Why it matters |
|---|---|---|
| F6 | Every verb shares one `FlagSet`: `wkt init --force --repos zzz` and `wkt path t --force --all` are accepted silently. | Same class as defect 24 above — input that means nothing is taken as success. |
| F7 | `container.Lock` is `LOCK_NB` with no wait or retry, so two agents running `wkt new` at the same time make one of them fail with `WKT_LOCKED`. | The spec's own premise is two agents working at once. Correct, but abrasive. |

## One defect this pass introduced, and caught

The first `describePorcelain` ran `TrimSpace` over the whole `git status
--porcelain` blob, which shifts every path one column left; combined with
`gitx.Run` already returning trimmed stdout, it reported `1 changed: .txt`
for a file named `f.txt`. The test that was supposed to guard it only
asserted "not porcelain", which an empty string satisfies. Both the helper
and the test were fixed, and the helper now has a table test of its own —
the same lesson as defects 26 through 32 above, one round later.

