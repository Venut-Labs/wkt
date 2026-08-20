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
