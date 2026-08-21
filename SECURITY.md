# Security

## What wkt does and does not defend

wkt generates a perimeter file for each task tree: a settings document that
denies writes to the workspace, to other tasks' trees, and to its own
bookkeeping. **This is defence in depth against accidents, not a security
boundary**, and the README says so too:

- A session started below a covered directory has no perimeter at all.
- The Bash half is advisory. An agent that is determined to write somewhere can
  disable its own sandbox; this was observed on one release, and did not
  reproduce on the next.
- A task tree can push to the repository's real origin.

If your threat model includes an adversarial agent, wkt is not the tool for it.

## Reporting a vulnerability

Open a private security advisory through GitHub
(**Security → Report a vulnerability**) rather than a public issue.

Two classes of report are especially welcome, because they are the ones that
cost users work rather than embarrassment:

1. **A path where wkt deletes something it did not create** — a walker that
   follows a link out of the tree, a computed path that escapes the container.
2. **A path where a refusal can be bypassed** — teardown removing a tree that
   holds unpushed commits, uncommitted work, or a repository whose history
   exists nowhere else.
