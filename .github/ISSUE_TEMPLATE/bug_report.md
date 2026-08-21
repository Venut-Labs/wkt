---
name: Bug report
about: Something wkt did that it should not have
labels: bug
---

## What happened

<!-- The command you ran and what it did. Paste the JSON error if there was one. -->

## What you expected

## Your workspace

Most defects here depend on the shape of the workspace, so this section is
usually the one that makes a report reproducible:

- How many repositories, and at what depths?
- Any nested repositories, submodules, or symlinked directories?
- Anything unusual in the tree: large loose files, a `.claude/settings.json` of
  your own, a repository below the discovery depth (default 4)?

## Versions

```
wkt version
git --version
```

OS and version:
