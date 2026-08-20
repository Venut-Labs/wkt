# wkt Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A working `wkt` binary that adopts a multi-repo workspace, materialises a mirrored task tree backed by per-repository bare stores, reports its state, and refuses to remove anything that would lose work.

**Architecture:** A single Go binary shelling out to `git`. Repositories are discovered under a plain (non-git) workspace directory and mirrored into per-repository bare stores in a container beside the workspace; task worktrees are cut from those stores, so the workspace itself is never written to. Task state is authoritative and versioned on disk, while anything that deletes enumerates the filesystem instead of trusting that state.

**Tech Stack:** Go 1.26 (stdlib only — no third-party dependencies), `git` ≥ 2.29 via `os/exec`, `bash` for the acceptance battery.

**Spec:** `docs/superpowers/specs/2026-08-19-wkt-design.md`

## Global Constraints

- **Language:** Go, standard library only. No third-party modules in v0.
- **Platforms:** macOS and Linux. Windows is explicitly out of scope (spec §1.1).
- **git floor:** 2.29 (`git worktree repair` landed in 2.29.0). Refuse to run below it.
- **Never shell out to delete.** No `rm -rf`, no `find -delete`, no symlink-following walker. Deletion goes through `os.RemoveAll` on a `wkt`-computed path (spec H3).
- **Never resolve a stash by index** (spec H4).
- **Never surface raw git stderr** — it contains absolute paths belonging to other tasks (spec §5.5). Wrap in a typed error.
- **Anything that deletes enumerates the filesystem**, never the state file (spec §5.4).
- **Every path is recorded in all known spellings** — as typed, `realpath`, and the macOS `/private` form (spec §5.6).
- **Store id** is `slug(workspace-relative-path) + "-" + hex(sha256(canonical-abs-path))[:8]` — never the basename (spec §5.2).
- **Base resolution order per repository:** `origin/HEAD` → `init.defaultBranch` → current `HEAD`. Never a hardcoded `main` (spec §5.5).

---

### Task 1: Module skeleton, git wrapper, typed errors

**Files:**
- Create: `go.mod`
- Create: `internal/wkterr/wkterr.go`
- Create: `internal/gitx/gitx.go`
- Test: `internal/gitx/gitx_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `wkterr.E{Code, Message, Repo, Path, Expected, Found, Remedy []string}` implementing `error`; constructor `wkterr.New(code, msg string) *E`; methods `WithRepo(string) *E`, `WithPath(string) *E`, `WithFound(string) *E`, `WithRemedy(...string) *E`; `wkterr.JSON(err error) []byte`.
  - `gitx.Run(dir string, args ...string) (string, error)` — trimmed stdout, or a `*wkterr.E` with code `WKT_GIT_FAILED` whose `Found` is the **first line** of stderr.
  - `gitx.RunOK(dir string, args ...string) bool`.
  - `gitx.Version() (major, minor int, err error)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/gitx/gitx_test.go
package gitx

import (
	"strings"
	"testing"
)

func TestRunReturnsTrimmedStdout(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-q"); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := Run(dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	if out != "true" {
		t.Fatalf("got %q, want %q", out, "true")
	}
}

func TestRunWrapsFailureWithoutLeakingFullStderr(t *testing.T) {
	dir := t.TempDir()
	_, err := Run(dir, "rev-parse", "--verify", "refs/heads/nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "WKT_GIT_FAILED") {
		t.Fatalf("error %q does not carry the code", msg)
	}
	if strings.Count(msg, "\n") > 0 {
		t.Fatalf("error must be a single line, got %q", msg)
	}
}

func TestVersionMeetsFloor(t *testing.T) {
	major, minor, err := Version()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if major < 2 || (major == 2 && minor < 29) {
		t.Skipf("git %d.%d is below the 2.29 floor; nothing to assert", major, minor)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/gitx/ -run TestRun -v`
Expected: FAIL — the package does not compile, `undefined: Run`.

- [ ] **Step 3: Write the typed error**

```go
// internal/wkterr/wkterr.go
package wkterr

import (
	"encoding/json"
	"fmt"
)

type E struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Repo     string   `json:"repo,omitempty"`
	Path     string   `json:"path,omitempty"`
	Expected string   `json:"expected,omitempty"`
	Found    string   `json:"found,omitempty"`
	Remedy   []string `json:"remedy,omitempty"`
}

func New(code, msg string) *E { return &E{Code: code, Message: msg} }

func (e *E) Error() string {
	if e.Found != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Found)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *E) WithRepo(r string) *E        { e.Repo = r; return e }
func (e *E) WithPath(p string) *E        { e.Path = p; return e }
func (e *E) WithFound(f string) *E       { e.Found = f; return e }
func (e *E) WithExpected(x string) *E    { e.Expected = x; return e }
func (e *E) WithRemedy(r ...string) *E   { e.Remedy = append(e.Remedy, r...); return e }

func JSON(err error) []byte {
	if e, ok := err.(*E); ok {
		b, _ := json.Marshal(e)
		return b
	}
	b, _ := json.Marshal(&E{Code: "WKT_INTERNAL", Message: err.Error()})
	return b
}
```

- [ ] **Step 4: Write the git wrapper**

```go
// internal/gitx/gitx.go
package gitx

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"

	"wkt/internal/wkterr"
)

func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		first := strings.SplitN(strings.TrimSpace(errb.String()), "\n", 2)[0]
		return "", wkterr.New("WKT_GIT_FAILED", "git "+args[0]+" failed").
			WithPath(dir).WithFound(first)
	}
	return strings.TrimSpace(out.String()), nil
}

func RunOK(dir string, args ...string) bool {
	_, err := Run(dir, args...)
	return err == nil
}

func Version() (int, int, error) {
	out, err := Run(".", "--version")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out) // "git version 2.50.1"
	if len(fields) < 3 {
		return 0, 0, wkterr.New("WKT_GIT_VERSION", "cannot parse git version").WithFound(out)
	}
	parts := strings.Split(fields[2], ".")
	major, _ := strconv.Atoi(parts[0])
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major, minor, nil
}
```

- [ ] **Step 5: Create the module and run the tests**

```bash
go mod init wkt && go test ./internal/... -v
```
Expected: all three tests PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod internal/gitx internal/wkterr
git commit -m "feat: git wrapper and typed errors"
```

---

### Task 2: Path canonicalisation and spellings

**Files:**
- Create: `internal/paths/paths.go`
- Test: `internal/paths/paths_test.go`

**Interfaces:**
- Consumes: `wkterr`.
- Produces:
  - `paths.Canonical(p string) (string, error)` — absolute, symlinks resolved.
  - `paths.Spellings(p string) []string` — every spelling of one path: as given (absolutised), canonical, and on macOS the `/private`-prefixed and `/private`-stripped forms. Deduplicated, stable order.
  - `paths.IsUnder(child, parent string) bool` — canonical containment, no lexical prefix bugs (`/a/bc` is not under `/a/b`).

- [ ] **Step 1: Write the failing test**

```go
// internal/paths/paths_test.go
package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpellingsIncludeCanonicalAndGiven(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got := Spellings(link)
	if len(got) < 2 {
		t.Fatalf("want at least the given and canonical spellings, got %v", got)
	}
	canon, _ := Canonical(link)
	var sawGiven, sawCanon bool
	for _, s := range got {
		if s == link {
			sawGiven = true
		}
		if s == canon {
			sawCanon = true
		}
	}
	if !sawGiven || !sawCanon {
		t.Fatalf("spellings %v must contain both %q and %q", got, link, canon)
	}
}

func TestIsUnderRejectsLexicalSiblings(t *testing.T) {
	base := t.TempDir()
	b := filepath.Join(base, "b")
	bc := filepath.Join(base, "bc")
	for _, d := range []string{b, bc} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if IsUnder(bc, b) {
		t.Fatalf("%q must not be considered under %q", bc, b)
	}
	if !IsUnder(filepath.Join(b, "x"), b) {
		t.Fatal("a real child must be under its parent")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/paths/ -v`
Expected: FAIL — `undefined: Spellings`.

- [ ] **Step 3: Write the implementation**

```go
// internal/paths/paths.go
package paths

import (
	"path/filepath"
	"runtime"
	"strings"
)

func Canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, nil // the path may not exist yet; absolute is the best we have
	}
	return resolved, nil
}

func Spellings(p string) []string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	canon, _ := Canonical(p)
	out := []string{abs}
	add := func(s string) {
		for _, existing := range out {
			if existing == s {
				return
			}
		}
		out = append(out, s)
	}
	add(canon)
	if runtime.GOOS == "darwin" {
		for _, s := range []string{abs, canon} {
			if strings.HasPrefix(s, "/private/") {
				add(strings.TrimPrefix(s, "/private"))
			} else {
				add("/private" + s)
			}
		}
	}
	return out
}

func IsUnder(child, parent string) bool {
	c, _ := Canonical(child)
	p, _ := Canonical(parent)
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/paths/ -v`
Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/paths
git commit -m "feat: path canonicalisation and spellings"
```

---

### Task 3: Repository discovery and `.git` marker classification

**Files:**
- Create: `internal/discover/discover.go`
- Test: `internal/discover/discover_test.go`

**Interfaces:**
- Consumes: `gitx`, `paths`, `wkterr`.
- Produces:
  - `discover.Kind` with constants `KindRepo`, `KindLinkedWorktree`, `KindSubmodule`, `KindNested`.
  - `discover.Entry{RelPath, AbsPath string; Kind Kind; ContainedBy string}`.
  - `discover.Walk(workspace string, maxDepth int) ([]Entry, error)` — enumerates `.git` markers as **file or directory**, never follows symlinks, classifies each per spec §5.3 rule 6.
  - `discover.NestedPairs(entries []Entry) [][2]string` — repository pairs where one contains the other.

- [ ] **Step 1: Write the failing test**

```go
// internal/discover/discover_test.go
package discover

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-c", "init.defaultBranch=main", "init", "-q")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init in %s: %v", dir, err)
	}
}

