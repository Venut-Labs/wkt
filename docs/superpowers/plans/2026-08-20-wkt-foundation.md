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
- **Every check fails closed.** A check whose git call errors blocks; "cannot
  tell" is treated as "would lose work" (spec §5.7). This is a rule about the
  error path, which is exactly where it gets forgotten.
- **Every symlink `wkt` is about to create has its target resolved and refused
  if it contains a repository at any depth** (spec §5.3 rule 4). Enumeration is
  depth-bounded; this scan is not, and it is what stops a repository below the
  bound being shared writable by every task.
- **No placeholder imports.** `var _ = fmt.Sprintf` to prop up an import that is
  no longer used means the import should have been deleted.

---

### Task 1: Module skeleton, git wrapper, typed errors

**Files:**
- Create: `go.mod`
- Create: `internal/wkterr/wkterr.go`
- Create: `internal/gitx/gitx.go`
- Test: `internal/gitx/gitx_test.go`
- Test: `internal/wkterr/wkterr_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `wkterr.E{Code, Message, Repo, Path, Expected, Found, Remedy []string}` implementing `error`; constructor `wkterr.New(code, msg string) *E`; methods `WithRepo(string) *E`, `WithPath(string) *E`, `WithExpected(string) *E`, `WithFound(string) *E`, `WithRemedy(...string) *E`; `wkterr.JSON(err error) []byte`.
  - `wkterr.Problem{Code, Repo, Path, Detail string, Info bool}` with `WithProblem(Problem) *E` — what is in the way, kept apart from `Remedy`, which is what to do about it. The zero value **blocks**: a caller who forgets the flag fails closed.
  - `gitx.Run(dir string, args ...string) (string, error)` — trimmed stdout, or a `*wkterr.E` with code `WKT_GIT_FAILED` whose `Found` is the **first line** of stderr.
  - `gitx.RunOK(dir string, args ...string) bool`.
  - `gitx.Version() (major, minor int, err error)`.

**Traps** — every one of these was hit while executing this plan; the code and
tests below already avoid them, so do not "simplify" them back:

- The module must exist before any `go test` runs, hence step 1.
- A stderr-truncation test needs a command whose stderr is genuinely
  **multi-line** (`git checkout --nonexistent-flag`); one that emits a single
  line passes whether or not truncation happens.
- Test the version **parser** over a table of strings, not the installed git.
  A test that skips when the parser returns zeros asserts nothing.
- Do not prop up an unused import with `var _ = fmt.Sprintf`. Remove the import.

- [ ] **Step 1: Create the module**

```bash
go mod init wkt
```

Nothing below can run before this: `go test` needs a module, so the
verify-it-fails step is unreachable until `go.mod` exists.

- [ ] **Step 2: Write the failing test**

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
	// Initialize a git repo so the command starts, but use an invalid flag to trigger multi-line stderr
	if _, err := Run(dir, "init", "-q"); err != nil {
		t.Fatalf("init: %v", err)
	}
	// git checkout --nonexistent-flag produces multiple lines of stderr:
	// "error: unknown option `nonexistent-flag'\nusage: git checkout ..."
	_, err := Run(dir, "checkout", "--nonexistent-flag")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "WKT_GIT_FAILED") {
		t.Fatalf("error %q does not carry the code", msg)
	}
	// Error message must be single line
	if strings.Count(msg, "\n") > 0 {
		t.Fatalf("error must be a single line, got %q", msg)
	}
	// Error message must contain first line of stderr (starting with "error:")
	if !strings.Contains(msg, "error:") {
		t.Fatalf("error should contain first stderr line, got %q", msg)
	}
	// Error message must NOT contain usage lines (later lines of stderr)
	if strings.Contains(msg, "usage:") {
		t.Fatalf("error should not contain later stderr lines, got %q", msg)
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input   string
		wantMaj int
		wantMin int
		wantErr bool
	}{
		{"git version 2.50.1", 2, 50, false},
		{"git version 2.50.1 (Apple Git-155)", 2, 50, false},
		{"git version 2.29.0", 2, 29, false},
		{"git version 2.29", 2, 29, false},
		{"git version 3.0.0", 3, 0, false},
		{"git version", 0, 0, true},          // not enough fields
		{"git version abc", 0, 0, true},      // invalid major
		{"git version 2.abc.0", 2, 0, false}, // invalid minor defaults to 0
		{"malformed output", 0, 0, true},     // not enough fields
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			maj, min, err := parseVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseVersion(%q): got err=%v, want err=%v", tt.input, err != nil, tt.wantErr)
			}
			if !tt.wantErr {
				if maj != tt.wantMaj || min != tt.wantMin {
					t.Errorf("parseVersion(%q): got (%d, %d), want (%d, %d)", tt.input, maj, min, tt.wantMaj, tt.wantMin)
				}
			}
		})
	}
}
```

The typed error carries its own test, because the split between `problems` and `remedy` is a contract the rest of the tool depends on:

```go
// internal/wkterr/wkterr_test.go
package wkterr

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProblemsAreCarriedSeparatelyFromRemedy covers adversarial finding F5.
// A refusal has two different things to say — what is in the way, and what to
// do about it — and folding the first into the second left "remedy" holding a
// list of problems and no action at all.
func TestProblemsAreCarriedSeparatelyFromRemedy(t *testing.T) {
	e := New("WKT_WOULD_LOSE_WORK", "removal would lose work").
		WithProblem(Problem{Code: "WKT_DIRTY", Repo: "svc-a", Detail: "2 modified paths"}).
		WithProblem(Problem{Code: "WKT_REGENERABLE_IGNORED", Repo: "svc-a", Path: "dist/", Info: true}).
		WithRemedy("commit or stash the changes, then retry")

	var got struct {
		Problems []Problem `json:"problems"`
		Remedy   []string  `json:"remedy"`
	}
	if err := json.Unmarshal(JSON(e), &got); err != nil {
		t.Fatalf("error JSON must parse: %v", err)
	}
	if len(got.Problems) != 2 {
		t.Fatalf("want both problems, got %+v", got.Problems)
	}
	if got.Problems[0].Info {
		t.Fatal("a problem blocks unless it says otherwise — the zero value must fail closed")
	}
	if !got.Problems[1].Info {
		t.Fatal("an informational problem must not read as blocking")
	}
	if len(got.Remedy) != 1 || strings.Contains(got.Remedy[0], "WKT_") {
		t.Fatalf("remedy must hold actions, not problem codes: %v", got.Remedy)
	}
}

// TestErrorStaysOneLine pins the constraint the whole type exists for: no
// error surface may span lines, because raw git stderr must never reach it.
func TestErrorStaysOneLine(t *testing.T) {
	e := New("WKT_X", "boom").WithProblem(Problem{Code: "WKT_DIRTY", Detail: "a\nb"})
	if strings.Contains(e.Error(), "\n") {
		t.Fatalf("Error() must stay single-line, got %q", e.Error())
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/gitx/ -run TestRun -v`
Expected: FAIL — the package does not compile, `undefined: Run`.

- [ ] **Step 4: Write the typed error**

```go
// internal/wkterr/wkterr.go
package wkterr

import (
	"encoding/json"
	"fmt"
	"strings"
)

type E struct {
	Code     string    `json:"code"`
	Message  string    `json:"message"`
	Repo     string    `json:"repo,omitempty"`
	Path     string    `json:"path,omitempty"`
	Expected string    `json:"expected,omitempty"`
	Found    string    `json:"found,omitempty"`
	Problems []Problem `json:"problems,omitempty"`
	Remedy   []string  `json:"remedy,omitempty"`
}

// Problem is one thing standing in the way, as opposed to Remedy, which is
// what to do about it. Keeping them apart is the whole point: a refusal that
// lists its blockers under "remedy" tells the user what is wrong twice and
// what to do never.
type Problem struct {
	Code   string
	Repo   string
	Path   string
	Detail string
	// Info marks a problem that is reported but does not block. The zero
	// value therefore blocks, so a caller who forgets the field fails
	// closed — the same rule the teardown checks follow.
	Info bool
}

// MarshalJSON writes the positive form ("blocking": true) while the Go field
// stays the negative one, so the zero value keeps failing closed.
func (p Problem) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Code     string `json:"code"`
		Repo     string `json:"repo,omitempty"`
		Path     string `json:"path,omitempty"`
		Detail   string `json:"detail,omitempty"`
		Blocking bool   `json:"blocking"`
	}{p.Code, p.Repo, p.Path, p.Detail, !p.Info})
}

// UnmarshalJSON is the inverse, so a caller can round-trip an error.
func (p *Problem) UnmarshalJSON(b []byte) error {
	var raw struct {
		Code     string `json:"code"`
		Repo     string `json:"repo,omitempty"`
		Path     string `json:"path,omitempty"`
		Detail   string `json:"detail,omitempty"`
		Blocking bool   `json:"blocking"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = Problem{Code: raw.Code, Repo: raw.Repo, Path: raw.Path, Detail: raw.Detail, Info: !raw.Blocking}
	return nil
}

func New(code, msg string) *E { return &E{Code: code, Message: msg} }

func (e *E) Error() string {
	if e.Found != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Found)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *E) WithRepo(r string) *E      { e.Repo = r; return e }
func (e *E) WithPath(p string) *E      { e.Path = p; return e }
func (e *E) WithFound(f string) *E     { e.Found = f; return e }
func (e *E) WithExpected(x string) *E  { e.Expected = x; return e }
func (e *E) WithRemedy(r ...string) *E { e.Remedy = append(e.Remedy, r...); return e }

// WithProblem records one blocker. Detail is flattened to a single line here
// rather than at every call site, because the callers feed it git output.
func (e *E) WithProblem(p Problem) *E {
	p.Detail = strings.Join(strings.Fields(p.Detail), " ")
	e.Problems = append(e.Problems, p)
	return e
}

func JSON(err error) []byte {
	if err == nil {
		b, _ := json.Marshal(&E{Code: "WKT_INTERNAL", Message: ""})
		return b
	}
	if e, ok := err.(*E); ok {
		b, _ := json.Marshal(e)
		return b
	}
	b, _ := json.Marshal(&E{Code: "WKT_INTERNAL", Message: err.Error()})
	return b
}
```

- [ ] **Step 5: Write the git wrapper**

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
		stderr := strings.TrimSpace(errb.String())
		if stderr == "" {
			// Fallback to exec error when stderr is empty
			stderr = err.Error()
		}
		first := strings.SplitN(stderr, "\n", 2)[0]
		return "", wkterr.New("WKT_GIT_FAILED", "git "+args[0]+" failed").
			WithPath(dir).WithFound(first)
	}
	return strings.TrimSpace(out.String()), nil
}

func RunOK(dir string, args ...string) bool {
	_, err := Run(dir, args...)
	return err == nil
}

func parseVersion(out string) (int, int, error) {
	fields := strings.Fields(out) // "git version 2.50.1"
	if len(fields) < 3 {
		return 0, 0, wkterr.New("WKT_GIT_VERSION", "cannot parse git version").WithFound(out)
	}
	parts := strings.Split(fields[2], ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, wkterr.New("WKT_GIT_VERSION", "cannot parse git version").WithFound(out)
	}
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major, minor, nil
}

func Version() (int, int, error) {
	out, err := Run(".", "--version")
	if err != nil {
		return 0, 0, err
	}
	return parseVersion(out)
}
```

- [ ] **Step 6: Run the tests**

```bash
go test ./internal/... -v
```
Expected: all three tests PASS.

- [ ] **Step 7: Commit**

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
- Consumes: nothing — path arithmetic has no errors to type.
- Produces:
  - `paths.Canonical(p string) (string, error)` — absolute, symlinks resolved. Resolves the **nearest existing ancestor** and re-appends the missing tail, so a path that does not exist yet canonicalises the same way its parent does.
  - `paths.Spellings(p string) []string` — every spelling of one path: as given (absolutised), canonical, and on macOS the `/private`-prefixed and `/private`-stripped forms. Deduplicated, stable order.
  - `paths.IsUnder(child, parent string) bool` — canonical containment, no lexical prefix bugs (`/a/bc` is not under `/a/b`).

**Traps:**

- `filepath.EvalSymlinks` fails on a path that does not exist yet, and returning
  the input unchanged is not a fallback: on macOS `$TMPDIR` lives under `/var`,
  a symlink to `/private/var`, so an existing path fully resolves while its
  non-existent child does not — and `IsUnder` then compares mismatched roots.
  Resolve the nearest existing ancestor and re-append the tail.
- The two multi-level non-existent cases below are the regression guards for it.

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

func TestIsUnderMultiLevelNonexistent(t *testing.T) {
	base := t.TempDir()
	b := filepath.Join(base, "b")
	if err := os.Mkdir(b, 0o755); err != nil {
		t.Fatal(err)
	}
	// x and y both don't exist
	if !IsUnder(filepath.Join(b, "x", "y"), b) {
		t.Fatal("b/x/y must be under b even though x and y don't exist")
	}
}

func TestIsUnderMultiLevelNonexistentThroughSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "x", "y")
	// Neither x nor y exist; resolution must recursively walk up through the symlink
	if !IsUnder(target, real) {
		t.Fatal("link/x/y must be under real when link -> real, even though x and y don't exist")
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
	if err == nil {
		return resolved, nil
	}
	// Path doesn't exist; try to resolve parent directories recursively
	parent := filepath.Dir(abs)
	if parent == abs {
		// We're at the root
		return abs, nil
	}
	resolvedParent, _ := Canonical(parent)
	// Return the parent with the original filename appended
	return filepath.Join(resolvedParent, filepath.Base(abs)), nil
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
Expected: all four tests PASS.

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
- Consumes: `paths`.
- Produces:
  - `discover.Kind` with constants `KindRepo`, `KindLinkedWorktree`, `KindSubmodule`, `KindNested`.
  - `discover.Entry{RelPath, AbsPath string; Kind Kind; ContainedBy string}`.
  - `discover.Walk(workspace string, maxDepth int) ([]Entry, error)` — enumerates `.git` markers as **file or directory**, never follows symlinks, classifies each per spec §5.3 rule 6. A linked worktree and a submodule are told apart **structurally** — by a file named `gitdir` inside the admin directory the marker points at — never by substring-matching the target.
  - `discover.NestedPairs(entries []Entry) [][2]string` — repository pairs where one contains the other.

**Traps:**

- `fs.SkipDir` returned for a **symlink** entry skips the symlink's *siblings*,
  not its subtree — a symlink `DirEntry` reports `IsDir() == false`, so the walk
  treats the request as "skip the rest of this directory". Repositories sorting
  after a symlink vanish. The same trap appears twice: at the symlink check and
  at the `.git`-as-file check.
- `>= maxDepth` discovers only `maxDepth - 1` directory segments. Off by one.
- Substring-matching `worktrees` in the `gitdir:` target misclassifies a
  submodule whose own path happens to contain a `worktrees` segment. The
  structural discriminator is a file named `gitdir` inside the admin directory.
- `classify`'s tail must not have two arms both returning `KindRepo`; the second
  spawns a git subprocess whose answer is discarded.
- `markNested` must report the **nearest** containing repository. Computing it
  by mutating the entry while iterating reports the outermost ancestor instead.
- `Kind`'s zero value **is** `KindRepo`, so `found[key] != KindRepo` is false for
  a key that was never discovered: a test written that way is blind to total
  non-discovery. Assert presence first, then the kind.

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
	// Check services/svc-a exists and is a repo
	if v, ok := found["services/svc-a"]; !ok {
		t.Fatalf("services/svc-a not discovered: %v", found)
	} else if v != KindRepo {
		t.Fatalf("services/svc-a not classified as repo: %v", found)
	}
	// Check docs exists and is a repo
	if v, ok := found["docs"]; !ok {
		t.Fatalf("docs not discovered: %v", found)
	} else if v != KindRepo {
		t.Fatalf("docs not classified as repo: %v", found)
	}
	// Check notes is not reported
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
	found := false
	for _, e := range entries {
		if e.RelPath == "svc-a-wt" {
			found = true
			if e.Kind != KindLinkedWorktree {
				t.Fatalf("linked worktree classified as %v, want KindLinkedWorktree", e.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("linked worktree not discovered")
	}
}

func TestWalkDoesNotSkipSiblingsAfterSymlink(t *testing.T) {
	ws := t.TempDir()
	// Create a symlink at "aaa-link" pointing to a directory
	linkTarget := filepath.Join(ws, "target")
	if err := os.MkdirAll(linkTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "aaa-link")
	if err := os.Symlink(linkTarget, link); err != nil {
		t.Fatal(err)
	}
	// Create a real repo at "zzz-repo" after the symlink (lexically)
	gitInit(t, filepath.Join(ws, "zzz-repo"))
	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.RelPath == "zzz-repo" {
			found = true
			if e.Kind != KindRepo {
				t.Fatalf("zzz-repo misclassified as %v", e.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("zzz-repo not discovered after symlink: %v", entries)
	}
}

func TestWalkRespectsDepthBoundary(t *testing.T) {
	ws := t.TempDir()
	gitInit(t, filepath.Join(ws, "a", "b", "c"))
	gitInit(t, filepath.Join(ws, "a", "b", "c", "d"))

	// With maxDepth=3, a/b/c should be found but a/b/c/d should not
	entries, err := Walk(ws, 3)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Kind{}
	for _, e := range entries {
		found[e.RelPath] = e.Kind
	}

	// a/b/c is 3 levels deep (a=1, b=2, c=3), should be found
	if v, ok := found["a/b/c"]; !ok {
		t.Fatalf("a/b/c not discovered: %v", found)
	} else if v != KindRepo {
		t.Fatalf("a/b/c not classified as repo: %v", found)
	}

	// a/b/c/d is 4 levels deep, should not be found
	if _, ok := found["a/b/c/d"]; ok {
		t.Fatalf("a/b/c/d should not be discovered with maxDepth=3: %v", found)
	}
}

func TestWalkClassifiesSubmoduleWithWorktreesInPath(t *testing.T) {
	ws := t.TempDir()
	repo := filepath.Join(ws, "main-repo")
	gitInit(t, repo)

	// Create a separate submodule repository outside the main repo
	submoduleSource := filepath.Join(ws, "external-submodule")
	gitInit(t, submoduleSource)

	// Create a commit in the submodule
	if err := os.WriteFile(filepath.Join(submoduleSource, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=e@x", "-c", "user.name=t"}, args...)...)
		cmd.Dir = submoduleSource
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}

	// Add it as a submodule to the main repo in a path containing "worktrees"
	cmd := exec.Command("git", "-c", "protocol.file.allow=always", "submodule", "add", "-q", submoduleSource, "vendor/worktrees/mylib")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("submodule add: %s", out)
	}

	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, e := range entries {
		if e.RelPath == "main-repo/vendor/worktrees/mylib" {
			found = true
			if e.Kind != KindSubmodule {
				t.Fatalf("submodule misclassified as %v, want KindSubmodule", e.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("submodule not discovered: entries=%v", entries)
	}
}

// TestWalkFindsNestedRepoInsideLinkedWorktree is a regression guard for the
// same fs.WalkDir SkipDir asymmetry fixed in internal/task/remove.go's
// foreign-repo walk (round 2 review): a linked worktree's own ".git" is
// always a regular *file*, not a directory, so returning SkipDir
// unconditionally on it (rather than only when d.IsDir()) skipped the rest
// of that worktree's own siblings — hiding any nested repository sorting
// after ".git" (almost anything) from discovery entirely. Narrower blast
// radius than the remove.go instance (it truncates within one repository's
// own subtree, not across the whole tree root), but the same shape.
func TestWalkFindsNestedRepoInsideLinkedWorktree(t *testing.T) {
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
	// A repository nested inside the linked worktree's own directory,
	// sorting after ".git" alphabetically — the condition the bug depended
	// on, since ".git" is visited first in a sorted directory listing.
	nested := filepath.Join(wt, "zzz-nested")
	gitInit(t, nested)

	entries, err := Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	var sawWorktree, sawNested bool
	for _, e := range entries {
		if e.RelPath == "svc-a-wt" {
			sawWorktree = true
		}
		if e.RelPath == "svc-a-wt/zzz-nested" {
			sawNested = true
		}
	}
	if !sawWorktree {
		t.Fatalf("the linked worktree itself must still be discovered: %v", entries)
	}
	if !sawNested {
		t.Fatalf("a repository nested inside a linked worktree's own directory must be discovered, got %+v", entries)
	}
}

func TestWalkFindsNearestContainingRepository(t *testing.T) {
	ws := t.TempDir()
	gitInit(t, filepath.Join(ws, "outer"))
	gitInit(t, filepath.Join(ws, "outer", "middle"))
	gitInit(t, filepath.Join(ws, "outer", "middle", "inner"))

	entries, err := Walk(ws, 5)
	if err != nil {
		t.Fatal(err)
	}

	// Build a map for easy lookup
	found := make(map[string]*Entry)
	for i, e := range entries {
		found[e.RelPath] = &entries[i]
	}

	// Verify outer is a repo
	if v, ok := found["outer"]; !ok {
		t.Fatalf("outer not discovered")
	} else if v.Kind != KindRepo {
		t.Fatalf("outer not classified as repo")
	}

	// Verify middle is nested inside outer, not outer/middle
	if v, ok := found["outer/middle"]; !ok {
		t.Fatalf("outer/middle not discovered")
	} else if v.Kind != KindNested {
		t.Fatalf("outer/middle not classified as nested: %v", v.Kind)
	} else if v.ContainedBy != "outer" {
		t.Fatalf("outer/middle.ContainedBy=%q, want outer", v.ContainedBy)
	}

	// Verify inner is nested inside middle, not outer
	if v, ok := found["outer/middle/inner"]; !ok {
		t.Fatalf("outer/middle/inner not discovered")
	} else if v.Kind != KindNested {
		t.Fatalf("outer/middle/inner not classified as nested: %v", v.Kind)
	} else if v.ContainedBy != "outer/middle" {
		t.Fatalf("outer/middle/inner.ContainedBy=%q, want outer/middle", v.ContainedBy)
	}

	// Verify NestedPairs is correct
	pairs := NestedPairs(entries)
	expectedPairs := [][2]string{
		{"outer/middle", "outer"},
		{"outer/middle/inner", "outer/middle"},
	}
	if len(pairs) != len(expectedPairs) {
		t.Fatalf("NestedPairs returned %d pairs, want %d: %v", len(pairs), len(expectedPairs), pairs)
	}
	pairMap := make(map[[2]string]bool)
	for _, p := range pairs {
		pairMap[p] = true
	}
	for _, ep := range expectedPairs {
		if !pairMap[ep] {
			t.Fatalf("NestedPairs missing expected pair %v", ep)
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
		if strings.Count(rel, string(filepath.Separator)) > maxDepth {
			return fs.SkipDir
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil // never follow symlinks (spec §5.3 rule 4), skip only this entry
		}
		if d.Name() != ".git" {
			return nil
		}
		repoDir := filepath.Dir(p)
		repoRel, _ := filepath.Rel(root, repoDir)
		e := Entry{RelPath: filepath.ToSlash(repoRel), AbsPath: repoDir, Kind: classify(p, repoDir)}
		out = append(out, e)
		// fs.WalkDir's SkipDir means "don't descend into this" only when the
		// visited entry is itself a directory; on a non-directory entry it
		// instead means "skip the rest of this directory's siblings" — a
		// linked worktree or submodule checkout's ".git" is always a regular
		// *file*, so returning SkipDir unconditionally here silently
		// truncated the scan of the rest of that repository's own subtree
		// right after visiting its own marker, hiding any nested repository
		// sorting after ".git" (almost anything). Only an actual ".git"
		// *directory* should stop descent — the same fix already applied in
		// internal/task/remove.go's foreign-repo walk (round 2 review),
		// confirmed there against real fs.WalkDir before being ported here.
		if d.IsDir() {
			return fs.SkipDir // do not descend into a repository's own .git directory
		}
		return nil
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

	// Resolve target relative to repoDir
	var gitdir string
	if filepath.IsAbs(target) {
		gitdir = target
	} else {
		gitdir = filepath.Join(repoDir, target)
	}

	// Check if gitdir contains a gitdir file (linked worktree marker)
	if _, err := os.Stat(filepath.Join(gitdir, "gitdir")); err == nil {
		return KindLinkedWorktree
	}

	// Fall back to substring matching for submodules
	if strings.Contains(target, string(filepath.Separator)+"modules"+string(filepath.Separator)) {
		return KindSubmodule
	}

	return KindRepo
}

func markNested(entries []Entry) {
	// First pass: collect all repositories to analyze
	var repos []*Entry
	for i := range entries {
		if entries[i].Kind == KindRepo {
			repos = append(repos, &entries[i])
		}
	}

	// Second pass: for each repo, find all containing repos and pick the deepest one
	for _, e := range repos {
		var containers []*Entry
		for _, c := range repos {
			if e == c {
				continue
			}
			if paths.IsUnder(e.AbsPath, c.AbsPath) {
				containers = append(containers, c)
			}
		}

		if len(containers) > 0 {
			// Find the deepest container (longest AbsPath)
			deepest := containers[0]
			for _, c := range containers[1:] {
				if len(c.AbsPath) > len(deepest.AbsPath) {
					deepest = c
				}
			}
			e.Kind = KindNested
			e.ContainedBy = deepest.RelPath
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
Expected: all seven tests PASS.

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
  - `container.C{Root, Workspace string}` with methods `StoreDir() string`, `TreesDir() string`, `StateDir() string`, `ConfigDir() string`, `StagingDir() string`, `TreePath(task string) string`.
  - `container.Locate(workspace string) (C, error)` — `<workspace>.worktrees` when the parent is writable, otherwise `~/.local/state/wkt/<hash>`; never a descendant of the workspace, and it **refuses** rather than returning a container inside it (the fallback's own trigger is an unwritable parent, which `$HOME` as the workspace satisfies while `~/.local/state` sits inside it).
  - `container.Create(c C) error` — creates the four subdirectories, 0o700.
  - `container.Lock(c C) (release func(), err error)` — one exclusive lock per container. The holder's PID is written into the file so a refusal can name it; no stale-PID sweep is needed, because the kernel drops an `flock` when its holder dies. `release` unlocks and closes but **never unlinks** the lock file: unlinking lets a second party flock the orphaned inode while a third flocks a freshly created one.

**Traps:**

- `Locate` canonicalises, so a test comparing `c.Root` against a raw
  `t.TempDir()` path is unsatisfiable on macOS. Canonicalise both sides.
- The fallback triggers on an unwritable parent — and `$HOME` as the workspace
  is exactly that case, which would put `~/.local/state/wkt/...` **inside** the
  workspace. Refuse instead of returning it.
- `release` must not unlink the lock file. A party that opened the path before
  the unlink holds a lock on an orphaned inode while the next `Lock` locks a
  freshly created one — two holders of one lock.

- [ ] **Step 1: Write the failing test**

```go
// internal/container/container_test.go
package container

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"wkt/internal/paths"
	"wkt/internal/wkterr"
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
	if c.Root != c.Workspace+".worktrees" {
		t.Fatalf("container %q is not the documented sibling of %q", c.Root, c.Workspace)
	}
	if paths.IsUnder(c.Root, c.Workspace) {
		t.Fatalf("container %q must never live inside the workspace", c.Root)
	}
	canonWs, err := paths.Canonical(ws)
	if err != nil {
		t.Fatal(err)
	}
	if c.Workspace != canonWs {
		t.Fatalf("container workspace %q is not canonical (expected %q)", c.Workspace, canonWs)
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

func TestLocateRejectsWhenFallbackWouldBeInsideWorkspace(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "work")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o755) })

	// Set HOME to the workspace so the fallback would be inside it
	oldHome := os.Getenv("HOME")
	t.Setenv("HOME", ws)
	t.Cleanup(func() { os.Setenv("HOME", oldHome) })

	_, err := Locate(ws)
	if err == nil {
		t.Fatal("Locate must refuse when the fallback container would be inside the workspace")
	}
	e, ok := err.(*wkterr.E)
	if !ok {
		t.Fatalf("error is not *wkterr.E: %T", err)
	}
	if e.Code != "WKT_NO_CONTAINER" {
		t.Fatalf("error code is %q, expected WKT_NO_CONTAINER", e.Code)
	}
}

