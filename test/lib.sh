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