func TestWalkFindsReposAtMixedDepths(t *testing.T) {
	ws := t.TempDir()
	gitInit(t, filepath.Join(ws, "services", "svc-a"))
	gitInit(t, filepath.Join(ws, "docs"))
	if err := os.MkdirAll(filepath.Join(ws, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Kind{}
	for _, e := range entries {
		found[e.RelPath] = e.Kind
	}
	if found["services/svc-a"] != KindRepo {
		t.Fatalf("services/svc-a not discovered as a repo: %v", found)
	}
	if found["docs"] != KindRepo {
		t.Fatalf("docs not discovered as a repo: %v", found)
	}
	if _, ok := found["notes"]; ok {
		t.Fatal("a plain directory must not be reported as a repository")
	}
}

func TestWalkClassifiesLinkedWorktreeNotAsRepo(t *testing.T) {
	ws := t.TempDir()
	repo := filepath.Join(ws, "svc-a")
	gitInit(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=e@x", "-c", "user.name=t"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}
	wt := filepath.Join(ws, "svc-a-wt")
	cmd := exec.Command("git", "worktree", "add", "-q", wt)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %s", out)
	}
	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.RelPath == "svc-a-wt" && e.Kind != KindLinkedWorktree {
			t.Fatalf("linked worktree classified as %v, want KindLinkedWorktree", e.Kind)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/discover/ -v`
Expected: FAIL — `undefined: Walk`.

- [ ] **Step 3: Write the implementation**

```go
// internal/discover/discover.go
package discover

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"wkt/internal/gitx"
	"wkt/internal/paths"
)

type Kind int

const (
	KindRepo Kind = iota
	KindLinkedWorktree
	KindSubmodule
	KindNested
)

type Entry struct {
	RelPath     string
	AbsPath     string
	Kind        Kind
	ContainedBy string
}

// Walk enumerates .git markers under workspace, never following symlinks.
func Walk(workspace string, maxDepth int) ([]Entry, error) {
	root, err := paths.Canonical(workspace)
	if err != nil {
		return nil, err
	}
	var out []Entry
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, do not abort the walk
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if strings.Count(rel, string(filepath.Separator)) >= maxDepth {
			return fs.SkipDir
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fs.SkipDir // never follow symlinks (spec §5.3 rule 4)
		}
		if d.Name() != ".git" {
			return nil
		}
		repoDir := filepath.Dir(p)
		repoRel, _ := filepath.Rel(root, repoDir)
		e := Entry{RelPath: filepath.ToSlash(repoRel), AbsPath: repoDir, Kind: classify(p, repoDir)}
		out = append(out, e)
		return fs.SkipDir // do not descend into a repository's own .git
	})
	if err != nil {
		return nil, err
	}
	markNested(out)
	return out, nil
}

func classify(gitMarker, repoDir string) Kind {
	info, err := os.Lstat(gitMarker)
	if err != nil {
		return KindRepo
	}
	if info.IsDir() {
		return KindRepo
	}
	// .git is a file: either a linked worktree or a submodule checkout.
	b, err := os.ReadFile(gitMarker)
	if err != nil {
		return KindRepo
	}
	target := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
	if strings.Contains(target, string(filepath.Separator)+"worktrees"+string(filepath.Separator)) {
		return KindLinkedWorktree
	}
	if strings.Contains(target, string(filepath.Separator)+"modules"+string(filepath.Separator)) {
		return KindSubmodule
	}
	if gitx.RunOK(repoDir, "rev-parse", "--git-common-dir") {
		return KindRepo
	}
	return KindRepo
}

func markNested(entries []Entry) {
	for i := range entries {
		if entries[i].Kind != KindRepo {
			continue
		}
		for j := range entries {
			if i == j || entries[j].Kind != KindRepo {
				continue
			}
			if paths.IsUnder(entries[i].AbsPath, entries[j].AbsPath) {
				entries[i].Kind = KindNested
				entries[i].ContainedBy = entries[j].RelPath
			}
		}
	}
}

func NestedPairs(entries []Entry) [][2]string {
	var out [][2]string
	for _, e := range entries {
		if e.Kind == KindNested {
			out = append(out, [2]string{e.RelPath, e.ContainedBy})
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/discover/ -v`
Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discover
git commit -m "feat: repository discovery with .git marker classification"
```

---

### Task 4: Container layout, location fallback and lock

**Files:**
- Create: `internal/container/container.go`
- Test: `internal/container/container_test.go`

**Interfaces:**
- Consumes: `paths`, `wkterr`.
- Produces:
  - `container.C{Root, Workspace string}` with methods `StoreDir() string`, `TreesDir() string`, `StateDir() string`, `StagingDir() string`, `TreePath(task string) string`.
  - `container.Locate(workspace string) (C, error)` — `<workspace>.worktrees` when the parent is writable, otherwise `~/.local/state/wkt/<hash>`; never a descendant of the workspace.
  - `container.Create(c C) error` — creates the four subdirectories, 0o700.
  - `container.Lock(c C) (release func(), err error)` — one exclusive lock per container, stale-PID sweep.

- [ ] **Step 1: Write the failing test**

```go
// internal/container/container_test.go
package container

import (
	"os"
	"path/filepath"
	"testing"

	"wkt/internal/paths"
)

func TestLocateIsSiblingAndNeverInsideWorkspace(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "work")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := Locate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if c.Root != ws+".worktrees" {
		t.Fatalf("container %q is not the documented sibling of %q", c.Root, ws)
	}
	if paths.IsUnder(c.Root, ws) {
		t.Fatalf("container %q must never live inside the workspace", c.Root)
	}
}

func TestLocateFallsBackWhenParentUnwritable(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "work")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o755) })

	c, err := Locate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if c.Root == ws+".worktrees" {
		t.Fatal("an unwritable parent must force the state-directory fallback")
	}
}

func TestLockIsExclusive(t *testing.T) {
	base := t.TempDir()
	c := C{Root: filepath.Join(base, "c"), Workspace: base}
	if err := Create(c); err != nil {
		t.Fatal(err)
	}
	release, err := Lock(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(c); err == nil {
		release()
		t.Fatal("a second lock must fail while the first is held")
	}
	release()
	release2, err := Lock(c)
	if err != nil {
		t.Fatalf("lock after release must succeed: %v", err)
	}
	release2()
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/container/ -v`
Expected: FAIL — `undefined: Locate`.

- [ ] **Step 3: Write the implementation**

```go
// internal/container/container.go
package container

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"wkt/internal/paths"
	"wkt/internal/wkterr"
)

type C struct {
	Root      string
	Workspace string
}

func (c C) StoreDir() string   { return filepath.Join(c.Root, "store") }
func (c C) TreesDir() string   { return filepath.Join(c.Root, "trees") }
func (c C) StateDir() string   { return filepath.Join(c.Root, "state", "tasks") }
func (c C) StagingDir() string { return filepath.Join(c.Root, "staging") }

func (c C) TreePath(task string) string { return filepath.Join(c.TreesDir(), task) }

func Locate(workspace string) (C, error) {
	ws, err := paths.Canonical(workspace)
	if err != nil {
		return C{}, err
	}
	sibling := ws + ".worktrees"
	if writable(filepath.Dir(ws)) {
		return C{Root: sibling, Workspace: ws}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return C{}, wkterr.New("WKT_NO_CONTAINER", "cannot place the container").WithFound(err.Error())
	}
	sum := sha256.Sum256([]byte(ws))
	id := hex.EncodeToString(sum[:])[:12]
	return C{Root: filepath.Join(home, ".local", "state", "wkt", id), Workspace: ws}, nil
}

func writable(dir string) bool {
	probe := filepath.Join(dir, ".wkt-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}

func Create(c C) error {
	for _, d := range []string{c.StoreDir(), c.TreesDir(), c.StateDir(), c.StagingDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return wkterr.New("WKT_NO_CONTAINER", "cannot create the container").
				WithPath(d).WithFound(err.Error())
		}
	}
	return nil
}

func Lock(c C) (func(), error) {
	path := filepath.Join(c.Root, ".wkt.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, wkterr.New("WKT_LOCKED", "cannot open the container lock").
			WithPath(path).WithFound(err.Error())
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder, _ := os.ReadFile(path)
		_ = f.Close()
		return nil, wkterr.New("WKT_LOCKED", "another wkt process holds the container lock").
			WithPath(path).WithFound(string(holder)).
			WithRemedy("wait for it to finish", "or remove the lock file if no wkt process is running")
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(path)
	}, nil
}

var _ = fmt.Sprintf
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/container/ -v`
Expected: all three tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/container
git commit -m "feat: container layout, location fallback and exclusive lock"
```

---

### Task 5: Authoritative state with atomic writes

**Files:**
- Create: `internal/state/state.go`
- Test: `internal/state/state_test.go`

**Interfaces:**
- Consumes: `wkterr`.
- Produces:
  - `state.Repo{RelPath, AbsPath, StoreID, Branch, BaseSHA, BaseRef, WorktreePath, StoreWorktreeName, BasePinRef string}`.
  - `state.LinkSlot{RelPath, Target, Type string}` where `Type` is `"symlink"` or `"copy"`; copies carry `Hash`.
  - `state.Task{SchemaVersion int, Name, Container, Workspace string, WorkspaceSpellings []string, BaseEpoch time.Time, Repos []Repo, Links []LinkSlot, PerimeterCoverage []string, PerimeterHashes map[string]string}`.
  - `state.Save(dir string, t Task) error` — write to a temporary file in the same directory, then rename.
  - `state.Load(dir, name string) (Task, error)`, `state.List(dir string) ([]string, error)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/state/state_test.go
package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveIsAtomicAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	task := Task{
		SchemaVersion: 1,
		Name:          "feat-42",
		Workspace:     "/ws",
		BaseEpoch:     time.Now().UTC().Truncate(time.Second),
		Repos: []Repo{{
			RelPath: "services/svc-a", StoreID: "services-svc-a-deadbeef",
			Branch: "feat-42", BaseSHA: "abc123", StoreWorktreeName: "svc-a",
		}},
	}
	if err := Save(dir, task); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("a temporary file survived the save: %s", e.Name())
		}
	}
	got, err := Load(dir, "feat-42")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repos) != 1 || got.Repos[0].StoreWorktreeName != "svc-a" {
		t.Fatalf("round-trip lost the store worktree name: %+v", got)
	}
	if !got.BaseEpoch.Equal(task.BaseEpoch) {
		t.Fatalf("base epoch %v != %v", got.BaseEpoch, task.BaseEpoch)
	}
}