func TestLockIsExclusive(t *testing.T) {
	base := t.TempDir()
	c := C{Root: filepath.Join(base, "c"), Workspace: base}
	if err := Create(c); err != nil {
		t.Fatal(err)
	}

	lockPath := filepath.Join(c.Root, ".wkt.lock")
	release, err := Lock(c)
	if err != nil {
		t.Fatal(err)
	}

	// Capture inode after first lock
	info1, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	inode1 := info1.Sys().(*syscall.Stat_t).Ino

	if _, err := Lock(c); err == nil {
		release()
		t.Fatal("a second lock must fail while the first is held")
	}
	release()

	release2, err := Lock(c)
	if err != nil {
		t.Fatalf("lock after release must succeed: %v", err)
	}

	// Verify inode is the same after second lock
	info2, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	inode2 := info2.Sys().(*syscall.Stat_t).Ino

	if inode1 != inode2 {
		t.Fatalf("lock file inode changed: %d != %d (proves lock file was unlinked and recreated)", inode1, inode2)
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

// ConfigDir holds the container's own state, as opposed to StateDir, which
// holds one file per task.
func (c C) ConfigDir() string { return filepath.Join(c.Root, "state") }

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
	root := filepath.Join(home, ".local", "state", "wkt", id)
	if paths.IsUnder(root, ws) {
		return C{}, wkterr.New("WKT_NO_CONTAINER", "the fallback container would live inside the workspace").
			WithPath(root).
			WithFound("workspace: " + ws).
			WithRemedy("configure the container location explicitly")
	}
	return C{Root: root, Workspace: ws}, nil
}

func writable(dir string) bool {
	probe := filepath.Join(dir, ".wkt-write-probe-"+strconv.Itoa(os.Getpid()))
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
		return nil, wkterr.New("WKT_CONTAINER_UNUSABLE", "cannot open the container lock").
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
	}, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/container/ -v`
Expected: all four tests PASS.

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
  - `state.Container{SchemaVersion int, Excluded []string}` with `state.SaveContainer(dir string, c Container) error` and `state.LoadContainer(dir string) (Container, error)` — the container's own state (spec §5.3 rule 6 records init's `--exclude` decisions here). A container that has never excluded anything has no file, which is not an error; an unreadable or future-schema one is.
  - `state.Load(dir, name string) (Task, error)` — rejects a `SchemaVersion` newer than this binary understands, with a typed error. `state.List(dir string) ([]string, error)` — files only; a directory named `something.json` is not a task.

**Traps:**

- An atomicity test that only asserts no `.tmp` file survived is satisfied by a
  naive direct write too. Assert that a **reader never sees a partial file** —
  the rename is the point.
- `List` needs an `IsDir` guard, or a directory named `something.json` is
  reported as a task.
- `Load` must validate `SchemaVersion`. A future binary's state file read by
  this one is a silent misparse, not a missing field.

- [ ] **Step 1: Write the failing test**

```go
// internal/state/state_test.go
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wkt/internal/wkterr"
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
	if e, ok := err.(*wkterr.E); !ok || e.Code != "WKT_NO_TASK" {
		t.Fatalf("error must be typed as WKT_NO_TASK; got %v (ok=%v)", err, ok)
	}
	if !strings.Contains(err.Error(), "WKT_NO_TASK") {
		t.Fatalf("error %q must carry WKT_NO_TASK", err.Error())
	}
}

func TestSaveOverwritesAReadOnlyDestination(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions do not discriminate")
	}
	dir := t.TempDir()
	task := Task{SchemaVersion: 1, Name: "feat-42", Workspace: "/ws"}
	if err := Save(dir, task); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "feat-42.json")
	if err := os.Chmod(dest, 0o400); err != nil {
		t.Fatal(err)
	}
	task.Workspace = "/ws2"
	if err := Save(dir, task); err != nil {
		t.Fatalf("Save must replace a read-only destination by rename: %v", err)
	}
	got, err := Load(dir, "feat-42")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != "/ws2" {
		t.Fatalf("second Save did not take effect: %+v", got)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()

	// Save two tasks
	task1 := Task{SchemaVersion: 1, Name: "task1", Workspace: "/ws"}
	task2 := Task{SchemaVersion: 1, Name: "task2", Workspace: "/ws"}
	if err := Save(dir, task1); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, task2); err != nil {
		t.Fatal(err)
	}

	// Create a .tmp file (leftover from failed write)
	if f, err := os.CreateTemp(dir, "task3.*.tmp"); err != nil {
		t.Fatal(err)
	} else {
		f.Close()
	}

	// Create a directory named like a task
	dirLikeTask := filepath.Join(dir, "task4.json")
	if err := os.Mkdir(dirLikeTask, 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 tasks, got %d: %v", len(got), got)
	}

	found := make(map[string]bool)
	for _, name := range got {
		found[name] = true
	}

	if !found["task1"] || !found["task2"] {
		t.Fatalf("expected task1 and task2; got %v", got)
	}
	if found["task3"] || found["task4"] {
		t.Fatalf("should not list .tmp files or directories: %v", got)
	}
}

func TestLoadRejectsNewerSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "future.json")

	// Write a task with a future schema version directly
	futureTask := Task{
		SchemaVersion: 999,
		Name:          "future",
		Workspace:     "/ws",
	}
	b, err := json.MarshalIndent(futureTask, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Load(dir, "future")
	if err == nil {
		t.Fatal("expected an error for future schema version")
	}

	if e, ok := err.(*wkterr.E); !ok || e.Code != "WKT_STATE_VERSION" {
		t.Fatalf("expected WKT_STATE_VERSION error; got %v", err)
	}

	if !strings.Contains(err.Error(), "WKT_STATE_VERSION") {
		t.Fatalf("error must mention WKT_STATE_VERSION: %v", err)
	}
}

// TestContainerStateRoundTrips covers adversarial finding F4: init's
// --exclude decision has to survive the command that made it, because spec
// §5.3 rule 6 calls it "recorded in container state" — a workspace whose
// nested repository is excluded must stay adoptable on every later run.
func TestContainerStateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if err := SaveContainer(dir, Container{Excluded: []string{"a/inner", "b/deep"}}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadContainer(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Excluded) != 2 || got.Excluded[0] != "a/inner" {
		t.Fatalf("excluded paths must round-trip, got %+v", got)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("save must stamp the schema version, got %d", got.SchemaVersion)
	}
}

// TestLoadContainerOnAFreshContainerIsEmptyNotAnError pins the ordinary case:
// a container that has never excluded anything has no file at all.
func TestLoadContainerOnAFreshContainerIsEmptyNotAnError(t *testing.T) {
	got, err := LoadContainer(t.TempDir())
	if err != nil {
		t.Fatalf("a missing container file is the normal state, got %v", err)
	}
	if len(got.Excluded) != 0 {
		t.Fatalf("want nothing excluded, got %+v", got)
	}
}

// TestLoadContainerRejectsANewerSchema mirrors the task-state guard: a file
// written by a future binary must not be misparsed by this one.
func TestLoadContainerRejectsANewerSchema(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "container.json"),
		[]byte(`{"schema_version":99,"excluded":["x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContainer(dir); err == nil {
		t.Fatal("a newer schema version must be refused, not read")
	}
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
	"strconv"
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

// Save writes a task to a temporary file in the same directory, then renames it
// for atomic writes. This ensures the final file is either complete or not present.
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

// Load reads a task from disk by name.
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
	if t.SchemaVersion > SchemaVersion {
		return Task{}, wkterr.New("WKT_STATE_VERSION", "task state was written by a newer wkt").
			WithPath(path(dir, name)).
			WithExpected(strconv.Itoa(SchemaVersion)).
			WithFound(strconv.Itoa(t.SchemaVersion)).
			WithRemedy("upgrade wkt")
	}
	return t, nil
}

// List returns the names of all saved tasks in a directory.
func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // no tasks yet is not an error
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return out, nil
}

// Container is the container's own state, as opposed to a task's: decisions
// made at init time that every later command has to honour. Spec §5.3 rule 6
// requires the nested-repository exclusions to be "recorded in container
// state", because a workspace whose nested repository was excluded once must
// stay adoptable without repeating the flag.
type Container struct {
	SchemaVersion int      `json:"schema_version"`
	Excluded      []string `json:"excluded,omitempty"`
}

func containerPath(dir string) string { return filepath.Join(dir, "container.json") }

// SaveContainer writes the container state through the same temp-file-then-
// rename dance Save uses, so a reader never sees a partial file.
func SaveContainer(dir string, c Container) error {
	c.SchemaVersion = SchemaVersion
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return wkterr.New("WKT_STATE_WRITE", "cannot create the state directory").WithPath(dir)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return wkterr.New("WKT_STATE_WRITE", "cannot encode container state").WithFound(err.Error())
	}
	tmp, err := os.CreateTemp(dir, "container.*.tmp")
	if err != nil {
		return wkterr.New("WKT_STATE_WRITE", "cannot create a temporary state file").WithPath(dir)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return wkterr.New("WKT_STATE_WRITE", "cannot write container state").WithPath(tmp.Name())
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return wkterr.New("WKT_STATE_WRITE", "cannot close the temporary state file").WithPath(tmp.Name())
	}
	if err := os.Rename(tmp.Name(), containerPath(dir)); err != nil {
		_ = os.Remove(tmp.Name())
		return wkterr.New("WKT_STATE_WRITE", "cannot commit container state").WithPath(containerPath(dir))
	}
	return nil
}

// LoadContainer reads the container state. A container that has never
// excluded anything simply has no file, which is the ordinary case and not
// an error — but an unreadable or future-schema file is, because guessing
// there would silently drop an exclusion the user made on purpose.
func LoadContainer(dir string) (Container, error) {
	b, err := os.ReadFile(containerPath(dir))
	if os.IsNotExist(err) {
		return Container{SchemaVersion: SchemaVersion}, nil
	}
	if err != nil {
		return Container{}, wkterr.New("WKT_STATE_CORRUPT", "container state is not readable").
			WithPath(containerPath(dir)).WithFound(err.Error())
	}
	var c Container
	if err := json.Unmarshal(b, &c); err != nil {
		return Container{}, wkterr.New("WKT_STATE_CORRUPT", "container state is not readable").
			WithPath(containerPath(dir)).WithFound(err.Error())
	}
	if c.SchemaVersion > SchemaVersion {
		return Container{}, wkterr.New("WKT_STATE_VERSION", "container state was written by a newer wkt").
			WithPath(containerPath(dir)).
			WithExpected(strconv.Itoa(SchemaVersion)).
			WithFound(strconv.Itoa(c.SchemaVersion)).
			WithRemedy("upgrade wkt")
	}
	return c, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/state/ -v`
Expected: all eight tests PASS.

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
	"strings"
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
	return strings.TrimSpace(string(out))
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
	// Give the workspace a real origin: with no origin, Ensure legitimately
	// drops the "origin" remote entirely (untidy otherwise), so the refspec
	// assertion below needs an origin present to mean anything.
	origin := filepath.Join(base, "origin.git")
	g(t, base, "init", "--bare", "-q", origin)
	g(t, ws, "remote", "add", "origin", origin)
	g(t, ws, "push", "-q", "origin", "main")
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

func TestEnsureRepointsOriginAndFetchesFromRealUpstream(t *testing.T) {
	base := t.TempDir()
	upstream := filepath.Join(base, "upstream.git")
	g(t, base, "init", "--bare", "-q", upstream)

	ws := filepath.Join(base, "ws", "svc-a")
	sha := seedRepo(t, ws)
	g(t, ws, "remote", "add", "origin", upstream)
	wantURL := strings.TrimSpace(g(t, ws, "config", "--get", "remote.origin.url"))
	g(t, ws, "push", "-q", "origin", "main")

	storeDir := filepath.Join(base, "store")
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	sp, err := Ensure(storeDir, ws, "svc-a", "t-origin", sha)
	if err != nil {
		t.Fatal(err)
	}

	// The store must point origin at the repository's real upstream, not at
	// the workspace it was cloned from (the whole reason Ensure touches
	// remotes at all).
	gotURL := strings.TrimSpace(g(t, sp, "config", "--get", "remote.origin.url"))
	if gotURL != wantURL {
		t.Fatalf("store remote.origin.url = %q, want %q (the real upstream)", gotURL, wantURL)
	}

	// Push a commit to the upstream from a THIRD clone -- never through the
	// workspace repo -- to prove the URL and the refspec work together.
	elsewhere := filepath.Join(base, "elsewhere")
	g(t, base, "clone", "-q", upstream, elsewhere)
	if err := os.WriteFile(filepath.Join(elsewhere, "src", "a.txt"), []byte("v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, elsewhere, "-c", "user.email=e@x", "-c", "user.name=t", "add", "-A")
	g(t, elsewhere, "commit", "-qm", "pushed upstream")
	g(t, elsewhere, "push", "-q", "origin", "main")
	upstreamOnly := strings.TrimSpace(g(t, elsewhere, "rev-parse", "HEAD"))

	if HasObject(sp, upstreamOnly) {
		t.Fatal("precondition: the store should not have the upstream-only commit yet")
	}
	g(t, sp, "fetch", "-q", "origin")
	if !HasObject(sp, upstreamOnly) {
		t.Fatal("after fetching origin the upstream-only commit must be reachable")
	}
	if out := g(t, sp, "rev-parse", "--verify", "refs/remotes/origin/main"); len(strings.TrimSpace(out)) < 40 {
		t.Fatalf("origin fetch must land in refs/remotes/origin/*: %q", out)
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
			WithRepo(relPath).WithPath(repoAbs).WithFound(err.Error())
	}

	sp := filepath.Join(storeDir, ID(relPath, repoAbs)+".git")
	if _, err := os.Stat(sp); err == nil {
		return sp, nil // idempotent
	}

	if _, err := gitx.Run(storeDir, "clone", "--shared", "--bare", "-q", repoAbs, sp); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot mirror the repository").
			WithRepo(relPath).WithPath(sp).WithFound(err.Error())
	}
	// De-borrow: copy the objects in, then drop the alternates pointer, so the
	// store survives deletion or re-clone of the workspace repository (spec §5.2).
	if _, err := gitx.Run(sp, "repack", "-a", "-d", "-q"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot repack the store").WithRepo(relPath).WithPath(sp).WithFound(err.Error())
	}
	if err := os.Remove(filepath.Join(sp, "objects", "info", "alternates")); err != nil && !os.IsNotExist(err) {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot de-borrow the store").WithRepo(relPath).WithPath(sp).WithFound(err.Error())
	}

	origin, err := gitx.Run(repoAbs, "remote", "get-url", "origin")
	if err == nil && origin != "" {
		if _, err := gitx.Run(sp, "remote", "set-url", "origin", origin); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot point the store at the real origin").WithRepo(relPath).WithFound(err.Error())
		}
		// Bare clones set NO fetch refspec; without this refs/remotes/* never exist,
		// which silently breaks sync and the unpushed-commit guard (spec H15).
		if _, err := gitx.Run(sp, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot configure the origin refspec").WithRepo(relPath).WithFound(err.Error())
		}
	} else {
		// No origin on the workspace repository: drop the borrowed "origin" the
		// clone created rather than leave a URL-less remote with a refspec.
		_, _ = gitx.Run(sp, "remote", "remove", "origin")
	}
	// Second remote: the workspace repository, so a task can branch from work the
	// developer has committed locally and not pushed (spec §5.2).
	if _, err := gitx.Run(sp, "remote", "add", "workspace", repoAbs); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot add the workspace remote").WithRepo(relPath).WithFound(err.Error())
	}
	if _, err := gitx.Run(sp, "config", "remote.workspace.fetch", "+refs/heads/*:refs/remotes/ws/*"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot configure the workspace refspec").WithRepo(relPath).WithFound(err.Error())
	}
	for _, kv := range [][2]string{{"gc.auto", "0"}, {"core.hooksPath", "/dev/null"}} {
		if _, err := gitx.Run(sp, "config", kv[0], kv[1]); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot harden the store").WithRepo(relPath).WithFound(err.Error())
		}
	}
	return sp, nil
}

func FetchWorkspace(storePath string) error {
	if _, err := gitx.Run(storePath, "fetch", "-q", "workspace"); err != nil {
		return wkterr.New("WKT_FETCH_FAILED", "cannot fetch from the workspace repository").WithPath(storePath).WithFound(err.Error())
	}
	return nil
}

func HasObject(storePath, sha string) bool {
	return gitx.RunOK(storePath, "cat-file", "-e", sha)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/ -v`
Expected: all three tests PASS. The second one is the regression guard for the two design defects found in review — a missing refspec and a store blind to local commits.

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
- Consumes: `discover`, `paths`, `state`, `wkterr`.
- Produces:
  - `tree.Plan{Materialise []string; BackFill []string; LinkDirs []string; CopyFiles []string}`.
  - `tree.PlanFor(workspace string, entries []discover.Entry, selected []string) (Plan, error)` — walks the **whole** workspace, not just its immediate children, so content sitting beside a nested repository inside an ancestor directory still reaches the tree.
  - `tree.Hash(path string) (string, error)` — the content hash recorded for a copied loose file, and re-checked at teardown.
  - `tree.Materialise(treeRoot, workspace string, p Plan) ([]state.LinkSlot, error)` — refuses to link any directory whose target contains a repository at any depth (spec §5.3 rule 4, unbounded, symlinks not followed); creates ancestor directories for real, back-fills un-materialised repositories as **absolute** symlinks at their mirrored positions, symlinks non-git directories, copies loose files (preserving their mode, so an executable script stays executable) and records their hashes. Ancestor directories are derived from **every** repository in the plan, back-filled ones included; deriving them from the materialised set alone makes a back-filled repository's group directory a real directory and the symlink beside it fail.

**Traps:**

- Ancestor directories must be derived from **every** repository in the plan,
  back-filled ones included. Deriving them from the materialised set alone makes
  a back-filled repository at depth ≥2 get a real group directory, the later
  `os.Symlink` fails `EEXIST` — and if that error is swallowed, state records a
  `LinkSlot{Type:"symlink"}` that describes something that is not on disk.
- `PlanFor` must walk the whole workspace. Scanning only its immediate children
  silently drops non-repo content sitting inside an ancestor directory.
- `copyFile` must carry the source mode. Hardcoding `0o644` strips the execute
  bit from every copied script.
- A workspace entry that is itself a symlink belongs in a link slot, not a copy.
- Spec §5.3 rule 4's symlink-target check is easy to read past: a repository
  below the discovery bound is otherwise linked in and shared writable by every
  task, which is the one thing the two-scan design exists to prevent.

- [ ] **Step 1: Write the failing test**

```go
// internal/tree/tree_test.go
package tree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"wkt/internal/discover"
	"wkt/internal/wkterr"
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
	// Pin the exclusion in the plan itself (review finding, Important 4):
	// this must not pass only because Materialise's old EEXIST-swallow
	// happened to leave a pre-existing directory alone.
	for _, d := range p.LinkDirs {
		if d == "services" {
			t.Fatalf("PlanFor must not put an ancestor directory into LinkDirs, got %+v", p.LinkDirs)
		}
	}
}

// TestBackFilledRepoAncestorsStayReal reproduces review finding Critical 1:
// an un-materialised (back-filled) repository still needs every directory on
// its own path to be real, because the symlink only replaces the leaf. Before
// the fix, ancestors were computed from Plan.Materialise alone, so "platform"
// (on the path to the back-filled "platform/team/svc2") fell through to
// LinkDirs as an ordinary whole-directory link, and Materialise's back-fill
// pass then raced the LinkDirs pass for the same tree path.
func TestBackFilledRepoAncestorsStayReal(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "platform", "team", "svc2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "platform", "team", "svc2", "marker.txt"), []byte("m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []discover.Entry{
		{RelPath: "platform/team/svc2", AbsPath: filepath.Join(ws, "platform", "team", "svc2"), Kind: discover.KindRepo},
	}
	// Nothing selected: svc2 is entirely back-filled.
	p, err := PlanFor(ws, entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.BackFill) != 1 || p.BackFill[0] != "platform/team/svc2" {
		t.Fatalf("platform/team/svc2 must be back-filled, got %+v", p)
	}
	for _, d := range p.LinkDirs {
		if d == "platform" || d == "platform/team" {
			t.Fatalf("an ancestor of a back-filled repo must not be a whole-directory link, got LinkDirs=%+v", p.LinkDirs)
		}
	}

	treeRoot := filepath.Join(base, "tree")
	if err := os.MkdirAll(treeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialise(treeRoot, ws, p); err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"platform", filepath.Join("platform", "team")} {
		info, err := os.Lstat(filepath.Join(treeRoot, d))
		if err != nil {
			t.Fatalf("%s must exist as a real directory: %v", d, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s must be a real directory, not a symlink", d)
		}
	}
	marker := filepath.Join(treeRoot, "platform", "team", "svc2", "marker.txt")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("content of the back-filled repo must be reachable through its symlink: %v", err)
	}
}

