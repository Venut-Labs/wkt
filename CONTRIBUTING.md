# Contributing

## Running the tests

Two halves, and both matter.

```sh
go test ./...                                   # unit tests
go build -o /tmp/wkt ./cmd/wkt
WT_CMD=/tmp/wkt bash test/run.sh                # acceptance battery
```

The battery drives the real binary through real git repositories. It is the
half that notices when git changes underneath us, so a change is not finished
until both are green.

Requirements: Go (the version in `go.mod`), `git` 2.29 or newer, and `bash`.
The battery is written for bash 3.2, because that is what macOS ships.

## What a good change looks like

- **A test that fails first.** Write it, watch it fail for the reason you
  expect, then make it pass.
- **Then break it on purpose.** Delete or invert the check your test is meant
  to guard and confirm the test goes red. A green suite proves nothing until a
  broken build makes it red — several tests in this repository's history passed
  against code with the guard removed entirely.
- **Say why in the code.** Comments here explain the reasoning that is not
  visible from the code: what was measured, which failure mode it prevents,
  what was tried and rejected. Comments that restate the syntax get deleted.

## The rules that are not negotiable

These come from the design, and a change that breaks one will be sent back:

- **Never shell out to delete.** No `rm -rf`, no `find -delete`, no walker that
  follows symlinks. Deletion goes through `os.RemoveAll` on a path wkt computed.
- **Anything that deletes enumerates the filesystem**, never the state file.
- **Every check fails closed.** A check whose git call errors blocks. "Cannot
  tell" is treated as "would lose work".
- **Never surface raw git output.** It contains absolute paths belonging to
  other tasks. Wrap it in a typed error.
- **No isolation claim.** The perimeter prevents accidents. It is not a
  boundary, and nothing in the output may suggest that it is.

## Reporting a bug

Include `wkt version`, `git --version`, your OS, and — if you can — the shape
of the workspace (how many repositories, at what depths, and whether any are
nested or carry submodules). Most defects in this tool so far have depended on
exactly that shape.