func TestLoadMissingTaskIsTyped(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir, "nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); got == "" || !contains(got, "WKT_NO_TASK") {
		t.Fatalf("error %q must carry WKT_NO_TASK", got)
	}
}

func contains(h, n string) bool { return len(h) >= len(n) && (h == n || len(h) > 0 && indexOf(h, n) >= 0) }
func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/state/ -v`
Expected: FAIL — `undefined: Save`.

- [ ] **Step 3: Write the implementation**

```go
// internal/state/state.go
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wkt/internal/wkterr"
)

const SchemaVersion = 1

type Repo struct {
	RelPath           string `json:"rel_path"`
	AbsPath           string `json:"abs_path"`
	StoreID           string `json:"store_id"`
	Branch            string `json:"branch"`
	BaseSHA           string `json:"base_sha"`
	BaseRef           string `json:"base_ref"`
	WorktreePath      string `json:"worktree_path"`
	StoreWorktreeName string `json:"store_worktree_name"`
	BasePinRef        string `json:"base_pin_ref"`
}

type LinkSlot struct {
	RelPath string `json:"rel_path"`
	Target  string `json:"target"`
	Type    string `json:"type"`
	Hash    string `json:"hash,omitempty"`
}

type Task struct {
	SchemaVersion      int               `json:"schema_version"`
	Name               string            `json:"name"`
	Container          string            `json:"container"`
	Workspace          string            `json:"workspace"`
	WorkspaceSpellings []string          `json:"workspace_spellings"`
	BaseEpoch          time.Time         `json:"base_epoch"`
	Repos              []Repo            `json:"repos"`
	Links              []LinkSlot        `json:"links"`
	PerimeterCoverage  []string          `json:"perimeter_coverage,omitempty"`
	PerimeterHashes    map[string]string `json:"perimeter_hashes,omitempty"`
}

func path(dir, name string) string { return filepath.Join(dir, name+".json") }

func Save(dir string, t Task) error {
	if t.SchemaVersion == 0 {
		t.SchemaVersion = SchemaVersion
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return wkterr.New("WKT_STATE_WRITE", "cannot create the state directory").WithPath(dir)
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return wkterr.New("WKT_STATE_WRITE", "cannot encode task state").WithFound(err.Error())
	}
	tmp, err := os.CreateTemp(dir, t.Name+".*.tmp")
	if err != nil {
		return wkterr.New("WKT_STATE_WRITE", "cannot create a temporary state file").WithPath(dir)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return wkterr.New("WKT_STATE_WRITE", "cannot write task state").WithPath(tmp.Name())
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return wkterr.New("WKT_STATE_WRITE", "cannot close the temporary state file").WithPath(tmp.Name())
	}
	if err := os.Rename(tmp.Name(), path(dir, t.Name)); err != nil {
		_ = os.Remove(tmp.Name())
		return wkterr.New("WKT_STATE_WRITE", "cannot commit task state").WithPath(path(dir, t.Name))
	}
	return nil
}

func Load(dir, name string) (Task, error) {
	b, err := os.ReadFile(path(dir, name))
	if err != nil {
		return Task{}, wkterr.New("WKT_NO_TASK", "no such task").WithPath(path(dir, name))
	}
	var t Task
	if err := json.Unmarshal(b, &t); err != nil {
		return Task{}, wkterr.New("WKT_STATE_CORRUPT", "task state is not readable").
			WithPath(path(dir, name)).WithFound(err.Error())
	}
	return t, nil
}

func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // no tasks yet is not an error
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/state/ -v`
Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/state
git commit -m "feat: authoritative task state with atomic writes"
```

---

### Task 6: Store construction — pin, mirror, de-borrow, two remotes

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `gitx`, `paths`, `wkterr`.
- Produces:
  - `store.ID(relPath, absPath string) string`.
  - `store.Ensure(storeDir, repoAbs, relPath, taskName, baseSHA string) (storePath string, err error)` — performs the six steps of spec §5.2 in order.
  - `store.FetchWorkspace(storePath string) error`.
  - `store.HasObject(storePath, sha string) bool`.

- [ ] **Step 1: Write the failing test**

```go
// internal/store/store_test.go
package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func g(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.email=e@x", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %s", args, dir, out)
	}
	return string(out)
}

func seedRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, dir, "init", "-q")
	g(t, dir, "add", "-A")
	g(t, dir, "commit", "-qm", "init")
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out[:40])
}

func TestEnsureProducesUsableStoreThatSurvivesSourceDeletion(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws", "services", "svc-a")
	sha := seedRepo(t, ws)
	storeDir := filepath.Join(base, "container", "store")
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	sp, err := Ensure(storeDir, ws, "services/svc-a", "feat-42", sha)
	if err != nil {
		t.Fatal(err)
	}
	if !HasObject(sp, sha) {
		t.Fatal("the store must contain the base commit")
	}
	// The pin must exist in the WORKSPACE repository, written before the clone.
	if out := g(t, ws, "rev-parse", "--verify", "refs/wkt/base/feat-42"); len(out) < 40 {
		t.Fatalf("base pin missing in the workspace repo: %q", out)
	}
	// De-borrowed: no alternates file, and the object survives losing the source.
	if _, err := os.Stat(filepath.Join(sp, "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatal("a de-borrowed store must have no alternates file")
	}
	if err := os.RemoveAll(filepath.Join(base, "ws")); err != nil {
		t.Fatal(err)
	}
	if !HasObject(sp, sha) {
		t.Fatal("the store must survive deletion of the workspace repository")
	}
}

func TestEnsureConfiguresFetchRefspecAndWorkspaceRemote(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws", "svc-a")
	sha := seedRepo(t, ws)
	storeDir := filepath.Join(base, "c", "store")
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sp, err := Ensure(storeDir, ws, "svc-a", "t1", sha)
	if err != nil {
		t.Fatal(err)
	}
	refspec := g(t, sp, "config", "--get", "remote.origin.fetch")
	if len(refspec) == 0 {
		t.Fatal("bare clones set no refspec; Ensure must add one (spec H15)")
	}
	if out := g(t, sp, "remote"); !containsLine(out, "workspace") {
		t.Fatalf("the workspace remote is missing: %q", out)
	}
	// A commit made locally in the workspace and never pushed must become reachable.
	if err := os.WriteFile(filepath.Join(ws, "src", "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, ws, "add", "-A")
	g(t, ws, "commit", "-qm", "local only")
	local := g(t, ws, "rev-parse", "HEAD")[:40]
	if HasObject(sp, local) {
		t.Fatal("precondition: the store should not have the new commit yet")
	}
	if err := FetchWorkspace(sp); err != nil {
		t.Fatal(err)
	}
	if !HasObject(sp, local) {
		t.Fatal("after FetchWorkspace the local-only commit must be reachable (spec §5.2)")
	}
}

func containsLine(haystack, want string) bool {
	for _, line := range splitLines(haystack) {
		if line == want {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out, cur = []string{}, ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -v`
Expected: FAIL — `undefined: Ensure`.

- [ ] **Step 3: Write the implementation**