// TestMaterialiseRefusesConflictingTreeContent reproduces review finding
// Critical 2: Materialise used to treat os.Symlink's EEXIST as success
// unconditionally, so stale real content already sitting at a link slot's
// path was silently left in place while the returned state claimed a fresh,
// correct symlink was there.
func TestMaterialiseRefusesConflictingTreeContent(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries := []discover.Entry{
		{RelPath: "shared", AbsPath: filepath.Join(ws, "shared"), Kind: discover.KindRepo},
	}
	p, err := PlanFor(ws, entries, nil) // shared is back-filled
	if err != nil {
		t.Fatal(err)
	}

	treeRoot := filepath.Join(base, "tree")
	// Stale real content already occupies the slot: not the expected symlink.
	if err := os.MkdirAll(filepath.Join(treeRoot, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(treeRoot, "shared", "stale.txt")
	if err := os.WriteFile(stale, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = Materialise(treeRoot, ws, p)
	if err == nil {
		t.Fatal("Materialise must refuse when a tree path already exists and is not the expected symlink")
	}
	var e *wkterr.E
	if !errors.As(err, &e) || e.Code != "WKT_TREE_CONFLICT" {
		t.Fatalf("expected WKT_TREE_CONFLICT, got %v", err)
	}
	// Materialise never deletes anything: the stale content must survive
	// untouched pending manual resolution.
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("stale content must be left untouched on refusal: %v", err)
	}
}

// TestAncestorSiblingContentIsMaterialised reproduces review finding
// Critical 3: PlanFor only scanned the workspace's immediate children, so
// content living alongside a materialised repo's ancestor chain — a sibling
// directory or file inside "platform" when only "platform/team/svc" is
// selected — never entered any bucket and silently vanished from the tree.
func TestAncestorSiblingContentIsMaterialised(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "platform", "team", "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "platform", "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "platform", "design", "notes.md"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "platform", "README.md"), []byte("readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []discover.Entry{
		{RelPath: "platform/team/svc", AbsPath: filepath.Join(ws, "platform", "team", "svc"), Kind: discover.KindRepo},
	}
	p, err := PlanFor(ws, entries, []string{"platform/team/svc"})
	if err != nil {
		t.Fatal(err)
	}

	wantLinkDir := filepath.ToSlash(filepath.Join("platform", "design"))
	if !contains(p.LinkDirs, wantLinkDir) {
		t.Fatalf("platform/design must be linked at its full relative path, got LinkDirs=%+v", p.LinkDirs)
	}
	wantCopy := filepath.ToSlash(filepath.Join("platform", "README.md"))
	if !contains(p.CopyFiles, wantCopy) {
		t.Fatalf("platform/README.md must be copied at its full relative path, got CopyFiles=%+v", p.CopyFiles)
	}

	treeRoot := filepath.Join(base, "tree")
	if err := os.MkdirAll(filepath.Join(treeRoot, "platform", "team", "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialise(treeRoot, ws, p); err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"platform", filepath.Join("platform", "team")} {
		info, err := os.Lstat(filepath.Join(treeRoot, d))
		if err != nil {
			t.Fatalf("%s must exist as a real directory: %v", d, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s must be a real directory, not a symlink", d)
		}
	}
	info, err := os.Lstat(filepath.Join(treeRoot, "platform", "design"))
	if err != nil {
		t.Fatalf("platform/design must exist: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("platform/design must be a symlink")
	}
	if _, err := os.Stat(filepath.Join(treeRoot, "platform", "design", "notes.md")); err != nil {
		t.Fatalf("platform/design/notes.md must be reachable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(treeRoot, "platform", "README.md")); err != nil {
		t.Fatalf("platform/README.md must be reachable: %v", err)
	}
}

// TestCopiedLooseFileKeepsExecuteBit covers review finding Minor 5:
// copyFile used to hardcode 0o644, stripping the execute bit off any
// executable loose file (a helper script, a hook).
func TestCopiedLooseFileKeepsExecuteBit(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "hook.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := PlanFor(ws, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	treeRoot := filepath.Join(base, "tree")
	if err := os.MkdirAll(treeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialise(treeRoot, ws, p); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(treeRoot, "hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("copied loose file must keep its execute bit, got mode %v", info.Mode().Perm())
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestLinkDirRefusesWhenItHidesANestedRepoBelowTheDiscoveryBound reproduces
// review finding Important 7: PlanFor's repository *enumeration* stops at
// a configurable depth (default 4) and Materialise used to link any
// non-git directory whole regardless — so a repository sitting deeper than
// that bound was invisible to discovery yet still made its containing
// directory a single real directory shared, writable, by every task's tree
// and by the workspace itself. Spec §5.3 rule 4 requires every symlink
// target to be separately resolved and walked, unbounded depth, before the
// link is created.
func TestLinkDirRefusesWhenItHidesANestedRepoBelowTheDiscoveryBound(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	deep := filepath.Join(ws, "notes", "a", "b", "c", "d", "hidden")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	initCmd := exec.Command("git", "-c", "init.defaultBranch=main", "init", "-q", deep)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}

	// Nothing discovered at all: "notes" plans as an ordinary whole-directory
	// link, exactly as it would for a workspace where "hidden" sits beyond
	// the discovery depth and so was never classified as a repository.
	p, err := PlanFor(ws, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(p.LinkDirs, "notes") {
		t.Fatalf("notes must be planned as a whole-directory link, got %+v", p)
	}

	treeRoot := filepath.Join(base, "tree")
	if err := os.MkdirAll(treeRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = Materialise(treeRoot, ws, p)
	if err == nil {
		t.Fatal("Materialise must refuse to link a directory that hides a nested repository below the discovery bound")
	}
	var e *wkterr.E
	if !errors.As(err, &e) || e.Code != "WKT_NESTED_REPO" {
		t.Fatalf("expected WKT_NESTED_REPO, got %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(treeRoot, "notes")); !os.IsNotExist(statErr) {
		t.Fatal("the refused link must never have been created — a shared writable slot must never exist")
	}
}

// TestSymlinkedWorkspaceEntryRoutesToALinkSlotNotACopy reproduces review
// finding Important 8: DirEntry.IsDir() is Lstat-based, so an ordinary
// symlink at the workspace root — "current", "bin", "data": normal
// workspace furniture — always reported false, bucketing it into
// CopyFiles. copyFile then os.Stat's it (following the link), opens a
// directory, and the content copy fails, breaking "wkt new" on the
// symlink without ever naming it in the error.
func TestSymlinkedWorkspaceEntryRoutesToALinkSlotNotACopy(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "releases", "v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "releases", "v3", "marker.txt"), []byte("v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "releases", "v3"), filepath.Join(ws, "current")); err != nil {
		t.Fatal(err)
	}

	p, err := PlanFor(ws, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(p.LinkDirs, "current") {
		t.Fatalf("a symlinked workspace entry must be planned as a link, got %+v", p)
	}
	for _, f := range p.CopyFiles {
		if f == "current" {
			t.Fatal("a symlink must never be routed to CopyFiles")
		}
	}

	treeRoot := filepath.Join(base, "tree")
	if err := os.MkdirAll(treeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialise(treeRoot, ws, p); err != nil {
		t.Fatalf("materialising a symlinked workspace entry must not fail: %v", err)
	}
	info, err := os.Lstat(filepath.Join(treeRoot, "current"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the tree's own slot for a symlinked entry must itself be a symlink, not a copy")
	}
	if _, err := os.Stat(filepath.Join(treeRoot, "current", "marker.txt")); err != nil {
		t.Fatalf("the symlink chain must resolve to the real content: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tree/ -v`
Expected: FAIL — `undefined: PlanFor`.

- [ ] **Step 3: Write the implementation**

```go
// internal/tree/tree.go
// Package tree materialises a task's tree: repositories mirrored at their
// workspace-relative positions, un-materialised repositories back-filled as
// absolute symlinks so cross-repo references still resolve, non-git
// directories linked, and loose files copied with their content hash
// recorded.
package tree

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"wkt/internal/discover"
	"wkt/internal/paths"
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
	// Directories on the path to anything the tree places — a materialised
	// worktree, or a back-filled repository's own leaf symlink — must stay
	// real directories. A back-filled repo is a symlink only at its own
	// position; every ancestor above it still has to be real, or the whole
	// ancestor chain would collapse into one whole-directory link and bury
	// the repository (and any of its siblings) inside it.
	ancestors := map[string]bool{}
	addAncestors := func(rel string) {
		for d := filepath.Dir(rel); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
			ancestors[d] = true
		}
	}
	for _, m := range p.Materialise {
		addAncestors(m)
	}
	for _, b := range p.BackFill {
		addAncestors(b)
	}
	if err := planDir(workspace, "", repoPaths, ancestors, &p); err != nil {
		return Plan{}, err
	}
	return p, nil
}

// planDir buckets the children of the workspace-relative directory dirRel
// (dirRel == "" for the workspace root). It recurses only into directories
// that lie on the path to something the tree places (ancestors) — anything
// else is a leaf: a repository already bucketed by the caller, a directory
// to link whole, or a file to copy. This is what keeps content that lives
// alongside an ancestor (a sibling directory or loose file inside a
// materialised or back-filled repo's parent) from silently falling out of
// the plan.
func planDir(workspace, dirRel string, repoPaths, ancestors map[string]bool, p *Plan) error {
	abs := filepath.Join(workspace, dirRel)
	top, err := os.ReadDir(abs)
	if err != nil {
		return wkterr.New("WKT_WORKSPACE_UNREADABLE", "cannot read the workspace").WithPath(abs)
	}
	for _, e := range top {
		name := e.Name()
		if dirRel == "" && (name == ".claude" || name == ".wkt") {
			continue
		}
		rel := name
		if dirRel != "" {
			rel = filepath.Join(dirRel, name)
		}
		if repoPaths[rel] {
			continue // already bucketed into Materialise or BackFill
		}
		if ancestors[rel] {
			if err := planDir(workspace, rel, repoPaths, ancestors, p); err != nil {
				return err
			}
			continue
		}
		relSlash := filepath.ToSlash(rel)
		// e.IsDir() is Lstat-based (os.ReadDir never follows symlinks), so a
		// symlink — an ordinary workspace fixture like "current", "bin" or
		// "data" — always reports false here, regardless of what it points
		// at. Bucketing it into CopyFiles on that basis used to route it
		// into copyFile, which os.Stat's (following the link): a symlink to
		// a directory then opens successfully as a directory and the
		// content copy fails, breaking "wkt new" on an ordinary symlink and
		// naming the destination in the error without ever mentioning the
		// symlink. An explicit Lstat here — rather than trusting
		// DirEntry.Type(), which is not guaranteed populated on every
		// platform — routes any symlink to a link slot instead: wkt creates
		// its own symlink pointing at the workspace's, so the chain
		// resolves exactly as it would from the workspace itself, whatever
		// it ultimately points at.
		info, statErr := os.Lstat(filepath.Join(abs, name))
		switch {
		case statErr != nil:
			return wkterr.New("WKT_WORKSPACE_UNREADABLE", "cannot inspect a workspace entry").WithPath(filepath.Join(abs, name))
		case info.Mode()&os.ModeSymlink != 0, info.IsDir():
			p.LinkDirs = append(p.LinkDirs, relSlash)
		default:
			p.CopyFiles = append(p.CopyFiles, relSlash)
		}
	}
	return nil
}

func Materialise(treeRoot, workspace string, p Plan) ([]state.LinkSlot, error) {
	var slots []state.LinkSlot
	// link creates a whole-directory (or whole-file) link slot at rel.
	// checkNested gates the nested-repository scan (spec §5.3 rule 4): a
	// back-filled repository's own slot is deliberately a live link to a
	// repository wkt already knows about, but an ordinary non-git directory
	// (p.LinkDirs) must never be linked whole without first walking it for
	// a repository sitting below the depth-bounded discovery scan — without
	// that check the SAME real directory becomes writable and shared by
	// every task's tree, and by the workspace itself.
	link := func(rel string, checkNested bool) error {
		dst := filepath.Join(treeRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return wkterr.New("WKT_TREE_BUILD", "cannot create an ancestor directory").WithPath(dst)
		}
		src := filepath.Join(workspace, rel) // absolute target (spec §5.3 rule 3)
		if checkNested {
			resolved, rerr := paths.Canonical(src)
			if rerr != nil {
				resolved = src
			}
			if found := findNestedRepo(resolved); found != "" {
				return wkterr.New("WKT_NESTED_REPO", "a repository lies beneath a directory wkt would otherwise link whole").
					WithPath(dst).WithFound(found)
			}
		}
		switch info, err := os.Lstat(dst); {
		case err == nil && info.Mode()&os.ModeSymlink != 0:
			// Already a symlink. Only a match on the exact intended target is
			// idempotent; anything else is a conflict, not a silent overwrite.
			actual, rerr := os.Readlink(dst)
			if rerr != nil {
				return wkterr.New("WKT_TREE_BUILD", "cannot read an existing link slot").WithPath(dst).WithFound(rerr.Error())
			}
			if actual != src {
				return wkterr.New("WKT_TREE_CONFLICT", "tree path already exists and is not the expected link").
					WithPath(dst).WithExpected(src).WithFound(actual)
			}
		case err == nil:
			// Exists and is not a symlink at all: never silently swallowed.
			kind := "a file"
			if info.IsDir() {
				kind = "a directory"
			}
			return wkterr.New("WKT_TREE_CONFLICT", "tree path already exists and is not the expected link").
				WithPath(dst).WithExpected(src).WithFound(kind)
		case os.IsNotExist(err):
			if err := os.Symlink(src, dst); err != nil {
				return wkterr.New("WKT_TREE_BUILD", "cannot create a link slot").WithPath(dst).WithFound(err.Error())
			}
		default:
			return wkterr.New("WKT_TREE_BUILD", "cannot inspect a tree path").WithPath(dst).WithFound(err.Error())
		}
		slots = append(slots, state.LinkSlot{RelPath: rel, Target: src, Type: "symlink"})
		return nil
	}
	for _, rel := range p.BackFill {
		if err := link(rel, false); err != nil {
			return nil, err
		}
	}
	for _, rel := range p.LinkDirs {
		if err := link(rel, true); err != nil {
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

// findNestedRepo walks dir to unbounded depth, never following symlinks,
// looking for a ".git" marker at any depth — a repository sitting below
// the depth-bounded repository *enumeration* scan (spec §5.3 rule 4: "two
// different scans, two different bounds"). It returns the first
// repository directory found, or "" if none. An unreadable subtree is
// skipped, not treated as a scan failure — the same convention
// discover.Walk already uses for repository enumeration.
func findNestedRepo(dir string) string {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	var found string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, do not abort the walk
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil // never follow symlinks
		}
		if d.Name() != ".git" {
			return nil
		}
		found = filepath.Dir(p)
		return fs.SkipAll
	})
	return found
}

func copyFile(src, dst string) (string, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot stat a workspace file").WithPath(src)
	}
	in, err := os.Open(src)
	if err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot read a workspace file").WithPath(src)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot create a directory").WithPath(dst)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot write into the tree").WithPath(dst)
	}
	defer out.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot copy a workspace file").WithPath(dst)
	}
	// OpenFile's mode is subject to umask; chmod explicitly so the copy's
	// permissions (notably the execute bit on scripts and hooks) match the
	// source exactly, not whatever the process umask allowed through.
	if err := out.Chmod(srcInfo.Mode().Perm()); err != nil {
		return "", wkterr.New("WKT_TREE_BUILD", "cannot set permissions on a copied file").WithPath(dst)
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
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/tree/ -v`
Expected: all eight tests PASS. The first is the differentiator guard — mirroring is worthless without back-fill.

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
- Consumes: `container`, `discover`, `gitx`, `paths`, `state`, `store`, `tree`, `wkterr`.
- Produces:
  - `task.Resolution{Repo state.Repo; Problems []*wkterr.E}`.
  - `task.SubmoduleWarnings(entries []discover.Entry, selected []string) []Blocker` — names every selected repository carrying a submodule, so `new` can warn (spec §5.7).
  - `task.Validate(c container.C, entries []discover.Entry, name string, selected []string) ([]state.Repo, error)` — phase one: refuses a task name that is not one path segment, then resolves the base per repository and checks branch existence, ancestry, `worktree list` occupancy, D/F conflicts, case-fold collisions and `check-ref-format` across the **whole set** before anything is created.
  - `task.Create(c container.C, entries []discover.Entry, name string, selected []string) (state.Task, error)` — phase two, rolling back every worktree, branch, pin and directory it created on any failure.

**Traps:**

- Roll back with `worktree remove --force` and it fails on a **locked**
  worktree — and `Create` locks every worktree immediately after adding it. The
  rollback that cannot roll back is worse than none.
- `store.Ensure` writes the base pin as its *first* action, so the undo for it
  must be registered **before** `Ensure` is called, not after it and the fetch
  block return. Two ordinary failure paths otherwise leak a ref into the user's
  own repository permanently. Same shape for the branch-delete undo: register it
  before `worktree add`, not after.
- `worktreeName` returning `""` on no match, stored unchecked, silently breaks
  the repair feature that depends on it. Return a typed error.
- Refuse up front when the task tree directory already exists — do not discover
  it half-way through phase two.
- A rollback test must actually **reach** phase two. If validation rejects the
  batch first, the undo stack is never exercised and the test passes green
  against a rollback that does nothing.
- The task name is a branch name **and** a path segment. `check-ref-format`
  accepts `feature/x` because it is a fine branch name; as a path it makes the
  state write fail *after* the tree is built, and the rollback leaves an empty
  `trees/feature` that blocks the plain name `feature` forever. Refuse the
  separator in phase one, before anything exists.
- `WKT_TREE_EXISTS` is only reachable when **no** task state exists, so a remedy
  of "wkt rm <name>" sends the user to a command that answers `WKT_NO_TASK`.
  Name the directory instead.

- [ ] **Step 1: Write the failing test**

```go
// internal/task/create_test.go
package task

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wkt/internal/container"
	"wkt/internal/discover"
	"wkt/internal/store"
	"wkt/internal/wkterr"
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

// TestCreateRecordsDistinctStoreWorktreeNamesOnCollision reproduces review
// finding Important 6: two tasks selecting the same repository collide by
// construction — every task tree mirrors the workspace shape, so both
// worktrees sit at a path whose basename is the repository's own leaf name
// (".../feat-a/svc-a" and ".../feat-b/svc-a"), even though git registers
// the second one under the shared store disambiguated, as "svc-a1". The old
// code took filepath.Base of the *worktree path* — always "svc-a" for
// both — instead of reading back git's actual admin directory name, which
// repair cannot work without (spec §5.4).
func TestCreateRecordsDistinctStoreWorktreeNamesOnCollision(t *testing.T) {
	c, entries := fixture(t)
	taskA, err := Create(c, entries, "feat-a", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	entries2, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}
	taskB, err := Create(c, entries2, "feat-b", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}

	nameA := taskA.Repos[0].StoreWorktreeName
	nameB := taskB.Repos[0].StoreWorktreeName
	if nameA != "svc-a" {
		t.Fatalf("the first task must register under the plain leaf name, got %q", nameA)
	}
	if nameA == nameB {
		t.Fatalf("two tasks colliding on one repository must record different store worktree registration names, both got %q", nameA)
	}

	sp := filepath.Join(c.StoreDir(), taskA.Repos[0].StoreID+".git")
	if _, err := os.Stat(filepath.Join(sp, "worktrees", nameB)); err != nil {
		t.Fatalf("the recorded name %q must be git's actual admin directory under the store: %v", nameB, err)
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

// TestCreateRollsBackAlreadyCreatedRepositoriesOnMidPhaseTwoFailure closes a
// gap the other two tests leave open: a divergent branch is caught by
// Validate before phase two ever starts, so it never exercises the rollback
// undo stack — a no-op rollback passes it just as well as a working one.
// Here the conflict is invisible to Validate and can only surface once
// phase two actually runs, after the first repository's store worktree,
// branch and base pin already exist. Only a real rollback removes them.
//
// The conflict is planted at the *second repository's store path*, not at
// its worktree destination under the tree root: review finding Important 5
// (fixed in the same pass as this test's adjustment) makes Create refuse up
// front whenever trees/<name> already exists, which is exactly what
// pre-populating a worktree destination under treeRoot would trip before
// Create ever reached phase two. A plain file sitting where the bare clone
// needs to go is just as invisible to Validate and just as unreachable
// until phase two runs.
func TestCreateRollsBackAlreadyCreatedRepositoriesOnMidPhaseTwoFailure(t *testing.T) {
	c, entries := fixture(t)

	treeRoot := c.TreePath("feat-42")
	docsAbs := filepath.Join(c.Workspace, "docs")
	docsStore := filepath.Join(c.StoreDir(), store.ID("docs", docsAbs)+".git")
	if err := os.WriteFile(docsStore, []byte("blocking"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Create(c, entries, "feat-42", []string{"services/svc-a", "docs"})
	if err == nil {
		t.Fatal("create must fail when a repository's store cannot be created")
	}

	if _, statErr := os.Stat(treeRoot); !os.IsNotExist(statErr) {
		t.Fatal("a refused create must leave no tree behind")
	}

	svcAbs := filepath.Join(c.Workspace, "services", "svc-a")
	svcStore := filepath.Join(c.StoreDir(), store.ID("services/svc-a", svcAbs)+".git")
	if out := g(t, svcStore, "branch", "--list", "feat-42"); len(out) != 0 {
		t.Fatalf("rollback must remove the branch already created for the first repository, found %q", out)
	}
	if out := g(t, svcStore, "worktree", "list", "--porcelain"); strings.Contains(out, "branch refs/heads/feat-42") {
		t.Fatalf("rollback must deregister the worktree already created for the first repository, found:\n%s", out)
	}
	if out := g(t, svcAbs, "for-each-ref", "refs/wkt/base/feat-42"); len(out) != 0 {
		t.Fatalf("rollback must remove the base pin already written for the first repository, found %q", out)
	}
}

// TestCreateRemovesBasePinWhenStoreEnsureFailsAfterWritingIt guards the base
// pin specifically: store.Ensure writes refs/wkt/base/<task> into the
// *workspace* repository as its unconditional first action, even when it
// then fails on a later step (here: cloning into an unwritable store
// directory). The pin undo must be registered before Ensure runs, not after
// it returns, or a failed create leaves a stray ref behind in the
// developer's own repository forever.
func TestCreateRemovesBasePinWhenStoreEnsureFailsAfterWritingIt(t *testing.T) {
	c, entries := fixture(t)
	if err := os.Chmod(c.StoreDir(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(c.StoreDir(), 0o700) })

	_, err := Create(c, entries, "feat-42", []string{"services/svc-a"})
	if err == nil {
		t.Fatal("create must fail when the store directory cannot be written to")
	}

	svcAbs := filepath.Join(c.Workspace, "services", "svc-a")
	if out := g(t, svcAbs, "for-each-ref", "refs/wkt/base/feat-42"); len(out) != 0 {
		t.Fatalf("rollback must remove the base pin even when store.Ensure fails after writing it, found %q", out)
	}
}

// TestCreateRollsBackBranchWhenWorktreeAddFails reproduces review finding
// Important 4: "worktree add -b" creates the branch as a side effect before
// it checks anything out, and can still fail on the checkout itself. The
// undo that deletes that branch used to be registered only after "worktree
// add" returned successfully, so a checkout failure left the branch behind
// forever and the task name permanently unusable (WKT_BRANCH_EXISTS on
// every later Create attempt).
//
// The failure is fabricated via plumbing rather than a pre-existing,
// non-empty destination directory: review finding Important 5 (fixed
// separately, in the same pass) makes Create refuse up front whenever
// trees/<name> already exists, which would trip before ever reaching
// "worktree add" if the destination were pre-populated. Instead, the base
// commit's own tree is given an entry literally named ".git" — git refuses
// to check that out ("invalid path '.git'") — built with hash-object /
// mktree / commit-tree so no working tree, anywhere, ever holds it; the
// local filesystem's own refusal to create a directory named ".git" is
// never in the path. Confirmed empirically against real git before writing
// this test: the branch is created and "worktree add" still exits non-zero.
func TestCreateRollsBackBranchWhenWorktreeAddFails(t *testing.T) {
	c, _ := fixture(t)
	repo := filepath.Join(c.Workspace, "docs")

	hashObj := exec.Command("git", "hash-object", "-w", "--stdin")
	hashObj.Dir = repo
	hashObj.Stdin = strings.NewReader("malicious\n")
	blobOut, err := hashObj.Output()
	if err != nil {
		t.Fatalf("git hash-object: %v", err)
	}
	blob := strings.TrimSpace(string(blobOut))

	mktree := exec.Command("git", "mktree")
	mktree.Dir = repo
	mktree.Stdin = strings.NewReader("100644 blob " + blob + "\t.git\n")
	treeOut, err := mktree.Output()
	if err != nil {
		t.Fatalf("git mktree: %v", err)
	}
	tree := strings.TrimSpace(string(treeOut))

	commit := strings.TrimSpace(g(t, repo, "commit-tree", tree, "-m", "evil tree with a .git entry"))
	g(t, repo, "update-ref", "refs/heads/main", commit)

	entries2, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(c, entries2, "feat-leak", []string{"docs"}); err == nil {
		t.Fatal("create must fail when the base commit cannot be checked out")
	}

	docsStore := filepath.Join(c.StoreDir(), store.ID("docs", repo)+".git")
	if out := g(t, docsStore, "branch", "--list", "feat-leak"); len(out) != 0 {
		t.Fatalf("a failed worktree add must not leak the branch git created before the checkout failed, found %q", out)
	}

	entries3, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(c, entries3, "feat-leak", []string{"services/svc-a"}); err != nil {
		t.Fatalf("a name freed by a failed create's rollback must be reusable, got: %v", err)
	}
}

// TestCreateRefusesAndDoesNotTouchAPreExistingTreeDirectory reproduces
// review finding Important 5's exact scenario: os.MkdirAll succeeds
// silently on a directory that already exists, and the old code then
// registered an unconditional os.RemoveAll(treeRoot) rollback undo
// regardless — so a pre-existing trees/<name>/ with unrelated content (a
// stale leftover, or simply a name collision with something that isn't a
// wkt tree at all) was destroyed once create failed for any reason.
//
// The second repository's own worktree destination is pre-populated too
// (the same conflict TestCreateRollsBackAlreadyCreatedRepositoriesOnMidPhaseTwoFailure
// used before it was adjusted for this same finding), so that against the
// pre-fix code this doesn't merely fail to refuse: phase two runs, the
// first repository's store worktree, branch and base pin all get created,
// the second repository's "worktree add" then fails on the pre-existing
// conflict, and the unconditional rollback os.RemoveAll(treeRoot) destroys
// "unrelated.txt" along with everything else. The fix must refuse before
// any of that happens at all.
func TestCreateRefusesAndDoesNotTouchAPreExistingTreeDirectory(t *testing.T) {
	c, entries := fixture(t)

	treeRoot := c.TreePath("feat-preexisting")
	blocked := filepath.Join(treeRoot, "docs")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "stray.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(treeRoot, "unrelated.txt")
	if err := os.WriteFile(unrelated, []byte("not wkt's to touch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Create(c, entries, "feat-preexisting", []string{"services/svc-a", "docs"})
	if err == nil {
		t.Fatal("create must refuse when the task tree directory already exists")
	}
	var e *wkterr.E
	if !errors.As(err, &e) || e.Code != "WKT_TREE_EXISTS" {
		t.Fatalf("expected WKT_TREE_EXISTS, got %v", err)
	}
	if _, statErr := os.Stat(unrelated); statErr != nil {
		t.Fatal("a refused create must never delete content it did not create")
	}
	svcAbs := filepath.Join(c.Workspace, "services", "svc-a")
	if out := g(t, svcAbs, "for-each-ref", "refs/wkt/base/feat-preexisting"); len(out) != 0 {
		t.Fatalf("refusing up front must mean nothing was ever created for either repository, found pin %q", out)
	}
}

// TestValidateRejectsATaskNameThatIsNotOneSafePathSegment covers adversarial
// finding F1. "feature/x" is a perfectly valid *branch* name, so
// check-ref-format waves it through — but the task name is also a path
// segment (trees/<name>, state/tasks/<name>.json), and a name carrying a
// separator made Create build the tree, fail at the state write, roll back,
// and leave an empty trees/feature behind that then blocked the plain name
// "feature" forever.
func TestValidateRejectsATaskNameThatIsNotOneSafePathSegment(t *testing.T) {
	c, entries := fixture(t)
	for _, name := range []string{"feature/x", "a/b/c", "sub/dir/task", "with\\backslash"} {
		_, err := Validate(c, entries, name, []string{"docs"})
		if err == nil {
			t.Fatalf("%q must be refused: the task name is a path segment", name)
		}
		var e *wkterr.E
		if !errors.As(err, &e) || e.Code != "WKT_BAD_TASK_NAME" {
			t.Fatalf("%q: got %v, want WKT_BAD_TASK_NAME", name, err)
		}
	}
}

// TestValidateStillAcceptsOrdinaryTaskNames pins the other side of F1's fix:
// the guard must reject separators, not tighten the name rules generally.
func TestValidateStillAcceptsOrdinaryTaskNames(t *testing.T) {
	c, entries := fixture(t)
	for _, name := range []string{"feat-42", "feat_42", "FEAT.42", "задача"} {
		if _, err := Validate(c, entries, name, []string{"docs"}); err != nil {
			t.Fatalf("%q must be accepted, got %v", name, err)
		}
	}
}

// TestSubmoduleWarningsNamesEverySelectedRepositoryWithASubmodule covers
// adversarial finding F3. Spec §5.7 requires wkt new to warn while the
// submodule route is unimplemented, because rm refuses on a submodule even
// with --force: creating such a task silently produces one that cannot be
// removed by any wkt command at all.
func TestSubmoduleWarningsNamesEverySelectedRepositoryWithASubmodule(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seed(t, filepath.Join(ws, "plain"))
	seed(t, filepath.Join(base, "lib"))
	seed(t, filepath.Join(ws, "super"))
	g(t, filepath.Join(ws, "super"), "-c", "protocol.file.allow=always",
		"submodule", "add", "-q", filepath.Join(base, "lib"), "vendor")
	g(t, filepath.Join(ws, "super"), "commit", "-qm", "add submodule")

	entries, err := discover.Walk(ws, 4)
	if err != nil {
		t.Fatal(err)
	}
	warned := SubmoduleWarnings(entries, []string{"plain", "super"})
	if len(warned) != 1 || warned[0].Repo != "super" {
		t.Fatalf("want exactly one warning naming super, got %+v", warned)
	}
	if warned[0].Code != "WKT_SUBMODULE" {
		t.Fatalf("warning must carry WKT_SUBMODULE, got %q", warned[0].Code)
	}
	if SubmoduleWarnings(entries, []string{"plain"}) != nil {
		t.Fatal("a selection without submodules must warn about nothing")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/task/ -v`
Expected: FAIL — `undefined: Create`.

- [ ] **Step 3: Write phase one — validation**

```go
// internal/task/create.go
// Package task implements the two-phase creation of a wkt task: phase one
// (Validate) resolves and checks every selected repository before anything
// is touched, phase two (Create) builds the tree, store worktrees and
// branches, rolling back everything it created if any step fails.
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

// Resolution pairs a resolved repository with the problems found for it.
// (Reserved for a future batch-diagnostics entry point; Validate today
// returns on the first problem it finds.)
type Resolution struct {
	Repo     state.Repo
	Problems []*wkterr.E
}

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

// Validate is phase one: it resolves the base and checks branch existence
// (in the workspace repository and, if already present, in the store),
// ancestry, case-fold collisions and D/F ref conflicts for every selected
// repository before phase two touches anything.
func Validate(c container.C, entries []discover.Entry, name string, selected []string) ([]state.Repo, error) {
	// The task name is a branch name *and* a path segment: trees/<name>,
	// state/tasks/<name>.json, staging/<name>. check-ref-format accepts
	// "feature/x" because it is a perfectly good branch name, but as a path
	// it makes the state write fail on a directory that does not exist —
	// after the tree was already built — and the rollback then leaves an
	// empty trees/feature behind that blocks the plain name "feature"
	// forever. Refuse the separator here, before anything is created.
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, wkterr.New("WKT_BAD_TASK_NAME", "the task name is also a directory name, so it cannot contain a path separator").
			WithFound(name).
			WithRemedy("use a single-segment name such as " + flatten(name))
	}
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
		// Task branches actually live in the store (since Task 6). A store may
		// already exist for this repository — from a task whose state was lost
		// but whose store survived — carrying the branch even though the
		// workspace repository does not. Check there too, when a store
		// directory already exists; a store with no directory yet simply
		// skips this half.
		storePath := filepath.Join(c.StoreDir(), store.ID(rel, e.AbsPath)+".git")
		if _, statErr := os.Stat(storePath); statErr == nil {
			if gitx.RunOK(storePath, "rev-parse", "--verify", "refs/heads/"+name) {
				return nil, wkterr.New("WKT_BRANCH_EXISTS", "a branch of that name already exists").
					WithRepo(rel).WithRemedy("choose another task name", "or delete the branch")
			}
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

// Create is phase two: it builds the store worktrees, branches and the
// mirrored tree for every resolved repository, and rolls back everything it
// created — in reverse order — the moment any step fails.
func Create(c container.C, entries []discover.Entry, name string, selected []string) (state.Task, error) {
	if _, err := state.Load(c.StateDir(), name); err == nil {
		return state.Task{}, wkterr.New("WKT_TASK_EXISTS", "task already exists").
			WithFound(name).WithRemedy("wkt path "+name, "wkt rm "+name)
	}

	treeRoot := c.TreePath(name)
	// os.MkdirAll succeeds silently on a directory that already exists, and
	// the rollback undo below then os.RemoveAll's the whole thing on any
	// later failure — destroying content wkt never created if that
	// directory was already there for an unrelated reason. Refusing up
	// front is simpler than tracking whether MkdirAll itself created the
	// directory, and it also stops a stale leftover tree from being
	// silently adopted.
	if _, err := os.Stat(treeRoot); err == nil {
		// Not "wkt rm <name>": this branch is only reachable when no task
		// state exists (a task with state fails above as WKT_TASK_EXISTS),
		// and rm on a stateless directory answers WKT_NO_TASK — a dead end
		// that left the user with no documented way out.
		return state.Task{}, wkterr.New("WKT_TREE_EXISTS", "the task tree directory already exists, but no task owns it").
			WithPath(treeRoot).
			WithRemedy("inspect the directory: it is left over from an interrupted create",
				"remove it once you are sure it holds nothing you need, then retry")
	} else if !os.IsNotExist(err) {
		return state.Task{}, wkterr.New("WKT_CHECK_FAILED", "cannot check the task tree directory").WithPath(treeRoot)
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

	if err := os.MkdirAll(treeRoot, 0o755); err != nil {
		return state.Task{}, wkterr.New("WKT_TREE_BUILD", "cannot create the task tree").WithPath(treeRoot)
	}
	undos = append(undos, func() { _ = os.RemoveAll(treeRoot) })

	for i := range repos {
		r := &repos[i]
		// store.Ensure writes the base pin into the workspace repository as its
		// unconditional first internal action — even on the idempotent
		// store-already-exists path, and even if it then fails later (e.g.
		// cloning into the store). The undo must therefore be registered
		// before calling it, not after it returns: deleting a ref that was
		// never written is a no-op in git, so registering early is safe, and
		// the undo only ever runs on rollback.
		repoAbs, pin := r.AbsPath, r.BasePinRef
		undos = append(undos, func() { _, _ = gitx.Run(repoAbs, "update-ref", "-d", pin) })

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

		if err := os.MkdirAll(filepath.Dir(r.WorktreePath), 0o755); err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_TREE_BUILD", "cannot create an ancestor directory").
				WithPath(r.WorktreePath)
		}
		storePath, wtPath := sp, r.WorktreePath
		// "worktree add -b" creates the branch before it checks anything
		// out, and can still fail on the checkout itself (a non-empty
		// destination, an unwritable one, content in the base commit the
		// filesystem refuses) — so the branch-delete undo must be
		// registered before the call, exactly like the pin undo above, or
		// a failed worktree add leaks a branch and the task name becomes
		// permanently unusable (WKT_BRANCH_EXISTS on every later attempt).
		undos = append(undos, func() { _, _ = gitx.Run(storePath, "branch", "-D", name) })
		if _, err := gitx.Run(sp, "worktree", "add", "-q", "-b", name, r.WorktreePath, r.BaseSHA); err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_WORKTREE_ADD", "cannot create the worktree").
				WithRepo(r.RelPath).WithPath(r.WorktreePath)
		}
		undos = append(undos, func() {
			// Force twice: a single --force removes a dirty worktree but still
			// refuses one that is locked, and by the time rollback runs, the
			// worktree lock below has usually already been taken. Registered
			// after the branch-delete undo above, so rollback (LIFO) removes
			// the worktree registration before it tries to delete the branch
			// it was checked out on — git refuses to delete a branch that is
			// still checked out anywhere.
			_, _ = gitx.Run(storePath, "worktree", "remove", "--force", "--force", wtPath)
		})
		if _, err := gitx.Run(sp, "worktree", "lock", "--reason", "held by wkt task "+name, r.WorktreePath); err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_WORKTREE_LOCK", "cannot lock the worktree").WithRepo(r.RelPath)
		}
		wtName, err := worktreeName(r.WorktreePath)
		if err != nil {
			rollback()
			return state.Task{}, wkterr.New("WKT_WORKTREE_NAME_UNRESOLVED",
				"cannot determine the store's worktree registration name; repair cannot work without it").
				WithRepo(r.RelPath).WithPath(r.WorktreePath)
		}
		r.StoreWorktreeName = wtName
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

// worktreeName reads back the admin directory git actually chose, which it
// derives from the leaf basename and silently disambiguates on collision
// (svc-a, svc-a1). repair cannot work without it (spec §5.4), so an
// unresolved name is an error, not a silently empty string persisted into
// state.
//
// Two different tasks on the same repository collide by construction: both
// task trees mirror the workspace shape, so both worktrees sit at a path
// whose basename is the repository's own leaf name (".../feat-a/svc-a" and
// ".../feat-b/svc-a"), even though git registers the second one under the
// store as "svc-a1". filepath.Base(worktreePath) — or, equivalently,
// filepath.Base of the *worktree path* reported by "git worktree list
// --porcelain" — is therefore always "svc-a" for both, which is simply
// wrong for the second task. The registration name has to be read back
// from the worktree's own gitdir instead: "git -C <worktree> rev-parse
// --git-dir" resolves to ".../store/<id>.git/worktrees/svc-a1", and its
// basename is git's actual choice. Confirmed empirically against real git
// before this fix: two worktrees added at paths that share a leaf basename
// register as "svc-a" and "svc-a1" under the store, verified via both
// "worktree list --porcelain" (which only ever reports the worktree
// *path*, never the admin name) and each worktree's own ".git" gitdir
// pointer.
func worktreeName(worktreePath string) (string, error) {
	gitDir, err := gitx.Run(worktreePath, "rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}
	name := filepath.Base(filepath.Clean(gitDir))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", wkterr.New("WKT_WORKTREE_NAME_UNRESOLVED", "cannot determine the store's worktree registration name").
			WithPath(worktreePath)
	}
	return name, nil
}

// flatten suggests a single-segment spelling of a name that carried
// separators, so the refusal above names a usable alternative.
func flatten(name string) string {
	f := strings.NewReplacer("/", "-", "\\", "-").Replace(strings.Trim(name, `/\`))
	if f == "" || f == "." || f == ".." {
		return "task"
	}
	return f
}

// SubmoduleWarnings names every selected repository that carries a submodule.
// Spec §5.7 requires the warning because rm refuses on a submodule even with
// --force — its object store lives under the doomed worktree — so a task
// created over one cannot be removed by any wkt command until the submodule
// is deinitialised. Warning at create time is the difference between knowing
// that before the work starts and discovering it at teardown.
func SubmoduleWarnings(entries []discover.Entry, selected []string) []Blocker {
	byRel := map[string]discover.Entry{}
	for _, e := range entries {
		byRel[e.RelPath] = e
	}
	var out []Blocker
	for _, rel := range selected {
		e, ok := byRel[rel]
		if !ok || e.Kind != discover.KindRepo {
			continue
		}
		sm, err := gitx.Run(e.AbsPath, "submodule", "status", "--recursive")
		if err != nil || strings.TrimSpace(sm) == "" {
			continue
		}
		out = append(out, Blocker{
			Code: "WKT_SUBMODULE", Repo: rel, Path: e.AbsPath,
			Detail: submodulePath(sm), Severity: "info",
		})
	}
	return out
}

// submodulePath pulls the submodule's path out of a "git submodule status"
// line ("<status><sha> <path> (<describe>)") instead of surfacing the raw
// line, which leads with a bare SHA and reads as noise.
func submodulePath(status string) string {
	if f := strings.Fields(firstLine(status)); len(f) >= 2 {
		return f[1]
	}
	return strings.TrimSpace(firstLine(status))
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/task/ -v`
Expected: all ten tests PASS. The rollback ones are the guard for spec H10 — create is not atomic unless the rollback works.

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
  - `task.Blocker{Code, Repo, Path, Detail, Severity string}` — `Severity` is `""` (blocking) or `"info"` (reported, never blocking), so regenerable ignored content can be listed without refusing on it.
  - `task.Preflight(c container.C, t state.Task) ([]Blocker, error)` — enumerates from the filesystem, never from state; walks real directories only and never descends link slots.
  - `task.Remove(c container.C, name string, force bool) error` — a refusal carries its blockers as `wkterr.Problem`s and reserves `Remedy` for actions; it refuses while any blocker exists; with `force`, renames the whole tree into `staging/` first, then removes from there. Recovers a task whose tree is already missing or half-staged, so an interrupted removal can be finished rather than being stuck forever.

**Traps:**

- A denylist of precious basenames is the wrong shape: five substrings matched
  against a basename delete a gitignored `server.key` with no `--force`. Invert
  it — an allowlist of known-**regenerable** content, everything else refuses.
- A bulk-ignored directory collapses to one `git status` line, hiding everything
  inside it. Descend before deciding.
- Preflight checks must fail **closed**: a check whose git call errors must
  block, never report "nothing found".
- A precious-file test must not plant an unpushed commit beside the file — the
  refusal then comes from the commit, and the test passes with the precious-file
  check deleted entirely.
- Untracked content at the tree root is a removal blocker like any other.
- The regenerable allowlist has to carry the OS artifacts too (`.DS_Store`,
  `Thumbs.db`), or every macOS tree refuses on a file nobody created on purpose
  — and a `server.key` sitting *beside* a `.DS_Store` must still refuse.
- Recover a task whose tree is missing or half-staged instead of refusing
  forever.

- [ ] **Step 1: Write the failing test**

```go
// internal/task/remove_test.go
package task

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wkt/internal/discover"
	"wkt/internal/state"
	"wkt/internal/wkterr"
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

// TestRemoveDetectsForeignRepoNestedInsideMaterialisedWorktree is a
// regression guard for a real bug found by mutating the foreign-repo walk:
// fs.WalkDir treats "return SkipDir" differently depending on whether the
// visited entry is a directory or not. A linked worktree's own ".git" is
// always a regular *file*; returning SkipDir unconditionally on it (rather
// than only when it is a directory) skips the rest of that directory's
// siblings, not just the ".git" contents — so a foreign repository nested
// anywhere sorting after ".git" (almost everything) was silently never
// visited. Confirmed empirically against Go's real fs.WalkDir before fixing
// internal/task/remove.go. This is the highest-stakes blocker in the
// package — it must fire even with --force — so it gets its own test rather
// than relying on the other tests to exercise it incidentally.
func TestRemoveDetectsForeignRepoNestedInsideMaterialisedWorktree(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-z", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	// Sorts after ".git" alphabetically, which is what the bug depended on.
	foreign := filepath.Join(wt, "zzz-vendor")
	seed(t, foreign)

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_FOREIGN_REPO" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a foreign repository nested inside a materialised worktree must be detected, got %+v", blockers)
	}

	if err := Remove(c, "feat-z", true); err == nil {
		t.Fatal("a foreign repository must block removal even with --force: its history exists nowhere else")
	}
	if _, statErr := os.Stat(filepath.Join(foreign, ".git")); statErr != nil {
		t.Fatal("the refused removal must not have deleted the foreign repository")
	}
}

// TestRemoveCleansUpStoreBranchSoTaskNameCanBeReused is a regression guard
// for a real bug found by mutating the store cleanup: "git worktree prune"
// removes the worktree's admin entry but never the branch the worktree was
// created on. Left behind, the branch makes a later Create of a task with
// the same name fail Validate's WKT_BRANCH_EXISTS check against the store —
// even though the previous task was fully and cleanly removed. Confirmed
// empirically against real git ("branch --list" still showed the branch
// after unlock+prune) before adding the "branch -D" cleanup step.
func TestRemoveCleansUpStoreBranchSoTaskNameCanBeReused(t *testing.T) {
	c, entries := fixture(t)
	if _, err := Create(c, entries, "feat-reuse", []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	if err := Remove(c, "feat-reuse", false); err != nil {
		t.Fatalf("a clean tree must remove without --force: %v", err)
	}

	entries2, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(c, entries2, "feat-reuse", []string{"docs"}); err != nil {
		t.Fatalf("a task name freed by a clean removal must be reusable, got: %v", err)
	}
}

// TestRemoveRefusesOnUncommittedWorkWithoutForceButForceRemoves exercises
// the whole lifecycle end to end: refusal leaves the tree untouched, --force
// actually deletes it through the staging fence, staging is left clean
// afterward, and the task's state file is gone.
func TestRemoveRefusesOnUncommittedWorkWithoutForceButForceRemoves(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-dirty", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(c, "feat-dirty", false); err == nil {
		t.Fatal("uncommitted work must block removal without --force")
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatal("the refused removal must not have deleted anything")
	}

	if err := Remove(c, "feat-dirty", true); err != nil {
		t.Fatalf("--force must remove a tree whose only problem is uncommitted work: %v", err)
	}
	if _, statErr := os.Stat(c.TreePath("feat-dirty")); !os.IsNotExist(statErr) {
		t.Fatal("a forced removal must actually delete the tree")
	}
	if _, statErr := os.Stat(filepath.Join(c.StagingDir(), "feat-dirty")); !os.IsNotExist(statErr) {
		t.Fatal("the staging fence must not leave a leftover directory behind")
	}
	if _, err := state.Load(c.StateDir(), "feat-dirty"); err == nil {
		t.Fatal("a successful removal must delete the task's state file")
	}
}

// TestPreflightDetectsInProgressBisect exercises spec H2 directly: a mid-
// bisect worktree has an empty "git status --porcelain" (confirmed
// empirically), so a preflight built on status alone would let it through.
// Bisect is used rather than an interactive rebase pause because it needs no
// editor/sequence-editor trickery to stay portable across macOS and Linux.
func TestPreflightDetectsInProgressBisect(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-bisect", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	good := task.Repos[0].BaseSHA

	g(t, wt, "commit", "-qm", "c2", "--allow-empty")
	g(t, wt, "commit", "-qm", "c3", "--allow-empty")
	bad := strings.TrimSpace(g(t, wt, "rev-parse", "HEAD"))

	g(t, wt, "bisect", "start")
	g(t, wt, "bisect", "bad", bad)
	g(t, wt, "bisect", "good", good)

	if s := g(t, wt, "status", "--porcelain"); s != "" {
		t.Fatalf("test setup invariant broken: expected a clean status mid-bisect, got %q", s)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_IN_PROGRESS" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a mid-bisect worktree must block removal even though git status is clean, got %+v", blockers)
	}
}

// TestRemoveRefusesOnSubmoduleEvenWithForce exercises spec §5.7's hardest
// block. Crucially the submodule addition is committed before the check
// runs: an uncommitted "git submodule add" already shows up in plain "git
// status --porcelain" (confirmed empirically) and would be caught by
// WKT_DIRTY regardless, which would prove nothing about the dedicated
// WKT_SUBMODULE check. Committed, status is clean and only the submodule
// check can catch it.
func TestRemoveRefusesOnSubmoduleEvenWithForce(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-sub", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	subSrc := filepath.Join(c.Workspace, "docs")

	cmd := exec.Command("git",
		"-c", "protocol.file.allow=always",
		"-c", "user.email=e@x", "-c", "user.name=t",
		"submodule", "add", "-q", subSrc, "subdir")
	cmd.Dir = wt
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git submodule add: %s", out)
	}
	g(t, wt, "commit", "-qm", "add submodule")

	if s := g(t, wt, "status", "--porcelain"); s != "" {
		t.Fatalf("test setup invariant broken: expected a clean status after committing the submodule, got %q", s)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_SUBMODULE" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a committed submodule must still be detected as a blocker, got %+v", blockers)
	}

	if err := Remove(c, "feat-sub", false); err == nil {
		t.Fatal("a submodule must block removal without --force")
	}
	if err := Remove(c, "feat-sub", true); err == nil {
		t.Fatal("a submodule must block removal even with --force: --force would destroy its object store")
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatal("the refused removal must not have deleted anything")
	}
}

// TestRemoveRefusesOnDivergedCopiedFile exercises the one blocker with no
// git mechanism behind it at all: a loose file living outside every
// repository is copied into the tree by tree.Materialise, and only the
// recorded content hash can tell an agent's edit to the copy apart from an
// untouched one before deletion would silently lose it.
func TestRemoveRefusesOnDivergedCopiedFile(t *testing.T) {
	c, entries := fixture(t)
	readme := filepath.Join(c.Workspace, "README.md")
	if err := os.WriteFile(readme, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Create(c, entries, "feat-copy", []string{"services/svc-a"}); err != nil {
		t.Fatal(err)
	}

	copied := filepath.Join(c.TreePath("feat-copy"), "README.md")
	if _, statErr := os.Stat(copied); statErr != nil {
		t.Fatalf("test setup invariant broken: README.md must have been copied into the tree, got %v", statErr)
	}
	if err := os.WriteFile(copied, []byte("agent edited this\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(c, "feat-copy", false); err == nil {
		t.Fatal("a diverged copied file must block removal")
	}
	if _, statErr := os.Stat(copied); statErr != nil {
		t.Fatal("the refused removal must not have deleted the diverged copy")
	}
}

// --- Round 2: the precious-file classifier was inverted from a denylist of
// five substrings (proven to miss ordinary secrets like "server.key" with
// zero blockers) to an allowlist of provably regenerable path components.
// Unknown ignored content now blocks by default.

func TestRemoveRefusesOnIgnoredKeyFileNotOnAnyDenylist(t *testing.T) {
	c, entries := fixture(t)
	// "server.key" is an entirely ordinary TLS/SSH private key name. It
	// matched none of the old classifier's five substrings
	// (".env", ".env.", "credentials", "id_rsa", ".pem") and was deleted
	// with zero blockers and no --force — the exact condition this check
	// exists for.
	repo := filepath.Join(c.Workspace, "services", "svc-a")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("server.key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, repo, "add", "-A")
	g(t, repo, "commit", "-qm", "ignore server.key")
	entries, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}

	task, err := Create(c, entries, "feat-key", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(task.Repos[0].WorktreePath, "server.key")
	if err := os.WriteFile(keyPath, []byte("-----BEGIN PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Remove(c, "feat-key", false); err == nil {
		t.Fatal("an ignored private key must block removal even though its name isn't on any hardcoded denylist")
	}
	if _, statErr := os.Stat(keyPath); statErr != nil {
		t.Fatal("the refused removal must not have deleted the key")
	}
}

func TestRemoveRefusesOnBulkIgnoredDirectoryNotOnAllowlist(t *testing.T) {
	c, entries := fixture(t)
	// git status collapses a wholly-ignored directory to a single
	// "!! secrets/" line — its contents are never listed individually — so
	// the classifier must treat an unrecognised directory name as precious
	// as a whole, not inspect (and miss) files inside it one by one.
	repo := filepath.Join(c.Workspace, "services", "svc-a")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("secrets/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, repo, "add", "-A")
	g(t, repo, "commit", "-qm", "ignore secrets")
	entries, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}

	task, err := Create(c, entries, "feat-secrets", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	secretsDir := filepath.Join(task.Repos[0].WorktreePath, "secrets")
	if err := os.MkdirAll(secretsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretsDir, "api_token"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_PRECIOUS_IGNORED" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a bulk-ignored, unrecognised directory must block as a whole, got %+v", blockers)
	}

	if err := Remove(c, "feat-secrets", false); err == nil {
		t.Fatal("a bulk-ignored unrecognised directory must block removal")
	}
}

func TestRemoveListsRegenerableIgnoredContentButDoesNotBlockOnIt(t *testing.T) {
	c, entries := fixture(t)
	repo := filepath.Join(c.Workspace, "services", "svc-a")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, repo, "add", "-A")
	g(t, repo, "commit", "-qm", "ignore node_modules")
	entries, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}

	task, err := Create(c, entries, "feat-nm", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	nm := filepath.Join(task.Repos[0].WorktreePath, "node_modules", "leftpad")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "index.js"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var sawInfo bool
	for _, b := range blockers {
		if b.Code == "WKT_PRECIOUS_IGNORED" {
			t.Fatalf("node_modules must not be flagged as precious, got %+v", b)
		}
		if b.Code == "WKT_REGENERABLE_IGNORED" && b.Severity == "info" {
			sawInfo = true
		}
	}
	if !sawInfo {
		t.Fatalf("a regenerable ignored directory must still be listed (as info), got %+v", blockers)
	}

	// This is the whole point of listing it: --force must not be needed
	// when the only ignored content is provably regenerable.
	if err := Remove(c, "feat-nm", false); err != nil {
		t.Fatalf("a tree whose only ignored content is regenerable must remove cleanly without --force: %v", err)
	}
}

// TestRemoveRefusesOnLinkSlotReplacedByRegularFile guards WKT_LINK_SLOT_CHANGED,
// which had no test at all — proven by mutation: disabling that branch left
// every other test in the package passing. An atomic save (write a temp
// file, then rename it over the target) replaces the symlink itself with a
// regular file, unlike a naive write through the symlink, which would leave
// the link intact.
func TestRemoveRefusesOnLinkSlotReplacedByRegularFile(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-linkchange", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}

	docsLink := filepath.Join(c.TreePath("feat-linkchange"), "docs")
	if info, statErr := os.Lstat(docsLink); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("test setup invariant broken: docs must be a symlink slot, got info=%v err=%v", info, statErr)
	}

	tmp := docsLink + ".atomic-tmp"
	if err := os.WriteFile(tmp, []byte("replaced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, docsLink); err != nil {
		t.Fatal(err)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_LINK_SLOT_CHANGED" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a link slot replaced by a regular file must block removal, got %+v", blockers)
	}

	if err := Remove(c, "feat-linkchange", false); err == nil {
		t.Fatal("a changed link slot must block removal without --force")
	}
}

// TestPreflightDetectsEachInProgressMarker table-drives all six markers
// individually. Previously only BISECT_LOG was exercised (indirectly, via a
// real bisect); a regression dropping e.g. MERGE_HEAD from the Go slice
// would have gone unnoticed. Each marker's real on-disk location is
// resolved via "git rev-parse --git-path" exactly as Preflight itself
// resolves it, then created directly — rebase-merge/rebase-apply are
// directories, the rest are files — so the test targets precisely the
// regression class at risk (the marker's name being dropped from the list),
// without depending on git's internal machinery to reach six different
// real operational states, several of which (conflict-driven MERGE_HEAD,
// CHERRY_PICK_HEAD, REVERT_HEAD) would also show up in plain "git status"
// and so would not by themselves prove this check is pulling its weight.
func TestPreflightDetectsEachInProgressMarker(t *testing.T) {
	markers := []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			c, entries := fixture(t)
			task, err := Create(c, entries, "feat-marker", []string{"docs"})
			if err != nil {
				t.Fatal(err)
			}
			wt := task.Repos[0].WorktreePath

			p := strings.TrimSpace(g(t, wt, "rev-parse", "--git-path", marker))
			if !filepath.IsAbs(p) {
				p = filepath.Join(wt, p)
			}
			if marker == "rebase-merge" || marker == "rebase-apply" {
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			blockers, err := Preflight(c, task)
			if err != nil {
				t.Fatal(err)
			}
			var saw bool
			for _, b := range blockers {
				if b.Code == "WKT_IN_PROGRESS" && b.Detail == marker {
					saw = true
				}
			}
			if !saw {
				t.Fatalf("marker %q must be individually detected, got %+v", marker, blockers)
			}
		})
	}
}

// --- Round 3: .DS_Store and other OS artifacts added to the regenerable
// allowlist. Finder writes ".DS_Store" into essentially every directory a
// macOS user opens, and nearly every macOS repository gitignores it; left
// blocking, "wkt rm" would refuse on almost every real tree on the primary
// development platform, teaching people to reach for --force without
// reading the list — the exact reflex the classifier inversion exists to
// prevent.

func TestRemoveListsDSStoreButDoesNotBlockOnIt(t *testing.T) {
	c, entries := fixture(t)
	repo := filepath.Join(c.Workspace, "services", "svc-a")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".DS_Store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, repo, "add", "-A")
	g(t, repo, "commit", "-qm", "ignore .DS_Store")
	entries, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}

	task, err := Create(c, entries, "feat-dsstore", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	dsStore := filepath.Join(task.Repos[0].WorktreePath, ".DS_Store")
	if err := os.WriteFile(dsStore, []byte("finder metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var sawInfo bool
	for _, b := range blockers {
		if b.Code == "WKT_PRECIOUS_IGNORED" {
			t.Fatalf(".DS_Store must not be flagged as precious, got %+v", b)
		}
		if b.Code == "WKT_REGENERABLE_IGNORED" && b.Severity == "info" {
			sawInfo = true
		}
	}
	if !sawInfo {
		t.Fatalf(".DS_Store must still be listed (as info), got %+v", blockers)
	}

	// The whole point: --force must not be needed just because Finder wrote
	// a metadata file into the tree.
	if err := Remove(c, "feat-dsstore", false); err != nil {
		t.Fatalf("a tree whose only ignored content is .DS_Store must remove cleanly without --force: %v", err)
	}
}

func TestRemoveRefusesOnServerKeyBesideDSStore(t *testing.T) {
	c, entries := fixture(t)
	// The regenerable addition must not mask a real secret sitting right
	// next to an OS artifact in the same ignored listing.
	repo := filepath.Join(c.Workspace, "services", "svc-a")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".DS_Store\nserver.key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, repo, "add", "-A")
	g(t, repo, "commit", "-qm", "ignore .DS_Store and server.key")
	entries, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}

	task, err := Create(c, entries, "feat-dsstore-key", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	if err := os.WriteFile(filepath.Join(wt, ".DS_Store"), []byte("finder metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "server.key"), []byte("-----BEGIN PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var sawPrecious, sawInfo bool
	for _, b := range blockers {
		if b.Code == "WKT_PRECIOUS_IGNORED" {
			sawPrecious = true
		}
		if b.Code == "WKT_REGENERABLE_IGNORED" {
			sawInfo = true
		}
	}
	if !sawPrecious {
		t.Fatalf("server.key must still block even beside a regenerable .DS_Store, got %+v", blockers)
	}
	if !sawInfo {
		t.Fatalf(".DS_Store beside it should still be listed as regenerable, got %+v", blockers)
	}

	if err := Remove(c, "feat-dsstore-key", false); err == nil {
		t.Fatal("a real secret beside a regenerable OS artifact must still block removal without --force")
	}
}

// --- Review finding Critical 1: Preflight scoped every content check to a
// repository's WorktreePath, so content living at the tree root itself —
// exactly where a session's working directory is, what "wkt path" prints —
// was invisible to every check and silently deleted by os.RemoveAll(staged).

func TestRemoveRefusesOnFileAtTreeRoot(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-root-file", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(c.TreePath(task.Name), "PLAN.md")
	if err := os.WriteFile(plan, []byte("cross-repo plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_UNTRACKED_TREE_CONTENT" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a file at the tree root must be reported as untracked tree content, got %+v", blockers)
	}

	if err := Remove(c, "feat-root-file", false); err == nil {
		t.Fatal("a file at the tree root must block removal without --force")
	}
	if _, statErr := os.Stat(plan); statErr != nil {
		t.Fatal("the refused removal must not have deleted the tree-root file")
	}
}

func TestRemoveRefusesOnFileInNewSubdirectoryOfTreeRoot(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-root-subdir", []string{"services/svc-a"})
	if err != nil {
		t.Fatal(err)
	}
	scratchDir := filepath.Join(c.TreePath(task.Name), "scratch")
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scratchFile := filepath.Join(scratchDir, "out.json")
	if err := os.WriteFile(scratchFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_UNTRACKED_TREE_CONTENT" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a file in a new subdirectory of the tree root must be reported as untracked tree content, got %+v", blockers)
	}

	if err := Remove(c, "feat-root-subdir", false); err == nil {
		t.Fatal("a new subdirectory at the tree root must block removal without --force")
	}
	if _, statErr := os.Stat(scratchFile); statErr != nil {
		t.Fatal("the refused removal must not have deleted the new subdirectory's content")
	}

	// Once the unexpected content is cleared away, removal must succeed —
	// proving the new check does not fail closed permanently on an
	// otherwise perfectly ordinary tree.
	if err := os.RemoveAll(scratchDir); err != nil {
		t.Fatal(err)
	}
	if err := Remove(c, "feat-root-subdir", false); err != nil {
		t.Fatalf("a clean tree must remove once the untracked content is gone: %v", err)
	}
}

// --- Review finding Important 2: a task whose tree is missing could never
// be removed. Preflight blocked with WKT_WORKTREE_MISSING, so plain "rm"
// refused; "--force" then died at os.Rename(treeRoot, staged) with ENOENT,
// reported as WKT_STAGING with a remedy about filesystems that had nothing
// to do with the cause. With no "doctor" in this plan, that left the state
// file, the base pin and the store branch behind forever, and the name
// permanently unusable.

func TestRemoveSucceedsWhenTheTreeWasDeletedByHand(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-hand-deleted", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(c.TreePath(task.Name)); err != nil {
		t.Fatal(err)
	}

	// Plain "rm", no --force: there is nothing left on disk to fence or to
	// force through, so this must succeed on the first, unforced call.
	if err := Remove(c, "feat-hand-deleted", false); err != nil {
		t.Fatalf("removing a task whose tree was deleted by hand must succeed: %v", err)
	}
	if _, err := state.Load(c.StateDir(), "feat-hand-deleted"); err == nil {
		t.Fatal("the task's state file must be gone")
	}

	docsAbs := filepath.Join(c.Workspace, "docs")
	if out := g(t, docsAbs, "for-each-ref", task.Repos[0].BasePinRef); len(out) != 0 {
		t.Fatalf("the base pin must be removed from the workspace repository, found %q", out)
	}

	entries2, err := discover.Walk(c.Workspace, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(c, entries2, "feat-hand-deleted", []string{"docs"}); err != nil {
		t.Fatalf("a name freed by removing a hand-deleted tree must be reusable, got: %v", err)
	}
}

// TestRemoveResumesFromStagingWhenTheTreeWasAlreadyMovedButNotFullyDeleted
// reproduces the exact state test/05_staging_fence.sh deliberately
// produces: a previous "--force" run moved the tree into staging/ (the
// fence) but could not finish deleting it (there, a locked subtree; here,
// simulated directly by moving the tree by hand). The tree root is gone,
// staging/<name> is not — Remove must resume the delete from there rather
// than trying, and failing, to fence a tree that is no longer at its
// original path.
func TestRemoveResumesFromStagingWhenTheTreeWasAlreadyMovedButNotFullyDeleted(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-resume", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	staged := filepath.Join(c.StagingDir(), "feat-resume")
	if err := os.MkdirAll(c.StagingDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(c.TreePath("feat-resume"), staged); err != nil {
		t.Fatal(err)
	}

	// No --force: the deletion this resumes was already authorised by
	// whatever produced the staged-but-undeleted state in the first place.
	if err := Remove(c, "feat-resume", false); err != nil {
		t.Fatalf("removal must resume from staging rather than dying on a missing tree root: %v", err)
	}
	if _, statErr := os.Stat(staged); !os.IsNotExist(statErr) {
		t.Fatal("staging must be fully cleared once removal resumes and completes")
	}
	if _, err := state.Load(c.StateDir(), "feat-resume"); err == nil {
		t.Fatal("the task's state file must be gone")
	}
}

// --- Review finding Important 3: four Preflight checks failed open. Each
// was guarded by "err == nil" with no else, so a git failure silently
// produced zero blockers from that check instead of one — unlike the very
// first check (plain "status --porcelain"), which already correctly
// emitted WKT_CHECK_FAILED in its own else branch. Spec §5.7 is explicit:
// "a failed check of any kind ... is treated as 'would lose work'."

// withFailingGit installs a "git" shim ahead of PATH for the rest of the
// test that fails any invocation whose argument list starts with prefix
// and delegates everything else, byte for byte, to the real git — used to
// make exactly one of Preflight's git calls fail while the others (in
// particular the first, already-correct "status --porcelain" check) keep
// succeeding, so each else branch can be proven in isolation.
func withFailingGit(t *testing.T, prefix string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  \"" + prefix + "\"*)\n" +
		"    echo 'injected failure for testing' >&2\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n" +
		"exec \"" + real + "\" \"$@\"\n"
	shim := filepath.Join(dir, "git")
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestPreflightFailsClosedWhenTheBaseSHACannotBeResolved is the review
// finding's own reproduction: a base_sha the store can no longer resolve
// makes "git rev-list ... <bad-sha>" error, and the old code silently
// dropped the unpushed-commit blocker instead of reporting that the check
// itself failed.
func TestPreflightFailsClosedWhenTheBaseSHACannotBeResolved(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-badbase", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	task.Repos[0].BaseSHA = strings.Repeat("f", 40)
	if err := state.Save(c.StateDir(), task); err != nil {
		t.Fatal(err)
	}

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_CHECK_FAILED" && strings.Contains(b.Detail, "unpushed") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("an unresolvable base SHA must fail the unpushed-commit check closed, got %+v", blockers)
	}

	if err := Remove(c, task.Name, false); err == nil {
		t.Fatal("a failed unpushed-commit check must block removal: 'cannot tell' is 'would lose work'")
	}
	if _, statErr := os.Stat(task.Repos[0].WorktreePath); statErr != nil {
		t.Fatal("the refused removal must not have deleted anything")
	}
}

func TestPreflightFailsClosedWhenTheIgnoredContentCheckFails(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-ignoredfail", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	withFailingGit(t, "status --porcelain --ignored=matching")

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_CHECK_FAILED" && strings.Contains(b.Detail, "ignored-content") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a failed ignored-content check must fail closed, got %+v", blockers)
	}

	if err := Remove(c, task.Name, false); err == nil {
		t.Fatal("a failed ignored-content check must block removal")
	}
}

func TestPreflightFailsClosedWhenTheInProgressCheckFails(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-inprogressfail", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	withFailingGit(t, "rev-parse --git-path")

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_CHECK_FAILED" && strings.Contains(b.Detail, "in-progress check") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a failed in-progress check must fail closed, got %+v", blockers)
	}

	if err := Remove(c, task.Name, false); err == nil {
		t.Fatal("a failed in-progress check must block removal")
	}
}

func TestPreflightFailsClosedWhenTheSubmoduleCheckFails(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-submodulefail", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	withFailingGit(t, "submodule status --recursive")

	blockers, err := Preflight(c, task)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, b := range blockers {
		if b.Code == "WKT_CHECK_FAILED" && strings.Contains(b.Detail, "submodule check") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a failed submodule check must fail closed, got %+v", blockers)
	}

	if err := Remove(c, task.Name, false); err == nil {
		t.Fatal("a failed submodule check must block removal")
	}
}

// TestRefusalSeparatesProblemsFromRemedy covers adversarial finding F5. The
// refusal used to pack every blocker into the "remedy" field as
// "CODE repo path detail", so the field meant to say what to do listed what
// was wrong, with an empty path and raw git output inside it.
func TestRefusalSeparatesProblemsFromRemedy(t *testing.T) {
	c, entries := fixture(t)
	task, err := Create(c, entries, "feat-shape", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	wt := task.Repos[0].WorktreePath
	if err := os.WriteFile(filepath.Join(wt, ".gitignore"), []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(t, wt, "add", ".gitignore")
	g(t, wt, "commit", "-qm", "ignore dist")
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "dist", "out.js"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = Remove(c, "feat-shape", false)
	if err == nil {
		t.Fatal("a dirty tree must refuse")
	}
	var e *wkterr.E
	if !errors.As(err, &e) {
		t.Fatalf("want a typed error, got %v", err)
	}
	if len(e.Problems) == 0 {
		t.Fatal("the refusal must carry its blockers as problems")
	}
	var sawDirty, sawIgnored bool
	for _, p := range e.Problems {
		switch p.Code {
		case "WKT_DIRTY":
			sawDirty = true
			if p.Repo == "" || p.Path == "" {
				t.Fatalf("a blocker must name its repository and path: %+v", p)
			}
			if p.Info {
				t.Fatal("uncommitted work blocks")
			}
			// The detail used to be one raw line of git status --porcelain,
			// which both leaks git's format and hides every path after the
			// first. Prose, yes — but prose that still names the paths: an
			// empty detail passes a "not porcelain" check while saying
			// nothing at all.
			if p.Detail == "" {
				t.Fatal("the dirty blocker must say what changed")
			}
			if strings.HasPrefix(p.Detail, " M") || strings.HasPrefix(p.Detail, "??") {
				t.Fatalf("detail must be prose, not porcelain: %q", p.Detail)
			}
			if !strings.Contains(p.Detail, "f.txt") {
				t.Fatalf("the modified path must appear in the detail: %q", p.Detail)
			}
		case "WKT_REGENERABLE_IGNORED":
			sawIgnored = true
			if !p.Info {
				t.Fatal("regenerable ignored content is listed, never blocking")
			}
		}
	}
	if !sawDirty || !sawIgnored {
		t.Fatalf("want both the dirty blocker and the informational one, got %+v", e.Problems)
	}
	if len(e.Remedy) == 0 {
		t.Fatal("a refusal must say what to do")
	}
	for _, r := range e.Remedy {
		if strings.Contains(r, "WKT_") {
			t.Fatalf("remedy must hold actions, not problem codes: %q", r)
		}
	}
}

// TestDescribePorcelainKeepsThePaths pins the helper directly. Its first
// version ran TrimSpace over the whole blob, which shifted " M f" left by one
// column and silently produced "1 changed: .txt" — a detail that reads fine
// and names a file that does not exist.
func TestDescribePorcelainKeepsThePaths(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{" M f.txt\n", "1 changed: f.txt"},
		{"M f.txt\n", "1 changed: f.txt"}, // gitx.Run trims, so the first line loses its leading space
		{"?? new.txt\n", "1 changed: new.txt"},
		{"M  staged.txt\n", "1 changed: staged.txt"},
		{"R  old.txt -> new.txt\n", "1 changed: new.txt"},
		{" M a\n?? b\n", "2 changed: a, b"},
		{" M a\n M b\n M c\n M d\n", "4 changed, including a, b, c"},
		{"", ""},
	} {
		if got := describePorcelain(tc.in); got != tc.want {
			t.Errorf("describePorcelain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/task/ -run TestRemove -v`
Expected: FAIL — `undefined: Remove`.

- [ ] **Step 3: Write the preflight**

```go
// internal/task/remove.go
// Refuse-only teardown: Remove never deletes anything until Preflight finds
// no blocker (or the caller passes --force for the ordinary ones), and even
// then a foreign repository or a submodule is never removable. Anything that
// deletes enumerates the filesystem, never the state file — state says what
// should be there, the disk says what is there.
package task

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"wkt/internal/container"
	"wkt/internal/gitx"
	"wkt/internal/paths"
	"wkt/internal/state"
	"wkt/internal/tree"
	"wkt/internal/wkterr"
)

// Severity distinguishes a blocker that gates removal from one that is
// merely reported. The zero value ("") means blocking, so every existing
// code that never sets Severity keeps its original meaning unchanged.
type Blocker struct {
	Code     string
	Repo     string
	Path     string
	Detail   string
	Severity string // "" (blocking, default) or "info" (listed, not blocking)
}

// regenerable lists path-component sequences that mark git-ignored content
// as safe to delete without a warning: build output and dependency caches
// any build system regenerates on demand. This is deliberately an
// allowlist, not a denylist. A denylist of "precious" filename substrings
// was tried first and failed the one property that matters here: it cannot
// be made complete. A gitignored "server.key" — an entirely ordinary TLS or
// SSH private key name — matched none of its five entries and was deleted
// with zero blockers and no --force. Now unknown ignored content blocks by
// default; only what's provably regenerable is exempt, and even that is
// still reported, not silently passed over (see the "info" Severity below).
var regenerable = [][]string{
	{"node_modules"}, {"dist"}, {"build"}, {"target"},
	{".venv"}, {"venv"}, {".next"}, {".nuxt"},
	{"__pycache__"}, {".pytest_cache"}, {"coverage"},
	{".gradle"}, {".tox"}, {"vendor", "bundle"}, {".terraform"},
	// Operating-system artifacts: recreated automatically by the OS/file
	// manager and never carry any work of their own. Without these, a
	// gitignored ".DS_Store" (which Finder writes into essentially every
	// directory a macOS user has so much as opened, and which nearly every
	// macOS repository gitignores) would make "wkt rm" refuse on almost
	// every real tree on the primary development platform — teaching
	// people to reach for --force without reading the list, which defeats
	// the reason this check exists at all, including for the "server.key"
	// case it was just fixed to catch.
	{".DS_Store"}, {"Thumbs.db"}, {".Spotlight-V100"}, {".fseventsd"},
	{".Trashes"}, {"desktop.ini"},
}

// isRegenerable reports whether relPath — git's own slash-separated,
// repo-relative reporting of an ignored path — contains one of the
// regenerable sequences as whole path components. It never matches a bare
// substring: "target" matches ".../target/..." but not
// ".../my-target-cache/...", and a name like "server.key" never matches
// anything here at all, which is the point.
func isRegenerable(relPath string) bool {
	comps := strings.Split(strings.TrimSuffix(relPath, "/"), "/")
	for _, seq := range regenerable {
		if containsComponentSequence(comps, seq) {
			return true
		}
	}
	return false
}

func containsComponentSequence(comps, seq []string) bool {
	if len(seq) > len(comps) {
		return false
	}
	for i := 0; i+len(seq) <= len(comps); i++ {
		match := true
		for j, s := range seq {
			if comps[i+j] != s {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Preflight enumerates every reason removing t's tree would lose work. It
// walks the real filesystem under the tree root — never the state file — so
// content a task's state knows nothing about (a foreign repository dropped
// into a worktree, a link slot an agent turned into a real file) is still
// found. It never descends into a symlink: those are workspace back-fill
// slots, not part of the tree wkt owns.
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
			out = append(out, Blocker{Code: "WKT_DIRTY", Repo: r.RelPath, Path: wt, Detail: describePorcelain(s)})
		}
		// 3: ignored content. git's own refusal never fires on any of it (H1).
		// A bulk-ignored directory collapses to one "!! dir/" line — its
		// contents are never listed individually — so an allowlisted
		// directory is trusted whole, and an unrecognised one blocks whole:
		// unknown means precious, not just unknown-named files. A failed
		// check is not "no ignored content" (spec §5.7: "a failed check of
		// any kind ... is treated as 'would lose work'") — the first check
		// above already got this right; this one and the three below it
		// used to fail open, each on its own "err == nil" with no else.
		if s, err := gitx.Run(wt, "status", "--porcelain", "--ignored=matching"); err == nil {
			for _, line := range strings.Split(s, "\n") {
				if !strings.HasPrefix(line, "!! ") {
					continue
				}
				rel := strings.TrimPrefix(line, "!! ")
				if isRegenerable(rel) {
					out = append(out, Blocker{Code: "WKT_REGENERABLE_IGNORED", Repo: r.RelPath, Path: rel, Severity: "info"})
					continue
				}
				out = append(out, Blocker{Code: "WKT_PRECIOUS_IGNORED", Repo: r.RelPath, Path: rel})
			}
		} else {
			out = append(out, Blocker{Code: "WKT_CHECK_FAILED", Repo: r.RelPath, Path: wt, Detail: "ignored-content check"})
		}
		// 4: in-progress operations — invisible to status --porcelain (H2):
		// empty during an interactive rebase pause, a bisect, or a detached
		// HEAD, so removal must not be gated on status alone.
		for _, marker := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"} {
			p, err := gitx.Run(wt, "rev-parse", "--git-path", marker)
			if err != nil {
				out = append(out, Blocker{Code: "WKT_CHECK_FAILED", Repo: r.RelPath, Path: wt, Detail: "in-progress check (" + marker + ")"})
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
		if n, err := gitx.Run(wt, args...); err == nil {
			if n != "" && n != "0" {
				out = append(out, Blocker{Code: "WKT_UNPUSHED", Repo: r.RelPath, Detail: plural(n, "commit")})
			}
		} else {
			out = append(out, Blocker{Code: "WKT_CHECK_FAILED", Repo: r.RelPath, Path: wt, Detail: "unpushed-commit check"})
		}
		// 6: submodules — worktree remove refuses unconditionally, and --force
		// destroys their object store (spec §5.7). Run from the worktree, not the
		// store, since submodule state is per-worktree (index-based) and this
		// fires even when the addition is fully committed and status is clean.
		if sm, err := gitx.Run(wt, "submodule", "status", "--recursive"); err == nil {
			if strings.TrimSpace(sm) != "" {
				out = append(out, Blocker{Code: "WKT_SUBMODULE", Repo: r.RelPath, Detail: "submodule " + submodulePath(sm)})
			}
		} else {
			out = append(out, Blocker{Code: "WKT_CHECK_FAILED", Repo: r.RelPath, Path: wt, Detail: "submodule check"})
		}
	}

	// 7: a foreign .git anywhere in the tree, found without following symlinks.
	known := map[string]bool{}
	for _, r := range t.Repos {
		canon, _ := paths.Canonical(r.WorktreePath)
		known[canon] = true
	}

	// 7b: untracked tree-root content (review Critical 1). Preflight's other
	// checks all scope to a repository's WorktreePath, so content living at
	// the tree root itself — a cross-repo plan, a generated report, agent
	// scratch — was invisible to every check and simply vanished into
	// os.RemoveAll(staged). Content *inside* a worktree is already fully
	// covered by "git status --porcelain" above, so this only needs to
	// classify entries that are not a descendant of a recorded worktree:
	// every such entry must be the worktree's own root, a recorded link
	// slot (symlink or copy), or a pure ancestor directory on the path to
	// one of those — tree.Materialise already recorded every sibling of an
	// ancestor as a link slot or copy at create time, so anything left over
	// is exactly what an agent added afterward.
	worktreeRel := map[string]bool{}
	for _, r := range t.Repos {
		worktreeRel[filepath.FromSlash(r.RelPath)] = true
	}
	linkRel := map[string]bool{}
	copyHash := map[string]string{}
	for _, slot := range t.Links {
		rel := filepath.FromSlash(slot.RelPath)
		linkRel[rel] = true
		if slot.Type == "copy" {
			copyHash[rel] = slot.Hash
		}
	}
	sep := string(filepath.Separator)
	underWorktree := func(rel string) bool {
		for wt := range worktreeRel {
			if strings.HasPrefix(rel, wt+sep) {
				return true
			}
		}
		return false
	}
	ancestorOfSomething := func(rel string) bool {
		prefix := rel + sep
		for wt := range worktreeRel {
			if strings.HasPrefix(wt, prefix) {
				return true
			}
		}
		for ls := range linkRel {
			if strings.HasPrefix(ls, prefix) {
				return true
			}
		}
		return false
	}

	_ = filepath.WalkDir(treeRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != treeRoot {
			if rel, relErr := filepath.Rel(treeRoot, p); relErr == nil {
				rel = filepath.Clean(rel)
				if !underWorktree(rel) {
					switch {
					case worktreeRel[rel]:
						// a worktree's own root: fall through so the walk still
						// descends into it below, for the foreign-.git scan.
					case linkRel[rel]:
						// a recorded link slot. A copy is not silently accepted
						// on the strength of occupying the right path alone: if
						// its hash no longer matches, it still falls through to
						// the untracked-content blocker below, as a defence in
						// depth alongside the dedicated WKT_COPY_DIVERGED check
						// (section 8).
						if wantHash, isCopy := copyHash[rel]; isCopy {
							if sum, hErr := tree.Hash(p); hErr != nil || sum != wantHash {
								out = append(out, Blocker{Code: "WKT_UNTRACKED_TREE_CONTENT", Path: p})
							}
						}
					case ancestorOfSomething(rel):
						// a real directory on the path to something recorded;
						// fall through and keep descending.
					default:
						out = append(out, Blocker{Code: "WKT_UNTRACKED_TREE_CONTENT", Path: p})
						if d.IsDir() {
							return fs.SkipDir
						}
						return nil
					}
				}
			}
		}
		// fs.WalkDir's SkipDir has two different meanings depending on the
		// entry it's returned for: on a directory it means "don't descend
		// into this", but on anything else it means "skip the rest of this
		// directory's *siblings*" — a completely different, much more
		// destructive effect. A symlink is never a directory as far as
		// DirEntry.IsDir() is concerned (Lstat, not Stat), and WalkDir never
		// follows symlinks on its own regardless, so SkipDir here bought
		// nothing and only tripped the sibling-skipping behaviour: whenever a
		// back-fill symlink (e.g. "docs") sorted before other tree content
		// (e.g. "services"), it silently truncated the scan of the entire
		// rest of the tree root, hiding every materialised worktree from the
		// foreign-repo check below. Confirmed against real fs.WalkDir before
		// this fix. plain "return nil" is correct and sufficient: the walk
		// already never recurses into a symlink.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.Name() != ".git" {
			return nil
		}
		if !d.IsDir() {
			// A regular-file .git marker is either our own materialised
			// worktree's marker (excluded below via known) or a submodule
			// checkout's marker, whose gitdir always points into the owning
			// repository's own .git/modules/<name> (git's own convention).
			// A submodule is not "a repository wkt did not create" — it is
			// nested inside one wkt did — and it already gets a precise,
			// correctly-worded blocker (WKT_SUBMODULE, with the right
			// remedy) from the per-repo check above. Flagging it here too as
			// WKT_FOREIGN_REPO would mislead a user with the wrong remedy
			// ("move it out of the tree") and, worse, made the submodule
			// hard-force-block in Remove() untestable in isolation: the
			// foreign-repo hard block always fired first regardless of
			// whether the submodule-specific guard was even present.
			// Confirmed by deliberately disabling the submodule guard and
			// finding TestRemoveRefusesOnSubmoduleEvenWithForce still passed
			// before this fix.
			if b, rerr := os.ReadFile(p); rerr == nil &&
				strings.Contains(string(b), string(filepath.Separator)+"modules"+string(filepath.Separator)) {
				return nil
			}
		}
		owner, _ := paths.Canonical(filepath.Dir(p))
		if !known[owner] {
			out = append(out, Blocker{Code: "WKT_FOREIGN_REPO", Path: filepath.Dir(p)})
		}
		// The same asymmetry applies here: a linked worktree's own ".git"
		// marker is a regular *file* (every materialised worktree has
		// exactly this), so SkipDir returned unconditionally on it also hit
		// the sibling-skipping case and stopped scanning the remainder of
		// the worktree right after its own marker was visited — a foreign
		// repository nested anywhere sorting after ".git" (almost anything)
		// was never found. Only an actual .git *directory* (an ordinary
		// nested repository, foreign or otherwise) should stop descent.
		// Confirmed against real fs.WalkDir before this fix; see
		// remove_test.go's
		// TestRemoveDetectsForeignRepoNestedInsideMaterialisedWorktree.
		if d.IsDir() {
			return fs.SkipDir
		}
		return nil
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
// Remove refuses while any blocker exists. With force it overrides the
// ordinary ones, but never a foreign repository (its history exists nowhere
// else) and never a submodule (its object store lives under the doomed
// worktree; --force would destroy objects reachable from neither the store
// nor the original submodule repository) — spec §5.7.
//
// The fence: one os.Rename moves the whole tree into staging/ first, making
// a still-running agent's cwd vanish atomically, before anything is deleted.
// Deletion goes through os.RemoveAll on a path this function computed —
// never a shell command, never a symlink-following walker (spec H3): "rm -rf
// link/" with a trailing slash destroys the symlink's target, not the link.
func Remove(c container.C, name string, force bool) error {
	t, err := state.Load(c.StateDir(), name)
	if err != nil {
		return err
	}

	treeRoot := c.TreePath(name)
	staged := filepath.Join(c.StagingDir(), name)

	// A tree that is simply gone cannot be preflighted — there is nothing
	// left on disk for a blocker to name — and the old behaviour tried
	// anyway: Preflight emitted a blocking WKT_WORKTREE_MISSING per
	// repository, so plain "rm" refused, and "--force" then died at
	// os.Rename(treeRoot, staged) with ENOENT, reported as WKT_STAGING with
	// a remedy about filesystems that had nothing to do with the cause.
	// With no "doctor" in this plan, that left the task permanently
	// unremovable: the state file, the base pin in the workspace repository
	// and the store branch all survived forever, and the name could never
	// be reused. Skip the fence entirely and go straight to store and state
	// cleanup — finishRemove's own os.RemoveAll(staged) is a no-op when
	// staging/<name> does not exist, and resumes an incomplete delete
	// (test/05_staging_fence.sh deliberately produces exactly that state)
	// when it does: that deletion was already authorised by the --force
	// that performed the original move, so it does not need re-authorising.
	if _, statErr := os.Stat(treeRoot); os.IsNotExist(statErr) {
		return finishRemove(c, t, name, staged)
	}

	all, err := Preflight(c, t)
	if err != nil {
		return err
	}
	// Severity "info" entries (regenerable ignored content) are reported but
	// never gate removal or the force decision — only the blocking ones do.
	var blocking []Blocker
	for _, b := range all {
		if b.Severity != "info" {
			blocking = append(blocking, b)
		}
	}
	for _, b := range blocking {
		if b.Code == "WKT_FOREIGN_REPO" {
			return wkterr.New(b.Code, "a repository wkt did not create lives inside the tree").
				WithPath(b.Path).WithRemedy("move it out of the tree, then retry")
		}
		if b.Code == "WKT_SUBMODULE" && force {
			return wkterr.New(b.Code, "a submodule is present; --force would destroy its objects").
				WithRepo(b.Repo).WithRemedy("push the submodule's commits", "git submodule deinit", "then retry")
		}
	}
	if len(blocking) > 0 && !force {
		e := wkterr.New("WKT_WOULD_LOSE_WORK", "removal would lose work")
		// List every entry, blocking and informational alike, so --force
		// doesn't become reflexive: the user sees that most of what's in the
		// way is build output, and one line is their .env (spec §5.7). They
		// go in Problems; Remedy is reserved for what to actually do.
		for _, b := range all {
			e = e.WithProblem(wkterr.Problem{
				Code: b.Code, Repo: b.Repo, Path: b.Path,
				Detail: b.Detail, Info: b.Severity == "info",
			})
		}
		return e.WithRemedy(remedyFor(blocking, name)...)
	}

	if err := os.MkdirAll(c.StagingDir(), 0o700); err != nil {
		return wkterr.New("WKT_STAGING", "cannot create the staging directory").WithPath(c.StagingDir())
	}
	if err := os.Rename(treeRoot, staged); err != nil {
		// Degrading the fence to a per-repo, non-atomic sequence when
		// staging/ is on another filesystem would defeat the reason the
		// fence exists (a still-running agent's cwd must vanish atomically),
		// so this refuses rather than falling back. Name the cause when it's
		// the well-known one (EXDEV) instead of only echoing the raw OS
		// error, so the remedy is actionable on first read.
		msg := "cannot move the tree into staging"
		remedy := "relocate the container so its trees/ and staging/ share one filesystem, then retry"
		if errors.Is(err, syscall.EXDEV) {
			msg = "the tree and staging/ are on different filesystems, so the removal fence cannot be atomic"
		}
		return wkterr.New("WKT_STAGING", msg).
			WithPath(treeRoot).WithFound(err.Error()).
			WithRemedy(remedy)
	}

	return finishRemove(c, t, name, staged)
}

// finishRemove does the git-side cleanup (unlock, prune, delete the task
// branch, delete the base pin from the workspace repository) and removes
// the task's state, for every path that reaches it: a normal removal just
// past the staging fence, and Remove's missing-tree shortcut above (where
// staged may or may not exist — os.RemoveAll is a no-op on a path that
// isn't there, so this same call resumes an interrupted delete without a
// separate branch for that case).
func finishRemove(c container.C, t state.Task, name, staged string) error {
	for _, r := range t.Repos {
		sp := filepath.Join(c.StoreDir(), r.StoreID+".git")
		_, _ = gitx.Run(sp, "worktree", "unlock", r.WorktreePath)
		_, _ = gitx.Run(sp, "worktree", "prune")
		// The branch a task's worktree was created on (spec §5.4) is never
		// removed by "worktree prune" — confirmed against real git: after
		// unlock+prune the admin entry is gone but "branch --list" still shows
		// it. Left behind, it makes the store un-reusable: a later Create of a
		// task with the same name fails Validate's WKT_BRANCH_EXISTS check
		// against the store even though the task was cleanly removed. See
		// remove_test.go's TestRemoveCleansUpStoreBranchSoTaskNameCanBeReused.
		_, _ = gitx.Run(sp, "branch", "-D", r.Branch)
		_, _ = gitx.Run(r.AbsPath, "update-ref", "-d", r.BasePinRef)
	}
	if err := os.RemoveAll(staged); err != nil {
		return wkterr.New("WKT_REMOVE_FAILED", "cannot remove the staged tree").WithPath(staged)
	}
	if err := os.Remove(filepath.Join(c.StateDir(), name+".json")); err != nil && !os.IsNotExist(err) {
		return wkterr.New("WKT_STATE_WRITE", "cannot remove task state").WithPath(name)
	}
	return nil
}

// describePorcelain turns "git status --porcelain" output into one line of
// prose. Reporting only its first line both leaked git's format and hid
// every path after the first.
func describePorcelain(s string) string {
	// Never slice at a fixed column. Porcelain's status is two columns
	// (" M f.txt"), but gitx.Run returns trimmed stdout, so the *first*
	// line arrives with its leading space already gone ("M f.txt") while
	// the rest keep theirs. Splitting on the first space after the status
	// handles both; slicing at [3:] silently ate a character of the first
	// path and reported a file that does not exist.
	var paths []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		f := strings.SplitN(strings.TrimLeft(l, " "), " ", 2)
		if len(f) != 2 {
			continue
		}
		p := strings.TrimSpace(f[1])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+len(" -> "):] // a rename: report where it landed
		}
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	if len(paths) <= 3 {
		return strconv.Itoa(len(paths)) + " changed: " + strings.Join(paths, ", ")
	}
	return strconv.Itoa(len(paths)) + " changed, including " + strings.Join(paths[:3], ", ")
}

// remedyFor answers the only question a refusal leaves open: what now. It
// names the action each *blocking* code needs, deduplicated and in a stable
// order, and never repeats the problem list back at the user.
func remedyFor(blocking []Blocker, name string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	forceable := true
	for _, b := range blocking {
		switch b.Code {
		case "WKT_DIRTY", "WKT_UNTRACKED_TREE_CONTENT":
			add("commit or stash the changes, or move them out of the tree")
		case "WKT_UNPUSHED":
			add("push the commits, or keep the task")
		case "WKT_PRECIOUS_IGNORED":
			add("copy the ignored files you need out of the tree")
		case "WKT_IN_PROGRESS":
			add("finish or abort the in-progress git operation")
		case "WKT_SUBMODULE":
			add("push the submodule's commits, then git submodule deinit it")
			forceable = false
		case "WKT_FOREIGN_REPO":
			add("move the repository out of the tree: its history exists nowhere else")
			forceable = false
		case "WKT_COPY_DIVERGED", "WKT_LINK_SLOT_CHANGED", "WKT_LINK_SLOT_MISSING":
			add("reconcile the changed file against the workspace copy")
		case "WKT_CHECK_FAILED":
			add("re-run once the repository is readable: a check that cannot run is treated as work at risk")
			forceable = false
		case "WKT_WORKTREE_MISSING":
			add("the worktree is gone from disk; wkt rm --force finishes the removal")
		}
	}
	if forceable {
		add("or wkt rm " + name + " --force once you are sure nothing above matters")
	}
	return out
}

// plural keeps counted details reading like prose rather than like a log line.
func plural(n, noun string) string {
	if n == "1" {
		return n + " " + noun
	}
	return n + " " + noun + "s"
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/task/ -v`
Expected: all thirty-five tests in the package PASS — the ten from task 8 and the twenty-five here.

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
  - `wkt init --exclude a/inner,...` — spec §5.3 rule 6's escape hatch for a genuine nested repository. Exclusions are cumulative across runs (this run's flag plus what an earlier one recorded in `state/container.json`), and excluding a path that is not nested fails rather than passing silently.
  - Documented aliases `create` → `new` and `cleanup` → `rm`, because the acceptance battery drives those verbs (spec §7.1).
  - `new` warns on stderr, before creating anything, when a selected repository carries a submodule (spec §5.7) — `rm` refuses on one even with `--force`, so the task would otherwise be unremovable by any wkt command.

**Traps:**

- `return fail(stderr, err) | 2` is a bitwise OR where an exit code was meant.
- Exit 4 must be reachable for the ordinary "init was never run" case; the spec's
  command table promises it and the battery drives it.
- `wkt init` on a nonexistent or repo-free directory must fail, or a typo in
  `--workspace` looks like success.
- Parsing positionals-before-flags discards the task name and every later flag
  when a flag comes first. Split the argument vector structurally, deriving the
  set of value-taking flags **from the `FlagSet` itself** rather than from a
  hand-maintained list that drifts.
- The submodule warning belongs on `new`, not only in `rm`'s refusal: learning
  at teardown that the task cannot be torn down is learning too late.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/cli_test.go
package cli

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wkt/internal/wkterr"
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

// --- exit-code contract, exercised beyond "not zero" ---

// TestFailMapsErrorCodesToContractExitCodes locks the fail() mapping table
// down directly: each wkterr code must land on its documented exit code, not
// merely "something non-zero". A regression that mapped every error to 1
// (or every error to 2) would still pass a test that only checked "!= 0".
func TestFailMapsErrorCodesToContractExitCodes(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{"WKT_TASK_EXISTS", 2},
		{"WKT_NO_CONTAINER", 4},
		{"WKT_TREE_MISSING", 3},
		{"WKT_NO_TASK", 1},
		{"WKT_DIRTY", 1},
		{"WKT_GIT_FAILED", 1},
	}
	for _, c := range cases {
		var errb bytes.Buffer
		got := fail(&errb, wkterr.New(c.code, "x"))
		if got != c.want {
			t.Errorf("fail(%s) = %d, want %d", c.code, got, c.want)
		}
		if errb.Len() == 0 {
			t.Errorf("fail(%s) wrote nothing to stderr", c.code)
		}
		if strings.Contains(errb.String(), "\t") {
			t.Errorf("fail(%s) leaked a raw-looking payload: %s", c.code, errb.String())
		}
	}
}

// TestStatusInfoSeverityDoesNotTriggerDrift is correction 2: an ordinary
// regenerable directory like node_modules must be reported (so --force
// doesn't become reflexive) but must NOT flip status's exit code to 3. A
// status loop that set drift on every Preflight entry — info or not — would
// fail this test; one that filtered by severity passes it.
func TestStatusInfoSeverityDoesNotTriggerDrift(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	wsRepo := filepath.Join(ws, "svc-a")
	seedRepo(t, wsRepo)

	// node_modules must be gitignored for git to report it as "!!" ignored,
	// which is what Preflight's regenerable classifier keys on. The
	// .gitignore is committed into the *workspace* repository, before "new"
	// resolves the task's base — so it becomes part of the base commit
	// itself rather than an unpushed commit made inside the tree (which
	// would introduce its own, unrelated WKT_UNPUSHED blocker and confound
	// this test's claim that ONLY the info-severity entry is present).
	if err := os.WriteFile(filepath.Join(wsRepo, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "ignore node_modules"}} {
		full := append([]string{"-c", "user.email=e@x", "-c", "user.name=t"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = wsRepo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	if code := Run([]string{"new", "t-info", "--workspace", ws, "--all"}, &out, &errb); code != 0 {
		t.Fatalf("new exited %d: %s", code, errb.String())
	}
	out.Reset()
	Run([]string{"path", "t-info", "--workspace", ws}, &out, &errb)
	treePath := strings.TrimSpace(out.String())

	repoDir := filepath.Join(treePath, "svc-a")
	if err := os.MkdirAll(filepath.Join(repoDir, "node_modules", "leftpad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "node_modules", "leftpad", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	code := Run([]string{"status", "t-info", "--workspace", ws}, &out, &errb)
	if code != 0 {
		t.Fatalf("status with only regenerable ignored content exited %d, want 0: stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "WKT_REGENERABLE_IGNORED") {
		t.Fatalf("status must still report the regenerable entry, got: %s", out.String())
	}
}

// TestStatusRealBlockerTriggersDriftExitThree is the other half of
// correction 2: a genuine blocker (an uncommitted change) must still flip
// status to exit 3. Without this paired test, a status loop that never sets
// drift at all would also pass the info-severity test above.
func TestStatusRealBlockerTriggersDriftExitThree(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))

	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	Run([]string{"new", "t-dirty", "--workspace", ws, "--all"}, &out, &errb)
	out.Reset()
	Run([]string{"path", "t-dirty", "--workspace", ws}, &out, &errb)
	treePath := strings.TrimSpace(out.String())

	if err := os.WriteFile(filepath.Join(treePath, "svc-a", "f.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	code := Run([]string{"status", "t-dirty", "--workspace", ws}, &out, &errb)
	if code != 3 {
		t.Fatalf("status with an uncommitted change exited %d, want 3: stdout=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "WKT_DIRTY") {
		t.Fatalf("status must report the dirty blocker, got: %s", out.String())
	}
}

// TestPathFailsWhenTreeMissingFromDisk is correction 3: state can still have
// a record of the task after its tree directory is gone from disk (e.g.
// deleted outside wkt). path must refuse rather than print a path to
// nothing — and, crucially, must not merely print the path anyway.
func TestPathFailsWhenTreeMissingFromDisk(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))

	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	Run([]string{"new", "t-gone", "--workspace", ws, "--all"}, &out, &errb)
	out.Reset()
	Run([]string{"path", "t-gone", "--workspace", ws}, &out, &errb)
	treePath := strings.TrimSpace(out.String())
	if treePath == "" {
		t.Fatal("setup: expected a tree path")
	}

	if err := os.RemoveAll(treePath); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	code := Run([]string{"path", "t-gone", "--workspace", ws}, &out, &errb)
	if code == 0 {
		t.Fatalf("path must not exit 0 once the tree is gone from disk, got stdout=%q", out.String())
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("path must not print a path to nothing, got stdout=%q", out.String())
	}
	if code != 3 {
		t.Fatalf("path on a state/disk mismatch must exit 3 (drift), got %d", code)
	}
}

// TestRmRefusesOnDirtyTreeExitsOne mirrors the acceptance battery's
// destructive-cleanup test: a plain rm on a tree with uncommitted work must
// refuse (exit 1, not 0, not silently succeed) and must leave the tree
// untouched.
func TestRmRefusesOnDirtyTreeExitsOne(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "docs"))

	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	Run([]string{"new", "t-refuse", "--workspace", ws, "--all"}, &out, &errb)
	out.Reset()
	Run([]string{"path", "t-refuse", "--workspace", ws}, &out, &errb)
	treePath := strings.TrimSpace(out.String())

	if err := os.WriteFile(filepath.Join(treePath, "docs", "untracked.md"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	code := Run([]string{"rm", "t-refuse", "--workspace", ws}, &out, &errb)
	if code != 1 {
		t.Fatalf("rm on a dirty tree exited %d, want 1", code)
	}
	if _, err := os.Stat(treePath); err != nil {
		t.Fatalf("rm must not remove a dirty tree: %v", err)
	}
}

// TestCreateAndCleanupAreDocumentedAliases checks the acceptance battery's
// two required verbs actually reach new/rm, not merely that some command
// named "create" exists and returns 2 for anything.
func TestCreateAndCleanupAreDocumentedAliases(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))

	var out, errb bytes.Buffer
	Run([]string{"init", "--workspace", ws}, &out, &errb)
	out.Reset()
	if code := Run([]string{"create", "t-alias", "--workspace", ws, "--all"}, &out, &errb); code != 0 {
		t.Fatalf("create exited %d: %s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"path", "t-alias", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("path after create exited %d: %s", code, errb.String())
	}
	treePath := strings.TrimSpace(out.String())
	if _, err := os.Stat(treePath); err != nil {
		t.Fatalf("create must materialise a tree just like new: %v", err)
	}
	out.Reset()
	if code := Run([]string{"cleanup", "t-alias", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("cleanup exited %d: %s", code, errb.String())
	}
	if _, err := os.Stat(treePath); !os.IsNotExist(err) {
		t.Fatal("cleanup must remove the tree just like rm")
	}
}

// TestUsageErrorsExitTwo checks the "2 = usage error" half of the contract
// with real malformed invocations rather than the happy path.
func TestUsageErrorsExitTwo(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{}, &out, &errb); code != 2 {
		t.Fatalf("no args exited %d, want 2", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"bogus-command"}, &out, &errb); code != 2 {
		t.Fatalf("unknown command exited %d, want 2", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"new"}, &out, &errb); code != 2 {
		t.Fatalf("new with no task name exited %d, want 2", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"new", "t", "--not-a-real-flag"}, &out, &errb); code != 2 {
		t.Fatalf("an unparsable flag set exited %d, want 2", code)
	}
}

// --- review round 2 ---

// TestPathAndRmRequireATaskNameExitTwo is minor fix 4: an empty task name on
// path or rm is a usage error, exactly like new, not whatever incidental
// error state.Load or task.Remove happens to produce for an empty name.
func TestPathAndRmRequireATaskNameExitTwo(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"path", "--workspace", ws}, &out, &errb); code != 2 {
		t.Fatalf("path with no task name exited %d, want 2", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"rm", "--workspace", ws}, &out, &errb); code != 2 {
		t.Fatalf("rm with no task name exited %d, want 2", code)
	}
}

// TestUninitialisedContainerExitsFour is Important fix 1: new, path, status
// and rm against a workspace that was never `wkt init`-ed must all exit 4,
// not whatever incidental error each command happens to hit first. This is
// an integration test against real commands, not just a check on fail()'s
// mapping table — it would have caught the original bug (status silently
// exiting 0, new/path/rm exiting 1) that the mapping-only test could not.
func TestUninitialisedContainerExitsFour(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))
	// Deliberately no "init" call.

	cases := [][]string{
		{"new", "t1", "--workspace", ws, "--all"},
		{"path", "t1", "--workspace", ws},
		{"status", "--workspace", ws},
		{"rm", "t1", "--workspace", ws},
	}
	for _, args := range cases {
		var out, errb bytes.Buffer
		code := Run(args, &out, &errb)
		if code != 4 {
			t.Errorf("%v against an uninitialised container exited %d, want 4: stdout=%q stderr=%q",
				args, code, out.String(), errb.String())
		}
		if !strings.Contains(errb.String(), "WKT_NO_CONTAINER") {
			t.Errorf("%v: stderr must report WKT_NO_CONTAINER, got %q", args, errb.String())
		}
	}
}

// TestInitRefusesNonexistentWorkspace and TestInitRefusesWorkspaceWithNoRepos
// are Important fix 2: init must not silently succeed and create an empty
// container for a workspace that plainly isn't one — a typo in --workspace
// looks exactly like success otherwise, since discover.Walk swallows a
// root-level walk error and simply returns zero entries.
func TestInitRefusesNonexistentWorkspace(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "does-not-exist")

	var out, errb bytes.Buffer
	code := Run([]string{"init", "--workspace", ws}, &out, &errb)
	if code == 0 {
		t.Fatalf("init on a nonexistent workspace exited 0, want a typed failure; stdout=%q", out.String())
	}
	if _, err := os.Stat(ws + ".worktrees"); !os.IsNotExist(err) {
		t.Fatal("init must not create a container for a workspace that does not exist")
	}
}

func TestInitRefusesWorkspaceWithNoRepos(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := Run([]string{"init", "--workspace", ws}, &out, &errb)
	if code == 0 {
		t.Fatalf("init on a workspace with zero repositories exited 0, want a typed failure; stdout=%q", out.String())
	}
	if _, err := os.Stat(ws + ".worktrees"); !os.IsNotExist(err) {
		t.Fatal("init must not create a container for a workspace with nothing to materialise")
	}
}

// TestNewAcceptsFlagsBeforeOrAfterTheTaskName is Important fix 3: the task
// name may be typed before its flags or after them. Each subtest checks the
// flags actually took effect (a materialised tree under the given
// --workspace, a repo selected by --all), not merely that the exit code was
// 0 — a version that silently ignored --workspace and operated on "." could
// still exit 0 while doing the wrong thing entirely.
func TestNewAcceptsFlagsBeforeOrAfterTheTaskName(t *testing.T) {
	positionalFirst := func(t *testing.T, ws, task string) {
		var out, errb bytes.Buffer
		if code := Run([]string{"new", task, "--workspace", ws, "--all"}, &out, &errb); code != 0 {
			t.Fatalf("positional-first order exited %d: %s", code, errb.String())
		}
	}
	flagsFirst := func(t *testing.T, ws, task string) {
		var out, errb bytes.Buffer
		if code := Run([]string{"new", "--all", "--workspace", ws, task}, &out, &errb); code != 0 {
			t.Fatalf("flags-first order exited %d: %s", code, errb.String())
		}
	}

	for name, create := range map[string]func(t *testing.T, ws, task string){
		"positional-first": positionalFirst,
		"flags-first":      flagsFirst,
	} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			ws := filepath.Join(base, "ws")
			seedRepo(t, filepath.Join(ws, "svc-a"))
			var out, errb bytes.Buffer
			if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
				t.Fatalf("init exited %d: %s", code, errb.String())
			}

			create(t, ws, "t-order")

			out.Reset()
			errb.Reset()
			if code := Run([]string{"path", "t-order", "--workspace", ws}, &out, &errb); code != 0 {
				t.Fatalf("path exited %d: %s", code, errb.String())
			}
			// Proves both flags actually took effect, not merely that the
			// exit code was 0: if --workspace were silently ignored (falling
			// back to "."), svc-a — created only under this test's isolated
			// tempdir — would not exist under whatever tree got built, and
			// if --all were ignored, no repository would be selected at all.
			treePath := strings.TrimSpace(out.String())
			if _, err := os.Stat(filepath.Join(treePath, "svc-a")); err != nil {
				t.Fatalf("--workspace and/or --all was not honoured (svc-a missing from %q): %v", treePath, err)
			}
		})
	}
}

// TestUsageStringDoesNotAdvertiseJSON is minor fix 5: --json was advertised
// but never implemented as a flag on any command, so passing it fails with
// "flag provided but not defined". The usage text must not promise it.
func TestUsageStringDoesNotAdvertiseJSON(t *testing.T) {
	if strings.Contains(usage, "json") {
		t.Fatalf("usage text still advertises --json: %q", usage)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"status", "--json"}, &out, &errb); code != 2 {
		t.Fatalf("--json exited %d, want 2 (an unrecognised flag is a usage error)", code)
	}
}

// --- review round 3 ---

// newSplitFlagsWorkspace seeds a two-repository workspace and runs `wkt
// init` against it, returning the workspace path. Two repositories are
// essential here, not one: with only one repository, --repos and --all
// select the same tree, and a version that silently ignored --repos would
// pass undetected.
func newSplitFlagsWorkspace(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "svc-a"))
	seedRepo(t, filepath.Join(ws, "svc-b"))
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	return ws
}

// assertOnlySvcA runs `wkt path` for the task and asserts the materialised
// tree contains svc-a and does NOT contain svc-b — proving --repos svc-a
// was actually honoured, not merely that the command exited 0. Exit 0 alone
// is not enough: the round 3 regression exited 0 while silently
// materialising both repositories instead of the one requested.
func assertOnlySvcA(t *testing.T, ws, task string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run([]string{"path", task, "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("path exited %d: %s", code, errb.String())
	}
	treePath := strings.TrimSpace(out.String())

	// An unselected repository is still present in the tree — as a
	// back-filled symlink to the workspace, per tree.PlanFor's design, so
	// cross-repo references still resolve — so "svc-b must not exist" is
	// the wrong assertion (and was, in an earlier draft of this test,
	// incorrectly red against CORRECT code). The real distinction --repos
	// makes is real materialised worktree (a directory, on the task
	// branch) vs. back-fill symlink (unmodified, still on main).
	aInfo, err := os.Lstat(filepath.Join(treePath, "svc-a"))
	if err != nil {
		t.Fatalf("--repos svc-a must materialise svc-a: %v", err)
	}
	if aInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatal("svc-a must be a real materialised worktree, not a back-fill symlink")
	}
	if branch, err := exec.Command("git", "-C", filepath.Join(treePath, "svc-a"), "rev-parse", "--abbrev-ref", "HEAD").Output(); err != nil || strings.TrimSpace(string(branch)) != task {
		t.Fatalf("svc-a must be checked out on the task branch %q, got %q (err=%v)", task, strings.TrimSpace(string(branch)), err)
	}

	bInfo, err := os.Lstat(filepath.Join(treePath, "svc-b"))
	if err != nil {
		t.Fatalf("svc-b should still be present as a back-fill symlink: %v", err)
	}
	if bInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("--repos svc-a must NOT materialise svc-b as a real worktree; it must remain a back-fill symlink")
	}
}

// TestNewHonoursReposFlagRegardlessOfPositionalPlacement is the round 3 fix:
// --repos must take effect no matter which side of the task name it's typed
// on, including split across both sides of it — the shape that regressed
// ("new --workspace WS task --repos svc-a" silently selected every
// repository instead of refusing or honouring --repos).
func TestNewHonoursReposFlagRegardlessOfPositionalPlacement(t *testing.T) {
	t.Run("positional-first", func(t *testing.T) {
		ws := newSplitFlagsWorkspace(t)
		var out, errb bytes.Buffer
		if code := Run([]string{"new", "task", "--workspace", ws, "--repos", "svc-a"}, &out, &errb); code != 0 {
			t.Fatalf("new exited %d: %s", code, errb.String())
		}
		assertOnlySvcA(t, ws, "task")
	})

	t.Run("flags-first", func(t *testing.T) {
		ws := newSplitFlagsWorkspace(t)
		var out, errb bytes.Buffer
		if code := Run([]string{"new", "--workspace", ws, "--repos", "svc-a", "task"}, &out, &errb); code != 0 {
			t.Fatalf("new exited %d: %s", code, errb.String())
		}
		assertOnlySvcA(t, ws, "task")
	})

	// This is the shape that regressed in round 2: flags on both sides of
	// the positional. Before this fix it exited 0 and silently materialised
	// every repository in the workspace (svc-a AND svc-b) instead of
	// honouring --repos svc-a — the worst-direction failure (a safe refusal
	// traded for a silent wrong result). Verified live against the round 2
	// binary before writing this fix; see the task report for the
	// transcript.
	t.Run("split-across-the-positional", func(t *testing.T) {
		ws := newSplitFlagsWorkspace(t)
		var out, errb bytes.Buffer
		if code := Run([]string{"new", "--workspace", ws, "task", "--repos", "svc-a"}, &out, &errb); code != 0 {
			t.Fatalf("new exited %d: %s", code, errb.String())
		}
		assertOnlySvcA(t, ws, "task")
	})
}

// TestNewRefusesTwoPositionalsExitsTwo is the belt-and-braces half of the
// round 3 fix: a shape splitPositional cannot safely resolve to one
// positional (here, two bare tokens) must be refused, not silently resolved
// by picking the first and discarding the second.
func TestNewRefusesTwoPositionalsExitsTwo(t *testing.T) {
	ws := newSplitFlagsWorkspace(t)
	var out, errb bytes.Buffer
	code := Run([]string{"new", "task1", "task2", "--workspace", ws, "--all"}, &out, &errb)
	if code != 2 {
		t.Fatalf("two positionals exited %d, want 2: stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	// And nothing must have been created under either name.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"path", "task1", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("a refused two-positional new must not have created task1")
	}
}

// TestSplitPositionalDerivesValueFlagsFromTheFlagSet is review finding
// Minor 9: which flags consume a separately-typed value used to be a
// hand-maintained map, independent of the FlagSet actually being parsed. A
// future flag added to the FlagSet but forgotten in that map silently
// reintroduces the exact round-2 bug (a value flag's argument mistaken for
// the positional, or vice versa) — which already happened once on this
// branch. Registers a brand-new string flag, "--extra", that has never
// appeared in any hardcoded list anywhere, and checks that its
// separately-typed value is still skipped correctly purely because
// splitPositional now derives the classification from fs.VisitAll.
func TestSplitPositionalDerivesValueFlagsFromTheFlagSet(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("workspace", ".", "")
	fs.Bool("all", false, "")
	fs.String("extra", "", "a brand-new value flag, not in any hand-maintained list")

	positional, remaining := splitPositional(fs, []string{"--extra", "not-the-positional", "task", "--all"})
	if positional != "task" {
		t.Fatalf("got positional %q, want %q — the new value flag's argument must be skipped, not mistaken for the positional", positional, "task")
	}
	wantRemaining := []string{"--extra", "not-the-positional", "--all"}
	if strings.Join(remaining, ",") != strings.Join(wantRemaining, ",") {
		t.Fatalf("got remaining %v, want %v", remaining, wantRemaining)
	}

	// A boolean flag's own argument, by contrast, must never be skipped: it
	// takes no separately-typed value, so the very next token is fair game
	// as the positional.
	positional2, _ := splitPositional(fs, []string{"--all", "task2"})
	if positional2 != "task2" {
		t.Fatalf("got positional %q, want %q — a boolean flag must not swallow the following token", positional2, "task2")
	}
}

// TestNewWithASeparatorInTheTaskNameCreatesNothing covers adversarial
// finding F1 end to end: the refusal must happen before anything is built,
// so no debris is left in trees/ to block a later, legitimate task name.
func TestNewWithASeparatorInTheTaskNameCreatesNothing(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"new", "feature/x", "--all", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatalf("new must refuse a task name carrying a path separator; stderr=%s", errb.String())
	}
	if !strings.Contains(errb.String(), "WKT_BAD_TASK_NAME") {
		t.Fatalf("want WKT_BAD_TASK_NAME, got %s", errb.String())
	}
	trees := filepath.Join(ws+".worktrees", "trees")
	ents, err := os.ReadDir(trees)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("the refusal must leave trees/ empty, found %d entries", len(ents))
	}
	// The name whose slot the debris used to occupy must still be usable.
	out.Reset()
	if code := Run([]string{"new", "feature", "--all", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("the plain name must remain available, exited %d: %s", code, errb.String())
	}
}

// TestTreeExistsRemedyIsActionable covers the second half of F1: the old
// remedy suggested "wkt rm <task>", which answers WKT_NO_TASK when only the
// directory exists — a dead end that left the user with no documented way out.
func TestTreeExistsRemedyIsActionable(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	orphan := filepath.Join(ws+".worktrees", "trees", "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := Run([]string{"new", "orphan", "--all", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("new must refuse when the tree directory is already there")
	}
	msg := errb.String()
	if !strings.Contains(msg, "WKT_TREE_EXISTS") {
		t.Fatalf("want WKT_TREE_EXISTS, got %s", msg)
	}
	if strings.Contains(msg, "wkt rm orphan") {
		t.Fatalf("the remedy must not recommend a command that answers WKT_NO_TASK: %s", msg)
	}
	if !strings.Contains(msg, orphan) {
		t.Fatalf("the remedy must name the directory to deal with: %s", msg)
	}
}

// TestNewWarnsOnStderrWhenASelectedRepositoryHasASubmodule covers F3 at the
// CLI seam: the warning goes to stderr and the command still succeeds.
func TestNewWarnsOnStderrWhenASelectedRepositoryHasASubmodule(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(base, "lib"))
	seedRepo(t, filepath.Join(ws, "super"))
	sub := filepath.Join(ws, "super")
	for _, args := range [][]string{
		{"-c", "protocol.file.allow=always", "submodule", "add", "-q", filepath.Join(base, "lib"), "vendor"},
		{"commit", "-qm", "add submodule"},
	} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=e@x", "-c", "user.name=t"}, args...)...)
		cmd.Dir = sub
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, o)
		}
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("init exited %d: %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"new", "t1", "--all", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("new must still succeed, exited %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "WKT_SUBMODULE") || !strings.Contains(errb.String(), "super") {
		t.Fatalf("new must warn on stderr that super carries a submodule, got %q", errb.String())
	}
}