```go
// internal/store/store.go
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"wkt/internal/gitx"
	"wkt/internal/paths"
	"wkt/internal/wkterr"
)

// ID is a collision-free function of the workspace-relative path — never the
// basename, so services/api and tools/api cannot collide (spec §5.2).
func ID(relPath, absPath string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, relPath)
	canon, _ := paths.Canonical(absPath)
	sum := sha256.Sum256([]byte(canon))
	return strings.Trim(slug, "-") + "-" + hex.EncodeToString(sum[:])[:8]
}

// Ensure performs the six steps of spec §5.2, in order. The base pin is written
// FIRST so no gc window exists before the store references the base (H11).
func Ensure(storeDir, repoAbs, relPath, taskName, baseSHA string) (string, error) {
	pin := "refs/wkt/base/" + taskName
	if _, err := gitx.Run(repoAbs, "update-ref", pin, baseSHA); err != nil {
		return "", wkterr.New("WKT_PIN_FAILED", "cannot pin the base commit in the workspace repository").
			WithRepo(relPath).WithPath(repoAbs)
	}

	sp := filepath.Join(storeDir, ID(relPath, repoAbs)+".git")
	if _, err := os.Stat(sp); err == nil {
		return sp, nil // idempotent
	}

	if _, err := gitx.Run(storeDir, "clone", "--shared", "--bare", "-q", repoAbs, sp); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot mirror the repository").
			WithRepo(relPath).WithPath(sp)
	}
	// De-borrow: copy the objects in, then drop the alternates pointer, so the
	// store survives deletion or re-clone of the workspace repository (spec §5.2).
	if _, err := gitx.Run(sp, "repack", "-a", "-d", "-q"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot repack the store").WithRepo(relPath).WithPath(sp)
	}
	if err := os.Remove(filepath.Join(sp, "objects", "info", "alternates")); err != nil && !os.IsNotExist(err) {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot de-borrow the store").WithRepo(relPath).WithPath(sp)
	}

	origin, err := gitx.Run(repoAbs, "remote", "get-url", "origin")
	if err == nil && origin != "" {
		if _, err := gitx.Run(sp, "remote", "set-url", "origin", origin); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot point the store at the real origin").WithRepo(relPath)
		}
	} else {
		_, _ = gitx.Run(sp, "remote", "remove", "origin")
	}
	// Bare clones set NO fetch refspec; without this refs/remotes/* never exist,
	// which silently breaks sync and the unpushed-commit guard (spec H15).
	if _, err := gitx.Run(sp, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot configure the origin refspec").WithRepo(relPath)
	}
	// Second remote: the workspace repository, so a task can branch from work the
	// developer has committed locally and not pushed (spec §5.2).
	if _, err := gitx.Run(sp, "remote", "add", "workspace", repoAbs); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot add the workspace remote").WithRepo(relPath)
	}
	if _, err := gitx.Run(sp, "config", "remote.workspace.fetch", "+refs/heads/*:refs/remotes/ws/*"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot configure the workspace refspec").WithRepo(relPath)
	}
	for _, kv := range [][2]string{{"gc.auto", "0"}, {"core.hooksPath", "/dev/null"}} {
		if _, err := gitx.Run(sp, "config", kv[0], kv[1]); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot harden the store").WithRepo(relPath)
		}
	}
	return sp, nil
}

func FetchWorkspace(storePath string) error {
	if _, err := gitx.Run(storePath, "fetch", "-q", "workspace"); err != nil {
		return wkterr.New("WKT_FETCH_FAILED", "cannot fetch from the workspace repository").WithPath(storePath)
	}
	return nil
}

func HasObject(storePath, sha string) bool {
	return gitx.RunOK(storePath, "cat-file", "-e", sha)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/ -v`
Expected: both tests PASS. The second one is the regression guard for the two design defects found in review — a missing refspec and a store blind to local commits.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat: store construction with base pin, de-borrow and two remotes"
```

---

### Task 7: Tree materialisation — mirrored shape, back-fill, loose files

**Files:**
- Create: `internal/tree/tree.go`
- Test: `internal/tree/tree_test.go`

**Interfaces:**
- Consumes: `discover`, `state`, `wkterr`.
- Produces:
  - `tree.Plan{Materialise []string; BackFill []string; LinkDirs []string; CopyFiles []string}`.
  - `tree.PlanFor(workspace string, entries []discover.Entry, selected []string) (Plan, error)`.
  - `tree.Materialise(treeRoot, workspace string, p Plan) ([]state.LinkSlot, error)` — creates ancestor directories for real, back-fills un-materialised repositories as **absolute** symlinks at their mirrored positions, symlinks non-git directories, copies loose files and records their hashes.

- [ ] **Step 1: Write the failing test**

```go
// internal/tree/tree_test.go
package tree

import (
	"os"
	"path/filepath"
	"testing"

	"wkt/internal/discover"
)

func TestBackFilledRepoKeepsCrossRepoRelativePathResolving(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "shared", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "shared", "src", "index.js"), []byte("export const X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []discover.Entry{
		{RelPath: "services/svc-a", AbsPath: filepath.Join(ws, "services", "svc-a"), Kind: discover.KindRepo},
		{RelPath: "shared", AbsPath: filepath.Join(ws, "shared"), Kind: discover.KindRepo},
	}
	p, err := PlanFor(ws, entries, []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.BackFill) != 1 || p.BackFill[0] != "shared" {
		t.Fatalf("shared must be back-filled, got %+v", p)
	}

	treeRoot := filepath.Join(base, "tree")
	if err := os.MkdirAll(filepath.Join(treeRoot, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialise(treeRoot, ws, p); err != nil {
		t.Fatal(err)
	}
	// The reference the source code actually contains: ../../shared from services/svc-a
	target := filepath.Join(treeRoot, "services", "svc-a", "..", "..", "shared", "src", "index.js")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("../../shared must resolve through the back-filled symlink: %v", err)
	}
}

func TestAncestorDirectoriesAreRealNotSymlinked(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries := []discover.Entry{
		{RelPath: "services/svc-a", AbsPath: filepath.Join(ws, "services", "svc-a"), Kind: discover.KindRepo},
	}
	p, err := PlanFor(ws, entries, []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	treeRoot := filepath.Join(base, "tree")
	if err := os.MkdirAll(filepath.Join(treeRoot, "services", "svc-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialise(treeRoot, ws, p); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(treeRoot, "services"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("a directory on the path to a selected repo must be real, never a symlink (spec §5.3 rule 2)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tree/ -v`
Expected: FAIL — `undefined: PlanFor`.

- [ ] **Step 3: Write the implementation**

```go
// internal/tree/tree.go
package tree

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"wkt/internal/discover"
	"wkt/internal/state"
	"wkt/internal/wkterr"
)

type Plan struct {
	Materialise []string // repositories that become real worktrees
	BackFill    []string // repositories present only as symlinks to the workspace
	LinkDirs    []string // non-git directories
	CopyFiles   []string // loose files
}

func PlanFor(workspace string, entries []discover.Entry, selected []string) (Plan, error) {
	sel := map[string]bool{}
	for _, s := range selected {
		sel[s] = true
	}
	var p Plan
	repoPaths := map[string]bool{}
	for _, e := range entries {
		if e.Kind != discover.KindRepo {
			continue
		}
		repoPaths[e.RelPath] = true
		if sel[e.RelPath] {
			p.Materialise = append(p.Materialise, e.RelPath)
		} else {
			p.BackFill = append(p.BackFill, e.RelPath)
		}
	}
	for _, s := range selected {
		if !repoPaths[s] {
			return Plan{}, wkterr.New("WKT_NO_SUCH_REPO", "not a discovered repository").WithRepo(s)
		}
	}
	// Ancestors of anything we materialise must stay real directories.
	ancestors := map[string]bool{}
	for _, m := range p.Materialise {
		for d := filepath.Dir(m); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
			ancestors[d] = true
		}
	}
	top, err := os.ReadDir(workspace)
	if err != nil {
		return Plan{}, wkterr.New("WKT_WORKSPACE_UNREADABLE", "cannot read the workspace").WithPath(workspace)
	}
	for _, e := range top {
		name := e.Name()
		if name == ".claude" || name == ".wkt" || repoPaths[name] || ancestors[name] {
			continue
		}
		if e.IsDir() {
			p.LinkDirs = append(p.LinkDirs, name)
		} else {
			p.CopyFiles = append(p.CopyFiles, name)
		}
	}
	return p, nil
}

func Materialise(treeRoot, workspace string, p Plan) ([]state.LinkSlot, error) {
	var slots []state.LinkSlot
	link := func(rel string) error {
		dst := filepath.Join(treeRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return wkterr.New("WKT_TREE_BUILD", "cannot create an ancestor directory").WithPath(dst)
		}
		src := filepath.Join(workspace, rel) // absolute target (spec §5.3 rule 3)
		if err := os.Symlink(src, dst); err != nil && !os.IsExist(err) {
			return wkterr.New("WKT_TREE_BUILD", "cannot create a link slot").WithPath(dst).WithFound(err.Error())
		}
		slots = append(slots, state.LinkSlot{RelPath: rel, Target: src, Type: "symlink"})
		return nil
	}
	for _, rel := range append(append([]string{}, p.BackFill...), p.LinkDirs...) {
		if err := link(rel); err != nil {
			return nil, err
		}
	}
	for _, rel := range p.CopyFiles {
		src := filepath.Join(workspace, rel)
		dst := filepath.Join(treeRoot, rel)
		sum, err := copyFile(src, dst)
		if err != nil {
			return nil, err
		}
		slots = append(slots, state.LinkSlot{RelPath: rel, Target: src, Type: "copy", Hash: sum})
	}
	return slots, nil
}

func copyFile(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot read a workspace file").WithPath(src)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot create a directory").WithPath(dst)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot write into the tree").WithPath(dst)
	}
	defer out.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot copy a workspace file").WithPath(dst)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Hash re-reads a copied file so teardown can detect divergence (spec §5.3 rule 5).
func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var _ = strings.TrimSpace
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/tree/ -v`
Expected: both tests PASS. The first is the differentiator guard — mirroring is worthless without back-fill.

- [ ] **Step 5: Commit**

```bash
git add internal/tree
git commit -m "feat: mirrored tree materialisation with back-filled repositories"
```

---

### Task 8: Two-phase create with rollback

**Files:**
- Create: `internal/task/create.go`
- Test: `internal/task/create_test.go`

**Interfaces:**
- Consumes: `container`, `discover`, `gitx`, `state`, `store`, `tree`, `wkterr`.
- Produces:
  - `task.Resolution{Repo state.Repo; Problems []*wkterr.E}`.
  - `task.Validate(c container.C, entries []discover.Entry, name string, selected []string) ([]state.Repo, error)` — phase one: resolves the base per repository and checks branch existence, ancestry, `worktree list` occupancy, D/F conflicts, case-fold collisions and `check-ref-format` across the **whole set** before anything is created.
  - `task.Create(c container.C, entries []discover.Entry, name string, selected []string) (state.Task, error)` — phase two, rolling back every worktree, branch, pin and directory it created on any failure.

- [ ] **Step 1: Write the failing test**

```go
// internal/task/create_test.go
package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"wkt/internal/container"
	"wkt/internal/discover"
)

func g(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.email=e@x", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %s", args, dir, out)
	}
	return string(out)
}

func seed(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, dir, "init", "-q")
	g(t, dir, "add", "-A")
	g(t, dir, "commit", "-qm", "init")
}

func fixture(t *testing.T) (container.C, []discover.Entry) {
	t.Helper()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seed(t, filepath.Join(ws, "services", "svc-a"))
	seed(t, filepath.Join(ws, "docs"))
	c, err := container.Locate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Create(c); err != nil {
		t.Fatal(err)
	}
	entries, err := discover.Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	return c, entries
}

func TestCreateBuildsMirroredTreeOnOneBranch(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-42", []string{"services/svc-a", "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Repos) != 2 {
		t.Fatalf("want 2 repos in the task, got %d", len(task.Repos))
	}
	for _, r := range task.Repos {
		br := g(t, r.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD")
		if br != "feat-42\n" {
			t.Fatalf("%s is on %q, want feat-42", r.RelPath, br)
		}
		if r.StoreWorktreeName == "" {
			t.Fatalf("%s: the store worktree registration name must be recorded", r.RelPath)
		}
	}
	if _, err := os.Stat(filepath.Join(c.TreePath("feat-42"), "services", "svc-a")); err != nil {
		t.Fatalf("the tree must mirror the workspace shape: %v", err)
	}
}

func TestCreateAbortsWholeSetWhenOneRepoHasADivergentBranch(t *testing.T) {
	c, entries := fixture(t)
	// docs already carries a branch of that name, pointing somewhere else.
	docs := filepath.Join(c.Workspace, "docs")
	g(t, docs, "branch", "feat-42")

	_, err := Create(c, entries, "feat-42", []string{"services/svc-a", "docs"})
	if err == nil {
		t.Fatal("create must refuse when one repository of the set has a conflicting branch")
	}
	if _, statErr := os.Stat(c.TreePath("feat-42")); !os.IsNotExist(statErr) {
		t.Fatal("a refused create must leave no tree behind")
	}
	out := g(t, filepath.Join(c.Workspace, "services", "svc-a"), "branch", "--list", "feat-42")
	if len(out) != 0 {
		t.Fatalf("rollback must remove branches created during the attempt, found %q", out)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/task/ -v`
Expected: FAIL — `undefined: Create`.

- [ ] **Step 3: Write phase one — validation**

```go
// internal/task/create.go
package task

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"wkt/internal/container"
	"wkt/internal/discover"
	"wkt/internal/gitx"
	"wkt/internal/paths"
	"wkt/internal/state"
	"wkt/internal/store"
	"wkt/internal/tree"
	"wkt/internal/wkterr"
)

func resolveBase(repoAbs string) (sha, ref string, err error) {
	if out, e := gitx.Run(repoAbs, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); e == nil && out != "" {
		if s, e2 := gitx.Run(repoAbs, "rev-parse", out); e2 == nil {
			return s, out, nil
		}
	}
	if def, e := gitx.Run(repoAbs, "config", "--get", "init.defaultBranch"); e == nil && def != "" {
		if s, e2 := gitx.Run(repoAbs, "rev-parse", "--verify", "refs/heads/"+def); e2 == nil {
			return s, "refs/heads/" + def, nil
		}
	}
	s, e := gitx.Run(repoAbs, "rev-parse", "HEAD")
	if e != nil {
		return "", "", wkterr.New("WKT_NO_BASE", "cannot resolve a base commit").WithPath(repoAbs)
	}
	return s, "HEAD", nil
}

func Validate(c container.C, entries []discover.Entry, name string, selected []string) ([]state.Repo, error) {
	if !gitx.RunOK(".", "check-ref-format", "--branch", name) {
		return nil, wkterr.New("WKT_BAD_TASK_NAME", "not a valid branch name").WithFound(name)
	}
	byRel := map[string]discover.Entry{}
	for _, e := range entries {
		byRel[e.RelPath] = e
	}
	var repos []state.Repo
	for _, rel := range selected {
		e, ok := byRel[rel]
		if !ok || e.Kind != discover.KindRepo {
			return nil, wkterr.New("WKT_NO_SUCH_REPO", "not a discovered repository").WithRepo(rel)
		}
		// A branch of that name must not already exist, locally or on the remote.
		if gitx.RunOK(e.AbsPath, "rev-parse", "--verify", "refs/heads/"+name) {
			return nil, wkterr.New("WKT_BRANCH_EXISTS", "a branch of that name already exists").
				WithRepo(rel).WithRemedy("choose another task name", "or delete the branch")
		}
		// Case-fold collision, checked on every platform so macOS and Linux agree.
		if out, err := gitx.Run(e.AbsPath, "for-each-ref", "--format=%(refname:short)", "refs/heads/"); err == nil {
			for _, b := range strings.Split(out, "\n") {
				if b != "" && strings.EqualFold(b, name) {
					return nil, wkterr.New("WKT_BRANCH_CASE_COLLISION", "a branch differing only in case exists").
						WithRepo(rel).WithFound(b)
				}
			}
		}
		// D/F conflict: refs/heads/feat and refs/heads/feat/42 cannot coexist.
		if out, err := gitx.Run(e.AbsPath, "for-each-ref", "--format=%(refname:short)", "refs/heads/"); err == nil {
			for _, b := range strings.Split(out, "\n") {
				if b == "" {
					continue
				}
				if strings.HasPrefix(b, name+"/") || strings.HasPrefix(name, b+"/") {
					return nil, wkterr.New("WKT_BRANCH_DF_CONFLICT", "a branch name conflicts hierarchically").
						WithRepo(rel).WithFound(b)
				}
			}
		}
		sha, ref, err := resolveBase(e.AbsPath)
		if err != nil {
			return nil, err
		}
		repos = append(repos, state.Repo{
			RelPath: rel, AbsPath: e.AbsPath, StoreID: store.ID(rel, e.AbsPath),
			Branch: name, BaseSHA: sha, BaseRef: ref,
			WorktreePath: filepath.Join(c.TreePath(name), rel),
			BasePinRef:   "refs/wkt/base/" + name,
		})
	}
	if len(repos) == 0 {
		return nil, wkterr.New("WKT_EMPTY_TASK", "no repositories selected")
	}
	return repos, nil
}
```

- [ ] **Step 4: Write phase two — execution with rollback**

```go
// appended to internal/task/create.go

type undo func()

func Create(c container.C, entries []discover.Entry, name string, selected []string) (state.Task, error) {
	if _, err := state.Load(c.StateDir(), name); err == nil {
		return state.Task{}, wkterr.New("WKT_TASK_EXISTS", "task already exists").
			WithFound(name).WithRemedy("wkt path "+name, "wkt rm "+name)
	}
	repos, err := Validate(c, entries, name, selected)
	if err != nil {
		return state.Task{}, err
	}

	var undos []undo
	rollback := func() {
		for i := len(undos) - 1; i >= 0; i-- {
			undos[i]()
		}
	}

	treeRoot := c.TreePath(name)
	if err := os.MkdirAll(treeRoot, 0o755); err != nil {
		return state.Task{}, wkterr.New("WKT_TREE_BUILD", "cannot create the task tree").WithPath(treeRoot)
	}
	undos = append(undos, func() { _ = os.RemoveAll(treeRoot) })

	for i := range repos {
		r := &repos[i]
		sp, err := store.Ensure(c.StoreDir(), r.AbsPath, r.RelPath, name, r.BaseSHA)
		if err != nil {
			rollback()
			return state.Task{}, err
		}
		if !store.HasObject(sp, r.BaseSHA) {
			if err := store.FetchWorkspace(sp); err != nil {
				rollback()
				return state.Task{}, err
			}
		}
		repoAbs, pin := r.AbsPath, r.BasePinRef
		undos = append(undos, func() { _, _ = gitx.Run(repoAbs, "update-ref", "-d", pin) })

		if err := os.MkdirAll(filepath.Dir(r.WorktreePath), 0o755); err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_TREE_BUILD", "cannot create an ancestor directory").
				WithPath(r.WorktreePath)
		}
		if _, err := gitx.Run(sp, "worktree", "add", "-q", "-b", name, r.WorktreePath, r.BaseSHA); err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_WORKTREE_ADD", "cannot create the worktree").
				WithRepo(r.RelPath).WithPath(r.WorktreePath)
		}
		storePath, wtPath := sp, r.WorktreePath
		undos = append(undos, func() {
			_, _ = gitx.Run(storePath, "worktree", "remove", "--force", wtPath)
			_, _ = gitx.Run(storePath, "branch", "-D", name)
		})
		if _, err := gitx.Run(sp, "worktree", "lock", "--reason", "held by wkt task "+name, r.WorktreePath); err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_WORKTREE_LOCK", "cannot lock the worktree").WithRepo(r.RelPath)
		}
		r.StoreWorktreeName = worktreeName(sp, r.WorktreePath)
	}

	plan, err := tree.PlanFor(c.Workspace, entries, selected)
	if err != nil {
		rollback()
		return state.Task{}, err
	}
	slots, err := tree.Materialise(treeRoot, c.Workspace, plan)
	if err != nil {
		rollback()
		return state.Task{}, err
	}

	t := state.Task{
		SchemaVersion: state.SchemaVersion, Name: name,
		Container: c.Root, Workspace: c.Workspace,
		WorkspaceSpellings: paths.Spellings(c.Workspace),
		BaseEpoch:          time.Now().UTC(),
		Repos:              repos, Links: slots,
	}
	if err := state.Save(c.StateDir(), t); err != nil {
		rollback()
		return state.Task{}, err
	}
	return t, nil
}