// TestInitExcludeAdoptsAWorkspaceWithANestedRepository covers adversarial
// finding F4. Spec §5.3 rule 6 and the §7.1 command table both promise
// --exclude as the escape hatch for a genuine nested repository; without it,
// init refuses and a workspace containing one cannot be adopted at all.
func TestInitExcludeAdoptsAWorkspaceWithANestedRepository(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	seedRepo(t, filepath.Join(ws, "a", "inner"))

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("a genuine nested repository must be refused without --exclude")
	}
	if !strings.Contains(errb.String(), "WKT_NESTED_REPO") {
		t.Fatalf("want WKT_NESTED_REPO, got %s", errb.String())
	}
	// The refusal has to name the way out, or the user is stuck.
	if !strings.Contains(errb.String(), "--exclude") {
		t.Fatalf("the refusal must point at --exclude: %s", errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"init", "--exclude", "a/inner", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("--exclude must adopt the workspace, exited %d: %s", code, errb.String())
	}

	// Recorded in container state: a later run must not need the flag again.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"init", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("the exclusion must be remembered, exited %d: %s", code, errb.String())
	}

	// And the workspace is usable: a task over the outer repository works.
	out.Reset()
	if code := Run([]string{"new", "t1", "--all", "--workspace", ws}, &out, &errb); code != 0 {
		t.Fatalf("new exited %d: %s", code, errb.String())
	}
}