// worktreeName reads back the admin directory git chose, which it derives from
// the leaf basename and silently disambiguates (svc-a, svc-a1). repair cannot
// work without it (spec §5.4).
func worktreeName(storePath, worktreePath string) string {
	out, err := gitx.Run(storePath, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	want, _ := paths.Canonical(worktreePath)
	var current string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			current = strings.TrimPrefix(line, "worktree ")
			got, _ := paths.Canonical(current)
			if got == want {
				return filepath.Base(current)
			}
		}
	}
	return ""
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/task/ -v`
Expected: both tests PASS. The second is the guard for spec H10 — create is not atomic unless the rollback works.

- [ ] **Step 6: Commit**

```bash
git add internal/task
git commit -m "feat: two-phase task create with full rollback"
```

---

### Task 9: Refuse-only teardown

**Files:**
- Create: `internal/task/remove.go`
- Test: `internal/task/remove_test.go`

**Interfaces:**
- Consumes: `container`, `gitx`, `paths`, `state`, `tree`, `wkterr`.
- Produces:
  - `task.Blocker{Code, Repo, Path, Detail string}`.
  - `task.Preflight(c container.C, t state.Task) ([]Blocker, error)` — enumerates from the filesystem, never from state; walks real directories only and never descends link slots.
  - `task.Remove(c container.C, name string, force bool) error` — refuses while any blocker exists; with `force`, renames the whole tree into `staging/` first, then removes from there.

- [ ] **Step 1: Write the failing test**

```go
// internal/task/remove_test.go
package task

import (
	"os"
	"path/filepath"
	"testing"

	"wkt/internal/discover"
	"wkt/internal/state"
)

func TestRemoveRefusesOnIgnoredButPreciousFile(t *testing.T) {
	c, entries := fixture(t)
	// .env is gitignored, so git's own refusal never fires on it (spec H1).
	repo := filepath.Join(c.Workspace, "services", "svc-a")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, repo, "add", "-A")
	g(t, repo, "commit", "-qm", "ignore env")
	entries, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}

	task, err := Create(c, entries, "feat-x", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(task.Repos[0].WorktreePath, ".env")
	if err := os.WriteFile(envPath, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = Remove(c, "feat-x", false)
	if err == nil {
		t.Fatal("removal must refuse while an ignored-but-precious file exists")
	}
	if _, statErr := os.Stat(envPath); statErr != nil {
		t.Fatal("the refused removal must not have deleted anything")
	}
}

func TestPreflightSeesUnpushedCommits(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-y", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, wt, "add", "-A")
	g(t, wt, "commit", "-qm", "agent work")

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var sawUnpushed bool
	for _, b := range blockers {
		if b.Code == "WKT_UNPUSHED" {
			sawUnpushed = true
		}
	}
	if !sawUnpushed {
		t.Fatalf("an unpushed commit must block removal, got %+v", blockers)
	}
}

var _ = state.Task{}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/task/ -run TestRemove -v`
Expected: FAIL — `undefined: Remove`.

- [ ] **Step 3: Write the preflight**

```go
// internal/task/remove.go
package task

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"wkt/internal/container"
	"wkt/internal/gitx"
	"wkt/internal/paths"
	"wkt/internal/state"
	"wkt/internal/tree"
	"wkt/internal/wkterr"
)

type Blocker struct {
	Code   string
	Repo   string
	Path   string
	Detail string
}

var preciousPatterns = []string{".env", ".env.", "credentials", "id_rsa", ".pem"}

func isPrecious(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range preciousPatterns {
		if strings.HasPrefix(lower, p) || strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func Preflight(c container.C, t state.Task) ([]Blocker, error) {
	var out []Blocker
	treeRoot := c.TreePath(t.Name)

	for _, r := range t.Repos {
		wt := r.WorktreePath
		if _, err := os.Stat(wt); os.IsNotExist(err) {
			out = append(out, Blocker{Code: "WKT_WORKTREE_MISSING", Repo: r.RelPath, Path: wt})
			continue
		}
		// 1+2: uncommitted and untracked.
		if s, err := gitx.Run(wt, "status", "--porcelain"); err != nil {
			out = append(out, Blocker{Code: "WKT_CHECK_FAILED", Repo: r.RelPath, Path: wt})
		} else if s != "" {
			out = append(out, Blocker{Code: "WKT_DIRTY", Repo: r.RelPath, Path: wt, Detail: firstLine(s)})
		}
		// 3: ignored-but-precious. git's own refusal never fires on these (H1).
		if s, err := gitx.Run(wt, "status", "--porcelain", "--ignored=matching"); err == nil {
			for _, line := range strings.Split(s, "\n") {
				if !strings.HasPrefix(line, "!! ") {
					continue
				}
				name := filepath.Base(strings.TrimPrefix(line, "!! "))
				if isPrecious(name) {
					out = append(out, Blocker{Code: "WKT_PRECIOUS_IGNORED", Repo: r.RelPath, Path: name})
				}
			}
		}
		// 4: in-progress operations — invisible to status --porcelain (H2).
		for _, marker := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"} {
			p, err := gitx.Run(wt, "rev-parse", "--git-path", marker)
			if err != nil {
				continue
			}
			// --git-path may answer relatively or absolutely; --path-format needs
			// git 2.31 and our floor is 2.29, so normalise by hand.
			if !filepath.IsAbs(p) {
				p = filepath.Join(wt, p)
			}
			if _, statErr := os.Stat(p); statErr == nil {
				out = append(out, Blocker{Code: "WKT_IN_PROGRESS", Repo: r.RelPath, Detail: marker})
			}
		}
		// 5: unpushed commits, including the no-upstream and detached cases. The
		// recorded base is excluded as well as every remote ref: a bare store has
		// no refs/remotes/* until something fetches, so counting against remotes
		// alone would flag the whole history of a freshly created task.
		args := []string{"rev-list", "--count", "HEAD", "--not", "--remotes"}
		if r.BaseSHA != "" {
			args = append(args, r.BaseSHA)
		}
		if n, err := gitx.Run(wt, args...); err == nil && n != "" && n != "0" {
			out = append(out, Blocker{Code: "WKT_UNPUSHED", Repo: r.RelPath, Detail: n + " commit(s)"})
		}
		// 6: submodules — worktree remove refuses unconditionally, and --force
		// destroys their object store (spec §5.7).
		if sm, err := gitx.Run(wt, "submodule", "status", "--recursive"); err == nil && strings.TrimSpace(sm) != "" {
			out = append(out, Blocker{Code: "WKT_SUBMODULE", Repo: r.RelPath, Detail: firstLine(sm)})
		}
	}

	// 7: a foreign .git anywhere in the tree, found without following symlinks.
	known := map[string]bool{}
	for _, r := range t.Repos {
		canon, _ := paths.Canonical(r.WorktreePath)
		known[canon] = true
	}
	_ = filepath.WalkDir(treeRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fs.SkipDir
		}
		if d.Name() != ".git" {
			return nil
		}
		owner, _ := paths.Canonical(filepath.Dir(p))
		if !known[owner] {
			out = append(out, Blocker{Code: "WKT_FOREIGN_REPO", Path: filepath.Dir(p)})
		}
		return fs.SkipDir
	})

	// 8: link slots whose type changed, and copies that diverged (H12, §5.3 r5).
	for _, slot := range t.Links {
		p := filepath.Join(treeRoot, slot.RelPath)
		info, err := os.Lstat(p)
		if err != nil {
			out = append(out, Blocker{Code: "WKT_LINK_SLOT_MISSING", Path: slot.RelPath})
			continue
		}
		switch slot.Type {
		case "symlink":
			if info.Mode()&os.ModeSymlink == 0 {
				out = append(out, Blocker{Code: "WKT_LINK_SLOT_CHANGED", Path: slot.RelPath,
					Detail: "the link was replaced by a regular file"})
			}
		case "copy":
			if sum, err := tree.Hash(p); err != nil || sum != slot.Hash {
				out = append(out, Blocker{Code: "WKT_COPY_DIVERGED", Path: slot.RelPath})
			}
		}
	}
	return out, nil
}

func firstLine(s string) string { return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0] }
```

- [ ] **Step 4: Write the removal with the staging fence**

```go
// appended to internal/task/remove.go

func Remove(c container.C, name string, force bool) error {
	t, err := state.Load(c.StateDir(), name)
	if err != nil {
		return err
	}
	blockers, err := Preflight(c, t)
	if err != nil {
		return err
	}
	// A foreign repository is never removable, not even with --force: its history
	// exists nowhere else (spec §5.7).
	for _, b := range blockers {
		if b.Code == "WKT_FOREIGN_REPO" {
			return wkterr.New(b.Code, "a repository wkt did not create lives inside the tree").
				WithPath(b.Path).WithRemedy("move it out of the tree, then retry")
		}
		if b.Code == "WKT_SUBMODULE" && force {
			return wkterr.New(b.Code, "a submodule is present; --force would destroy its objects").
				WithRepo(b.Repo).WithRemedy("push the submodule's commits", "git submodule deinit", "then retry")
		}
	}
	if len(blockers) > 0 && !force {
		e := wkterr.New("WKT_WOULD_LOSE_WORK", "removal would lose work")
		for _, b := range blockers {
			e = e.WithRemedy(b.Code + " " + b.Repo + " " + b.Path + " " + b.Detail)
		}
		return e
	}

	// The fence: one rename moves the whole tree out of reach before anything is
	// deleted, so a still-running agent's cwd disappears atomically (spec §5.7).
	treeRoot := c.TreePath(name)
	staged := filepath.Join(c.StagingDir(), name)
	if err := os.MkdirAll(c.StagingDir(), 0o700); err != nil {
		return wkterr.New("WKT_STAGING", "cannot create the staging directory").WithPath(c.StagingDir())
	}
	if err := os.Rename(treeRoot, staged); err != nil {
		return wkterr.New("WKT_STAGING", "cannot move the tree into staging").
			WithPath(treeRoot).WithFound(err.Error()).
			WithRemedy("if staging is on another filesystem, relocate the container")
	}

	for _, r := range t.Repos {
		sp := filepath.Join(c.StoreDir(), r.StoreID+".git")
		_, _ = gitx.Run(sp, "worktree", "unlock", r.WorktreePath)
		_, _ = gitx.Run(sp, "worktree", "prune")
		_, _ = gitx.Run(r.AbsPath, "update-ref", "-d", r.BasePinRef)
	}
	// Deletion goes through os.RemoveAll on a wkt-computed path: never a shell
	// command, never a symlink-following walker (spec H3).
	if err := os.RemoveAll(staged); err != nil {
		return wkterr.New("WKT_REMOVE_FAILED", "cannot remove the staged tree").WithPath(staged)
	}
	if err := os.Remove(filepath.Join(c.StateDir(), name+".json")); err != nil && !os.IsNotExist(err) {
		return wkterr.New("WKT_STATE_WRITE", "cannot remove task state").WithPath(name)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/task/ -v`
Expected: all four tests in the package PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/task
git commit -m "feat: refuse-only teardown with staging fence"
```

---

### Task 10: CLI wiring — `init`, `new`, `path`, `status`, `rm`

**Files:**
- Create: `cmd/wkt/main.go`
- Create: `internal/cli/cli.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `cli.Run(args []string, stdout, stderr io.Writer) int` — the exit code contract: 0 consistent, 2 usage or task exists, 3 drift, 4 container missing, 1 any other typed failure.
  - Documented aliases `create` → `new` and `cleanup` → `rm`, because the acceptance battery drives those verbs (spec §7.1).

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/cli_test.go
package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func seedRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
		full := append([]string{"-c", "user.email=e@x", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
}

func TestInitNewPathRmRoundTrip(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "services", "svc-a"))
	seedRepo(t, filepath.Join(ws, "docs"))

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"new", "feat-42", "--workspace", ws, "--all"}, &out, &errb); code != 0 {
		t.Fatalf("new exited %d: %s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"path", "feat-42", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("path exited %d: %s", code, errb.String())
	}
	treePath := strings.TrimSpace(out.String())
	if _, err := os.Stat(filepath.Join(treePath, "services", "svc-a")); err != nil {
		t.Fatalf("path must point at a materialised mirrored tree: %v", err)
	}
	out.Reset()
	if code := Run([]string{"rm", "feat-42", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("rm on a clean task exited %d: %s", code, errb.String())
	}
	if _, err := os.Stat(treePath); !os.IsNotExist(err) {
		t.Fatal("rm must remove a clean tree")
	}
}

func TestNewOnExistingTaskExitsTwo(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))
	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	Run([]string{"new", "t1", "--workspace", ws, "--all"}, &out, &errb)
	if code := Run([]string{"new", "t1", "--workspace", ws, "--all"}, &out, &errb); code != 2 {
		t.Fatalf("a duplicate task must exit 2, got %d", code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -v`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Write the CLI**

```go
// internal/cli/cli.go
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"wkt/internal/container"
	"wkt/internal/discover"
	"wkt/internal/gitx"
	"wkt/internal/state"
	"wkt/internal/task"
	"wkt/internal/wkterr"
)

const usage = `wkt — one task, one branch, many repositories

  wkt init   [--workspace DIR] [--dry-run]
  wkt new    TASK [--workspace DIR] [--repos a,b | --all]   (alias: create)
  wkt path   TASK [--workspace DIR]
  wkt status [TASK] [--workspace DIR] [--json]
  wkt rm     TASK [--workspace DIR] [--force]               (alias: cleanup)
`

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if major, minor, err := gitx.Version(); err != nil || major < 2 || (major == 2 && minor < 29) {
		fmt.Fprintln(stderr, "WKT_GIT_TOO_OLD: git 2.29 or newer is required")
		return 1
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "create":
		cmd = "new"
	case "cleanup":
		cmd = "rm"
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	ws := fs.String("workspace", ".", "workspace directory")
	repos := fs.String("repos", "", "comma-separated workspace-relative repository paths")
	all := fs.Bool("all", false, "select every discovered repository")
	force := fs.Bool("force", false, "remove even though work would be lost")
	dryRun := fs.Bool("dry-run", false, "report without writing anything")

	var positional string
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		positional, rest = rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}

	c, err := container.Locate(*ws)
	if err != nil {
		return fail(stderr, err)
	}

	switch cmd {
	case "init":
		entries, err := discover.Walk(c.Workspace, 4)
		if err != nil {
			return fail(stderr, err)
		}
		if pairs := discover.NestedPairs(entries); len(pairs) > 0 {
			e := wkterr.New("WKT_NESTED_REPO", "nested repositories are not supported")
			for _, p := range pairs {
				e = e.WithRemedy(p[0] + " is inside " + p[1])
			}
			return fail(stderr, e)
		}
		for _, e := range entries {
			if e.Kind == discover.KindRepo {
				fmt.Fprintln(stdout, e.RelPath)
			}
		}
		if *dryRun {
			return 0
		}
		if err := container.Create(c); err != nil {
			return fail(stderr, err)
		}
		return 0

	case "new":
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		release, err := container.Lock(c)
		if err != nil {
			return fail(stderr, err)
		}
		defer release()
		entries, err := discover.Walk(c.Workspace, 4)
		if err != nil {
			return fail(stderr, err)
		}
		selected := selection(entries, *repos, *all)
		t, err := task.Create(c, entries, positional, selected)
		if err != nil {
			return fail(stderr, err) // fail() maps WKT_TASK_EXISTS to 2
		}
		fmt.Fprintln(stdout, c.TreePath(t.Name))
		return 0

	case "path":
		if _, err := state.Load(c.StateDir(), positional); err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, c.TreePath(positional))
		return 0

	case "status":
		names, _ := state.List(c.StateDir())
		if positional != "" {
			names = []string{positional}
		}
		drift := false
		for _, n := range names {
			t, err := state.Load(c.StateDir(), n)
			if err != nil {
				return fail(stderr, err)
			}
			blockers, err := task.Preflight(c, t)
			if err != nil {
				return fail(stderr, err)
			}
			fmt.Fprintf(stdout, "%s  base epoch %s\n", t.Name, t.BaseEpoch.Format("2006-01-02 15:04:05Z"))
			for _, r := range t.Repos {
				fmt.Fprintf(stdout, "  %-28s %s\n", r.RelPath, r.Branch)
			}
			for _, b := range blockers {
				drift = true
				fmt.Fprintf(stdout, "  ! %-20s %s %s %s\n", b.Code, b.Repo, b.Path, b.Detail)
			}
		}
		if drift {
			return 3
		}
		return 0

	case "rm":
		release, err := container.Lock(c)
		if err != nil {
			return fail(stderr, err)
		}
		defer release()
		if err := task.Remove(c, positional, *force); err != nil {
			return fail(stderr, err)
		}
		return 0
	}

	fmt.Fprint(stderr, usage)
	return 2
}

func selection(entries []discover.Entry, repos string, all bool) []string {
	if repos != "" {
		return strings.Split(repos, ",")
	}
	var out []string
	for _, e := range entries {
		if e.Kind == discover.KindRepo {
			out = append(out, e.RelPath)
		}
	}
	return out // --all is the default when neither flag is given (spec §6)
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, string(wkterr.JSON(err)))
	if e, ok := err.(*wkterr.E); ok {
		switch e.Code {
		case "WKT_TASK_EXISTS":
			return 2
		case "WKT_NO_CONTAINER":
			return 4
		}
	}
	return 1
}

var _ = os.Exit
```

- [ ] **Step 4: Write the entry point**

```go
// cmd/wkt/main.go
package main

import (
	"os"

	"wkt/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
```

- [ ] **Step 5: Run the tests and build**

```bash
go test ./... -v && go build -o /tmp/wkt ./cmd/wkt && /tmp/wkt
```
Expected: all tests PASS; the bare binary prints usage and exits 2.

- [ ] **Step 6: Commit**

```bash
git add cmd internal/cli
git commit -m "feat: CLI wiring for init, new, path, status and rm"
```

---

### Task 11: Acceptance battery — the mechanical half

**Files:**
- Create: `test/lib.sh`
- Create: `test/01_nested_discovery.sh`
- Create: `test/02_isolation.sh`
- Create: `test/03_destructive_cleanup.sh`
- Create: `test/06_commit_under_readonly_workspace.sh`
- Create: `test/07_store_origin.sh`
- Create: `test/20m_two_tasks_one_repo.sh`
- Create: `test/run.sh`

**Interfaces:**
- Consumes: the built binary via `WT_CMD`.
- Produces: a runner that exits non-zero if any script fails. Scripts are bash 3.2 compatible, because macOS ships `/bin/bash` 3.2.

- [ ] **Step 1: Write the shared helpers**

```bash
# test/lib.sh
#!/usr/bin/env bash
set -u
: "${WT_CMD:?set WT_CMD to the wkt binary}"

PASSES=0; FAILURES=0
pass() { PASSES=$((PASSES+1)); printf '  PASS  %s\n' "$*"; }
fail() { FAILURES=$((FAILURES+1)); printf '  FAIL  %s\n' "$*"; }
assert_eq() { if [ "$2" = "$3" ]; then pass "$1"; else fail "$1 — got '$2', want '$3'"; fi; }
assert_file() { if [ -e "$2" ]; then pass "$1"; else fail "$1 — missing: $2"; fi; }
assert_no_file() { if [ -e "$2" ]; then fail "$1 — still present: $2"; else pass "$1"; fi; }

G() { git -c user.email=b@x.invalid -c user.name=b -c init.defaultBranch=main "$@"; }

mk_repo() {
  local rel="$1" name bare seed
  name="$(basename "$rel")"; bare="$REMOTES/$name.git"; seed="$TMP/seed-$name"
  G init -q --bare "$bare"
  rm -rf "$seed"; mkdir -p "$seed/src"
  printf "console.log('%s');\n" "$name" > "$seed/src/index.js"
  printf '.env\ndist/\n' > "$seed/.gitignore"
  ( cd "$seed" && G init -q && G add -A && G commit -qm init && G branch -M main \
      && G remote add origin "$bare" && G push -q -u origin main ) || return 1
  rm -rf "$seed"
  mkdir -p "$(dirname "$WS/$rel")"
  G clone -q "$bare" "$WS/$rel"
}

wt_init_env() {
  TESTDIR="$(mktemp -d "${TMPDIR:-/tmp}/wkt.XXXXXX")"
  REMOTES="$TESTDIR/remotes"; TMP="$TESTDIR/tmp"; WS="$TESTDIR/workspace"
  mkdir -p "$REMOTES" "$TMP" "$WS"
}
wt_cleanup_env() { [ -n "${TESTDIR:-}" ] && [ -d "$TESTDIR" ] && rm -rf "$TESTDIR"; }

wt() { ( cd "$WS" && "$WT_CMD" "$@" --workspace "$WS" ) ; }
wt_task_dir() { ( cd "$WS" && "$WT_CMD" path "$1" --workspace "$WS" 2>/dev/null | tail -1 ); }

# path_has_symlink BASE REL — true if any component below BASE is a symlink.
path_has_symlink() {
  local base="$1" rel="$2" acc="$1" part
  local IFS='/'
  for part in $rel; do
    [ -z "$part" ] && continue
    acc="$acc/$part"
    [ -L "$acc" ] && return 0
  done
  return 1
}

summary() { printf '\n-- %s: %d passed, %d failed\n' "${1:-test}" "$PASSES" "$FAILURES"; [ "$FAILURES" -eq 0 ]; }
```

- [ ] **Step 2: Write test 01 — nested discovery, no symlinked path component**

```bash
# test/01_nested_discovery.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
for r in services/svc-a services/svc-b docs shared; do mk_repo "$r" || exit 1; done
mkdir -p "$WS/notes"; echo scratch > "$WS/notes/s.md"; echo '# conv' > "$WS/CONVENTIONS.md"

wt init >/dev/null || { fail "init"; summary 01; exit 1; }
wt new task-1 --all >/dev/null || { fail "new"; summary 01; exit 1; }
TD="$(wt_task_dir task-1)"

for r in services/svc-a services/svc-b docs shared; do
  assert_file "$r materialised" "$TD/$r"
  if path_has_symlink "$TD" "$r"; then fail "$r: a path component is a symlink"; else pass "$r: no symlinked path component"; fi
  assert_eq "$r on the task branch" "$(cd "$TD/$r" && git rev-parse --abbrev-ref HEAD)" "task-1"
done
assert_file "non-git directory carried" "$TD/notes"
assert_file "loose file carried" "$TD/CONVENTIONS.md"
summary 01
```

- [ ] **Step 3: Write test 02 — isolation, and test 03 — destructive cleanup**

```bash
# test/02_isolation.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1
wt init >/dev/null; wt new task-2 --all >/dev/null
TD="$(wt_task_dir task-2)"

echo "edited in the task" >> "$TD/services/svc-a/src/index.js"
assert_eq "original stays clean after a task-tree edit" \
  "$(cd "$WS/services/svc-a" && git status --porcelain)" ""
assert_eq "original stays on its own branch" \
  "$(cd "$WS/services/svc-a" && git rev-parse --abbrev-ref HEAD)" "main"
summary 02
```

```bash
# test/03_destructive_cleanup.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo docs || exit 1
wt init >/dev/null; wt new task-3 --all >/dev/null
TD="$(wt_task_dir task-3)"

# Four classes of work, one of which git itself never protects (spec H1).
echo "TOKEN=secret" > "$TD/docs/.env"
echo "draft" > "$TD/docs/untracked.md"
echo "change" >> "$TD/docs/src/index.js"
( cd "$TD/docs" && G add -A && G commit -qm "unpushed" >/dev/null )

wt rm task-3 >/dev/null 2>&1
assert_eq "plain rm refuses" "$?" "1"
assert_file "ignored .env preserved" "$TD/docs/.env"
assert_file "untracked file preserved" "$TD/docs/untracked.md"
assert_file "tree still present" "$TD/docs"
summary 03
```

- [ ] **Step 4: Write tests 06, 07 and 20m — the store guarantees**

```bash
# test/06_commit_under_readonly_workspace.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo svc-a || exit 1
wt init >/dev/null; wt new task-6 --all >/dev/null
TD="$(wt_task_dir task-6)"

chmod -R a-w "$WS/svc-a"
echo "agent change" >> "$TD/svc-a/src/index.js"
( cd "$TD/svc-a" && G add -A && G commit -qm "agent commit" >/dev/null 2>&1 )
RC=$?
chmod -R u+w "$WS/svc-a"
assert_eq "commit succeeds with the workspace read-only" "$RC" "0"
summary 06
```

```bash
# test/07_store_origin.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo svc-a || exit 1
wt init >/dev/null; wt new task-7 --all >/dev/null
TD="$(wt_task_dir task-7)"

WANT="$(cd "$WS/svc-a" && git remote get-url origin)"
# --path-format needs git 2.31; our floor is 2.29, so resolve by hand.
STORE="$(cd "$TD/svc-a" && cd "$(git rev-parse --git-common-dir)" && pwd)"
GOT="$(git -C "$STORE" remote get-url origin)"
assert_eq "store origin equals the workspace origin" "$GOT" "$WANT"
( cd "$TD/svc-a" && G push -q -u origin task-7 ) && pass "push from the tree reaches the real remote" \
  || fail "push from the tree reaches the real remote"
assert_eq "the branch landed on the remote" \
  "$(git -C "$REMOTES/svc-a.git" rev-parse --verify --quiet task-7 >/dev/null && echo yes)" "yes"
summary 07
```

```bash
# test/20m_two_tasks_one_repo.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo svc-a || exit 1
wt init >/dev/null
wt new task-a --all >/dev/null; wt new task-b --all >/dev/null
TA="$(wt_task_dir task-a)"; TB="$(wt_task_dir task-b)"

echo "A" >> "$TA/svc-a/src/index.js"
( cd "$TA/svc-a" && G add -A && G commit -qm "A change" >/dev/null )
assert_eq "task B does not see task A's commit" \
  "$(cd "$TB/svc-a" && git log --oneline | wc -l | tr -d ' ')" "1"
assert_eq "task B stays clean" "$(cd "$TB/svc-a" && git status --porcelain)" ""
assert_eq "the workspace stays on its own branch" \
  "$(cd "$WS/svc-a" && git rev-parse --abbrev-ref HEAD)" "main"

( cd "$TA/svc-a" && G push -q -u origin task-a >/dev/null 2>&1 )
wt rm task-a >/dev/null 2>&1
assert_no_file "task A removed" "$TA"
assert_file "task B still works" "$TB/svc-a"
assert_eq "task B still on its branch" "$(cd "$TB/svc-a" && git rev-parse --abbrev-ref HEAD)" "task-b"
summary 20m
```

- [ ] **Step 5: Write the runner and run the whole battery**

```bash
# test/run.sh
#!/usr/bin/env bash
set -u
: "${WT_CMD:?set WT_CMD to the wkt binary}"
dir="$(cd "$(dirname "$0")" && pwd)"
rc=0
for t in "$dir"/[0-9]*.sh; do
  printf '\n== %s ==\n' "$(basename "$t")"
  bash "$t" || rc=1
done
exit $rc
```

```bash
chmod +x test/*.sh
go build -o /tmp/wkt ./cmd/wkt && WT_CMD=/tmp/wkt test/run.sh
```
Expected: every script reports `0 failed`.

- [ ] **Step 6: Commit**

```bash
git add test
git commit -m "test: mechanical acceptance battery"
```

---

## What this plan deliberately leaves out

`add`, `fetch`, `sync`, `repair`, `doctor`, the perimeter generator and the
`WorktreeCreate` hook are the second plan. Salvage refs, quarantine, `push`/`pr`,
`adopt` and `wkt run` are out of v0 entirely (spec §6, §9).