// TestInitExcludeRefusesAPathThatIsNotANestedRepository keeps the flag from
// becoming a silent no-op for a typo, the same failure shape as defect 24.
func TestInitExcludeRefusesAPathThatIsNotANestedRepository(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	seedRepo(t, filepath.Join(ws, "a"))
	seedRepo(t, filepath.Join(ws, "a", "inner"))

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--exclude", "a/typo", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("excluding something that is not a nested repository must fail")
	}
	if !strings.Contains(errb.String(), "WKT_NO_SUCH_NESTED_REPO") {
		t.Fatalf("want WKT_NO_SUCH_NESTED_REPO, got %s", errb.String())
	}
	// Excluding the *outer* repository is not what the flag is for either:
	// it is not nested, and dropping it would leave its directory to be
	// linked whole, hiding a repository inside a shared writable link.
	errb.Reset()
	if code := Run([]string{"init", "--exclude", "a", "--workspace", ws}, &out, &errb); code == 0 {
		t.Fatal("excluding a non-nested repository must fail")
	}
}

// TestUsageDocumentsExclude keeps the escape hatch discoverable: a flag the
// refusal recommends but the usage never mentions is a flag nobody finds.
func TestUsageDocumentsExclude(t *testing.T) {
	if !strings.Contains(usage, "--exclude") {
		t.Fatalf("usage must document --exclude:\n%s", usage)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -v`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Write the CLI**

```go
// internal/cli/cli.go
// Package cli parses wkt's command line and calls into the packages that do
// the actual work. It is intentionally thin: its own value is the exit-code
// contract it exposes (0 consistent, 2 usage error or task already exists, 3
// drift detected, 4 container missing, 1 any other typed failure), because
// the acceptance battery drives the binary through exactly these verbs and
// codes.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"wkt/internal/container"
	"wkt/internal/discover"
	"wkt/internal/gitx"
	"wkt/internal/state"
	"wkt/internal/task"
	"wkt/internal/wkterr"
)

const usage = `wkt — one task, one branch, many repositories

  wkt init   [--workspace DIR] [--dry-run] [--exclude a/inner,...]
  wkt new    TASK [--workspace DIR] [--repos a,b | --all]   (alias: create)
  wkt path   TASK [--workspace DIR]
  wkt status [TASK] [--workspace DIR]
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
	exclude := fs.String("exclude", "", "comma-separated nested repositories to exclude from adoption")

	// The task name may fall anywhere among the flags: before all of them,
	// after all of them, or split between two of them ("new --workspace WS
	// task --repos svc-a"). Go's flag.Parse only ever consumes a run of
	// flags up to the first non-flag token and then stops for good, so a
	// single Parse call cannot find a positional on the far side of a later
	// flag — it would leave that later flag sitting unparsed in Args(),
	// silently keeping its default instead of erroring. Concretely: "new
	// --workspace WS task1 --repos svc-a" left --repos unparsed, so
	// selection() fell back to its --all default and materialised every
	// repository the user never asked for, while still reporting success.
	// splitPositional extracts the one positional itself, by walking the
	// flags, before fs.Parse ever runs, so every flag on both sides reaches
	// it in one pass.
	positional, rest := splitPositional(fs, rest)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	// Belt and braces: splitPositional found at most one bare token and
	// removed it, so fs.Parse should never have anything left in Args().
	// If it does — two positionals, or a shape splitPositional could not
	// safely place — refuse rather than silently pick one and discard the
	// rest.
	if fs.NArg() > 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	c, err := container.Locate(*ws)
	if err != nil {
		return fail(stderr, err)
	}

	switch cmd {
	case "init":
		if info, statErr := os.Stat(c.Workspace); statErr != nil || !info.IsDir() {
			e := wkterr.New("WKT_NO_WORKSPACE", "the workspace does not exist or is not a directory").
				WithPath(c.Workspace)
			if statErr != nil {
				e = e.WithFound(statErr.Error())
			}
			return fail(stderr, e)
		}
		entries, err := discover.Walk(c.Workspace, 4)
		if err != nil {
			return fail(stderr, err)
		}
		// Exclusions are cumulative: what this run passes, plus whatever an
		// earlier run recorded (spec §5.3 rule 6, "recorded in container
		// state"). Without the recorded half, a workspace adopted once with
		// --exclude would refuse on every later init.
		prior, err := state.LoadContainer(c.ConfigDir())
		if err != nil {
			return fail(stderr, err)
		}
		excluded := map[string]bool{}
		for _, p := range prior.Excluded {
			excluded[p] = true
		}
		pairs := discover.NestedPairs(entries)
		for _, p := range splitList(*exclude) {
			nested := false
			for _, pair := range pairs {
				if pair[0] == p {
					nested = true
					break
				}
			}
			if !nested {
				// A typo, or an attempt to drop an ordinary repository —
				// which is not what this flag is for: an unexcluded
				// top-level repository still has to be discovered, or its
				// directory would be linked whole and share a repository
				// writably with every task (spec §5.3 rule 4).
				return fail(stderr, wkterr.New("WKT_NO_SUCH_NESTED_REPO", "not a nested repository in this workspace").
					WithRepo(p).
					WithRemedy("run wkt init to see which repositories are nested"))
			}
			excluded[p] = true
		}
		var stillNested [][2]string
		for _, p := range pairs {
			if !excluded[p[0]] {
				stillNested = append(stillNested, p)
			}
		}
		if len(stillNested) > 0 {
			e := wkterr.New("WKT_NESTED_REPO", "nested repositories are not supported")
			for _, p := range stillNested {
				e = e.WithProblem(wkterr.Problem{Code: "WKT_NESTED_REPO", Repo: p[0], Detail: "inside " + p[1]})
			}
			return fail(stderr, e.WithRemedy(
				"move the inner repository out of the outer one",
				"or adopt the workspace without it: wkt init --exclude "+stillNested[0][0]))
		}
		repoCount := 0
		for _, e := range entries {
			if e.Kind == discover.KindRepo {
				fmt.Fprintln(stdout, e.RelPath)
				repoCount++
			}
		}
		if repoCount == 0 {
			return fail(stderr, wkterr.New("WKT_NO_REPOS", "no repositories were found under the workspace").
				WithPath(c.Workspace).
				WithRemedy("check that --workspace points at the intended directory"))
		}
		if *dryRun {
			return 0
		}
		if err := container.Create(c); err != nil {
			return fail(stderr, err)
		}
		if err := recordExclusions(c, excluded); err != nil {
			return fail(stderr, err)
		}
		return 0

	case "new":
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		// A container that init already built is required from here on;
		// without this check, an uninitialised workspace fails on the lock
		// file it cannot open (WKT_CONTAINER_UNUSABLE, exit 1) instead of
		// the documented exit 4.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
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
		// Spec §5.7: warn before the work starts, because rm refuses on a
		// submodule even with --force, so a task created over one cannot be
		// removed by any wkt command until the submodule is deinitialised.
		for _, w := range task.SubmoduleWarnings(entries, selected) {
			fmt.Fprintf(stderr, "warning: %s %s carries the submodule %q; wkt rm will refuse to remove this task, --force included\n",
				w.Code, w.Repo, w.Detail)
		}
		t, err := task.Create(c, entries, positional, selected)
		if err != nil {
			return fail(stderr, err) // fail() maps WKT_TASK_EXISTS to 2
		}
		fmt.Fprintln(stdout, c.TreePath(t.Name))
		return 0

	case "path":
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		// Without this, a task looked up in a container that was never
		// created silently reads as "no such task" (WKT_NO_TASK, exit 1)
		// rather than the documented exit 4 for a missing container.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
		t, err := state.Load(c.StateDir(), positional)
		if err != nil {
			return fail(stderr, err)
		}
		// State says the task exists; the disk says whether its tree does.
		// Printing a path state remembers but disk no longer has is worse
		// than refusing, so verify before ever writing to stdout.
		treePath := c.TreePath(t.Name)
		if _, statErr := os.Stat(treePath); statErr != nil {
			if os.IsNotExist(statErr) {
				// "wkt rm <task> --force" used to be the advice here, but a
				// missing tree can never reach the staging fence --force
				// gates: Remove now goes straight to store/state cleanup
				// for a missing tree, so plain "wkt rm <task>" is both
				// sufficient and the honest remedy — there is nothing left
				// to force through.
				e := wkterr.New("WKT_TREE_MISSING", "task state exists but its tree is missing from disk").
					WithPath(treePath).
					WithRemedy("wkt status "+positional, "wkt rm "+positional)
				return fail(stderr, e)
			}
			return fail(stderr, wkterr.New("WKT_CHECK_FAILED", "cannot verify the tree").
				WithPath(treePath).WithFound(statErr.Error()))
		}
		fmt.Fprintln(stdout, treePath)
		return 0

	case "status":
		// status takes an optional task name, so there is no usage error to
		// check first; the container check is the first thing done. Without
		// it, an uninitialised workspace's absent state directory reads as
		// "zero tasks" and status silently exits 0 instead of the
		// documented exit 4.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
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
				// Only blocking-severity entries are drift. Info-severity
				// entries (regenerable ignored content — node_modules,
				// dist, and friends) are printed too, marked informational,
				// so seeing "these twelve things are build output" is what
				// stops --force becoming reflexive, but an ordinary tree
				// that merely has a node_modules in it must not exit 3.
				marker := "!"
				if b.Severity == "info" {
					marker = "i"
				} else {
					drift = true
				}
				fmt.Fprintf(stdout, "  %s %-20s %s %s %s\n", marker, b.Code, b.Repo, b.Path, b.Detail)
			}
		}
		if drift {
			return 3
		}
		return 0

	case "rm":
		if positional == "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		// Without this, an uninitialised workspace fails on the lock file
		// it cannot open (WKT_CONTAINER_UNUSABLE, exit 1) instead of the
		// documented exit 4.
		if err := requireContainer(c); err != nil {
			return fail(stderr, err)
		}
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

// requireContainer reports whether init has already built the container
// this workspace resolves to. Locate only computes where the container
// would live; it never checks whether Create has actually run there, so
// every command that depends on the container's directories existing must
// check for itself or fail with whatever unrelated error it happens to hit
// first (an unopenable lock file, a state directory that silently yields no
// tasks).
func requireContainer(c container.C) error {
	if info, err := os.Stat(c.Root); err != nil || !info.IsDir() {
		return wkterr.New("WKT_NO_CONTAINER", "no container for this workspace").
			WithPath(c.Root).
			WithRemedy("wkt init --workspace " + c.Workspace)
	}
	return nil
}

// splitPositional finds wkt's one positional argument (the task name)
// wherever it falls among rest's flags — before them, after them, or split
// between two of them — and returns it separately from every flag token, in
// their original relative order, so a single fs.Parse afterwards sees every
// flag regardless of which side of the positional it was typed on.
//
// It must never guess wrong in the unsafe direction. If a value flag's
// separately-typed value cannot be told apart from a positional by looking
// at it alone (nothing here inspects a value's shape — that is deliberate:
// a workspace path or a repo list can itself start with anything), the
// value is skipped over structurally, by the flag preceding it, and is
// therefore never considered as a positional candidate. If no bare token is
// ever found, it returns rest untouched rather than fabricating one, so an
// unparsable shape falls through to fs.Parse's own error handling, or to the
// caller's fs.NArg()>0 check, instead of a wrong guess being silently acted
// on. See the round 3 regression this replaces: a single fs.Parse call left
// a flag typed after the task name sitting unparsed in Args(), silently
// keeping its zero value instead of erroring.
//
// Which flags consume a separately-typed value is derived from fs itself via
// VisitAll, not hand-maintained: a flag is boolean iff its Value implements
// the standard library's unexported-but-checkable "IsBoolFlag() bool"
// convention (flag.Bool's Value does; flag.String's does not). A future flag
// added to fs and forgotten here — the exact shape of a bug already fixed
// once on this branch — now classifies itself correctly with no separate
// list to keep in sync.
func splitPositional(fs *flag.FlagSet, rest []string) (positional string, remaining []string) {
	valueFlags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			return
		}
		valueFlags["-"+f.Name] = true
		valueFlags["--"+f.Name] = true
	})
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		if !strings.HasPrefix(tok, "-") {
			out := make([]string, 0, len(rest)-1)
			out = append(out, rest[:i]...)
			out = append(out, rest[i+1:]...)
			return tok, out
		}
		if strings.Contains(tok, "=") {
			continue // "--flag=value" is one token; nothing extra to skip
		}
		if valueFlags[tok] {
			i++ // skip the value flag's separately-typed value
		}
	}
	return "", rest
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
		case "WKT_TREE_MISSING":
			return 3
		}
	}
	return 1
}

// splitList parses a comma-separated flag value, ignoring empty fields so a
// trailing comma is not read as a repository named "".
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// recordExclusions persists init's --exclude decisions in a stable order, so
// a later init honours them without the flag being repeated.
func recordExclusions(c container.C, excluded map[string]bool) error {
	if len(excluded) == 0 {
		return nil
	}
	list := make([]string, 0, len(excluded))
	for p := range excluded {
		list = append(list, p)
	}
	sort.Strings(list)
	return state.SaveContainer(c.ConfigDir(), state.Container{Excluded: list})
}
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
- Create: `test/04_precious_ignored_only.sh`
- Create: `test/05_staging_fence.sh`
- Create: `test/06_commit_under_readonly_workspace.sh`
- Create: `test/07_store_origin.sh`
- Create: `test/08_backfill.sh`
- Create: `test/09_exit_codes.sh`
- Create: `test/10_exclude_nested.sh`
- Create: `test/20m_two_tasks_one_repo.sh`
- Create: `test/run.sh`

**Interfaces:**
- Consumes: the built binary via `WT_CMD`.
- Produces: a runner that exits non-zero if any script fails. Scripts are bash 3.2 compatible, because macOS ships `/bin/bash` 3.2.

**Traps:**

- Do not drive every scenario with `--all`. The back-fill — the product's main
  differentiator — then has zero coverage.
- The staging fence needs its own observer: without test 05, deleting the
  rename-before-delete step leaves the whole battery green.

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
assert_eq "copied loose file matches the workspace original byte for byte" \
  "$(cat "$TD/CONVENTIONS.md")" "$(cat "$WS/CONVENTIONS.md")"
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
# task-a is driven through the "create"/"cleanup" aliases end to end, task-b
# through "new"/(never removed here) — between this script and the rest of
# the battery both spellings of both verbs are exercised. Both alias exit
# codes are checked directly, not just inferred from what happens later —
# a broken "create" alias would otherwise only surface several lines down,
# as an unrelated-looking cascading failure once $TA resolves to nothing.
wt create task-a --all >/dev/null
assert_eq "create (alias for new) exits 0" "$?" "0"
wt new task-b --all >/dev/null
TA="$(wt_task_dir task-a)"; TB="$(wt_task_dir task-b)"

# Review finding Important 6: both tasks' worktrees sit at a path whose
# basename is the repository's own leaf name ("svc-a"), but they share one
# store, so git disambiguates the second registration with a numeric suffix
# ("svc-a1"). Both task files must record their own actual registration
# name — repair cannot work without it (spec §5.4) — not the tree-path
# basename, which is "svc-a" for both regardless of which task it is.
CONTAINER="$(dirname "$(dirname "$TA")")"
NAME_A="$(grep '"store_worktree_name"' "$CONTAINER/state/tasks/task-a.json" | sed -E 's/.*: *"([^"]*)".*/\1/')"
NAME_B="$(grep '"store_worktree_name"' "$CONTAINER/state/tasks/task-b.json" | sed -E 's/.*: *"([^"]*)".*/\1/')"
if [ -z "$NAME_A" ] || [ "$NAME_A" = "$NAME_B" ]; then
  fail "the two tasks must record different store worktree registration names, got '$NAME_A' and '$NAME_B'"
else
  pass "the two tasks record different store worktree registration names ($NAME_A vs $NAME_B)"
fi

echo "A" >> "$TA/svc-a/src/index.js"
( cd "$TA/svc-a" && G add -A && G commit -qm "A change" >/dev/null )
assert_eq "task B does not see task A's commit" \
  "$(cd "$TB/svc-a" && git log --oneline | wc -l | tr -d ' ')" "1"
assert_eq "task B stays clean" "$(cd "$TB/svc-a" && git status --porcelain)" ""
assert_eq "the workspace stays on its own branch" \
  "$(cd "$WS/svc-a" && git rev-parse --abbrev-ref HEAD)" "main"

( cd "$TA/svc-a" && G push -q -u origin task-a >/dev/null 2>&1 )
wt cleanup task-a >/dev/null 2>&1
assert_eq "cleanup (alias for rm) exits 0" "$?" "0"
assert_no_file "task A removed" "$TA"
assert_file "task B still works" "$TB/svc-a"
assert_eq "task B still on its branch" "$(cd "$TB/svc-a" && git rev-parse --abbrev-ref HEAD)" "task-b"
summary 20m
```

- [ ] **Step 5: Write tests 04, 05, 08 and 09 — the guards the battery was missing**

Test 04 pins the precious-ignored refusal on its own, with no unpushed commit
beside it. Test 05 observes the staging fence. Test 08 is the only scenario that
does **not** pass `--all`, so it is the only one that exercises the back-fill.
Test 09 drives the exit-code contract end to end. Test 10 drives the nested-repo
refusal and its escape hatch, including that the exclusion survives a later
`init` — a decision recorded in state is worthless if the next run ignores it.

```bash
# test/04_precious_ignored_only.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo app || exit 1
wt init >/dev/null; wt new task-4 --all >/dev/null
TD="$(wt_task_dir task-4)"

# The ONE blocker, and nothing else: a gitignored file, no commit, no
# untracked non-ignored file, no modification. This is the class of work
# git itself never guards (spec H1), and the only one test 03 leaves
# unpinned — a battery that always refuses would still pass a scenario
# with four simultaneous blockers. Isolate it.
echo "TOKEN=secret" > "$TD/app/.env"

wt rm task-4 >/dev/null 2>&1
assert_eq "rm refuses on a gitignored file alone" "$?" "1"
assert_file ".env survives the refusal" "$TD/app/.env"
assert_file "tree still present after the refusal" "$TD/app"

( cd "$TD/app" && G clean -fdX -q )
wt rm task-4 >/dev/null 2>&1
assert_eq "rm succeeds once the only blocker is gone — no --force needed" "$?" "0"
assert_no_file "tree removed" "$TD/app"
summary 04
```

```bash
# test/05_staging_fence.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env
CONTAINER=""
fence_cleanup() { [ -n "$CONTAINER" ] && [ -d "$CONTAINER" ] && chmod -R u+w "$CONTAINER" 2>/dev/null; wt_cleanup_env; }
trap fence_cleanup EXIT
mk_repo docs || exit 1
wt init >/dev/null; wt new task-5 --all >/dev/null
TD="$(wt_task_dir task-5)"
CONTAINER="$(dirname "$(dirname "$TD")")"
STAGED="$CONTAINER/staging/task-5"

echo "draft" > "$TD/docs/untracked.md"
# Lock a directory INSIDE the tree, not the container's staging/ itself.
# staging/ has to stay writable or the rename that is the fence can never
# even start — renaming into a directory needs write on the *destination*
# parent, confirmed empirically before writing this test: chmod 500 on
# staging/ makes the rename itself fail with EACCES, never reaching a
# delete to observe at all. Locking content already inside the tree instead
# leaves both rename endpoints writable, so the fence gets to fire, and only
# the recursive delete that follows it trips — on the locked subtree, not
# on the move.
chmod 500 "$TD/docs"

wt rm task-5 --force >/dev/null 2>&1
RC=$?
chmod -R u+w "$CONTAINER"
assert_eq "rm --force reports the incomplete cleanup rather than pretending it finished" "$RC" "1"
assert_no_file "the fence already moved the tree off its original path" "$TD"
assert_file "the content the delete couldn't reach survived under staging" "$STAGED/docs/untracked.md"
summary 05
```

```bash
# test/08_backfill.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1
mk_repo shared || exit 1
wt init >/dev/null
wt new task-8 --repos services/svc-a >/dev/null
TD="$(wt_task_dir task-8)"

assert_file "services/svc-a materialised" "$TD/services/svc-a"
if path_has_symlink "$TD" "services/svc-a"; then fail "services/svc-a: unexpectedly linked"; else pass "services/svc-a: real worktree, no symlinked ancestor"; fi
assert_eq "services/svc-a on the task branch" "$(cd "$TD/services/svc-a" && git rev-parse --abbrev-ref HEAD)" "task-8"
if [ -d "$TD/services" ] && [ ! -L "$TD/services" ]; then pass "services stays a real directory, not collapsed into one link"; else fail "services should be a real directory"; fi

assert_eq "shared (unselected) back-fills as a symlink" "$([ -L "$TD/shared" ] && echo yes || echo no)" "yes"
GOT="$(cat "$TD/services/svc-a/../../shared/src/index.js" 2>/dev/null)"
WANT="$(cat "$WS/shared/src/index.js")"
assert_eq "shared resolves through the back-fill link from a sibling repo's path and matches the workspace" "$GOT" "$WANT"
assert_eq "the workspace's shared stays on its own branch, untouched by the task" \
  "$(cd "$WS/shared" && git rev-parse --abbrev-ref HEAD)" "main"
summary 08
```

```bash
# test/09_exit_codes.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo svc-a || exit 1

# exit 4: no container yet — init has never run.
wt new task-x --all >/dev/null 2>&1
assert_eq "new: exit 4 before init" "$?" "4"
wt path task-x >/dev/null 2>&1
assert_eq "path: exit 4 before init" "$?" "4"
wt status >/dev/null 2>&1
assert_eq "status: exit 4 before init" "$?" "4"
wt rm task-x >/dev/null 2>&1
assert_eq "rm: exit 4 before init" "$?" "4"

wt init >/dev/null

# exit 2: usage errors.
wt new >/dev/null 2>&1
assert_eq "new: exit 2 on a missing task name" "$?" "2"

wt new task-9 --all >/dev/null
wt new task-9 --all >/dev/null 2>&1
assert_eq "new: exit 2 on a duplicate task" "$?" "2"

# exit 0: status on a clean task.
wt status task-9 >/dev/null 2>&1
assert_eq "status: exit 0 on a clean task" "$?" "0"

# exit 3: status on a tree that has drifted (an unpushed commit is a blocker).
TD="$(wt_task_dir task-9)"
echo "change" >> "$TD/svc-a/src/index.js"
( cd "$TD/svc-a" && G add -A && G commit -qm "drift" >/dev/null )
wt status task-9 >/dev/null 2>&1
assert_eq "status: exit 3 when the task has drifted" "$?" "3"
summary 09
```

```bash
# test/10_exclude_nested.sh
#!/usr/bin/env bash
# Spec §5.3 rule 6: a genuine nested repository is refused by name, with
# --exclude as the escape hatch, "recorded in container state" — so a later
# init must honour it without the flag being repeated.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1
mk_repo services/svc-a/vendored || exit 1

wt init >"$TMP/out" 2>"$TMP/err"
assert_eq "init refuses a nested repository" "$?" "1"
if grep -q "WKT_NESTED_REPO" "$TMP/err"; then pass "the refusal names the code"
else fail "the refusal names the code — got $(cat "$TMP/err")"; fi
if grep -q -- "--exclude" "$TMP/err"; then pass "the refusal points at the escape hatch"
else fail "the refusal points at the escape hatch"; fi

wt init --exclude services/svc-a/vendored >/dev/null 2>&1
assert_eq "init --exclude adopts the workspace" "$?" "0"
assert_file "the exclusion is recorded in container state" "$WS.worktrees/state/container.json"

wt init >/dev/null 2>&1
assert_eq "a later init honours the recorded exclusion" "$?" "0"

wt new t1 --all >/dev/null 2>&1
assert_eq "a task over the outer repository works" "$?" "0"

wt init --exclude services/typo >"$TMP/out2" 2>"$TMP/err2"
assert_eq "excluding a path that is not nested fails" "$?" "1"
if grep -q "WKT_NO_SUCH_NESTED_REPO" "$TMP/err2"; then pass "the typo is named"
else fail "the typo is named — got $(cat "$TMP/err2")"; fi

summary 10
```

- [ ] **Step 6: Write the runner and run the whole battery**

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

- [ ] **Step 7: Commit**

```bash
git add test
git commit -m "test: mechanical acceptance battery"
```

---

## What this plan deliberately leaves out

`add`, `fetch`, `sync`, `repair`, `doctor`, the perimeter generator and the
`WorktreeCreate` hook are the second plan. Salvage refs, quarantine, `push`/`pr`,
`adopt` and `wkt run` are out of v0 entirely (spec §6, §9).
