# test/12_doctor.sh
#!/usr/bin/env bash
# doctor reconciles state against disk, and is the uninstall path: it must be
# able to say exactly what wkt has written into the user's own repositories.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1

wt init >/dev/null 2>&1
wt new task-12 --all >/dev/null 2>&1

wt doctor >"$TMP/clean" 2>&1
assert_eq "a healthy container exits 0" "$?" "0"
assert_eq "and says nothing" "$(cat "$TMP/clean")" ""

wt doctor --all >"$TMP/all" 2>&1
if grep -q "refs/wkt/base/task-12" "$TMP/all"; then pass "--all names the ref wkt wrote into the repository"
else fail "--all names the ref — got $(cat "$TMP/all")"; fi

# Debris an interrupted create would leave: a directory no task claims.
mkdir -p "$WS.worktrees/trees/leftover"
wt doctor >/dev/null 2>&1
assert_eq "debris in trees/ is a problem (exit 3)" "$?" "3"

# Something that might hold work is never removed for you.
mkdir -p "$WS.worktrees/trees/has-content"
echo "the only copy" > "$WS.worktrees/trees/has-content/notes.md"
wt doctor --fix >/dev/null 2>&1
assert_no_file "--fix clears the empty leftover" "$WS.worktrees/trees/leftover"
assert_file    "--fix leaves anything with content in it" "$WS.worktrees/trees/has-content/notes.md"

# A pin whose task is gone.
( cd "$WS/services/svc-a" && G update-ref refs/wkt/base/ghost "$(git rev-parse HEAD)" )
wt doctor >/dev/null 2>&1
assert_eq "a stray pin is a problem" "$?" "3"
wt doctor --fix >/dev/null 2>&1
assert_eq "and --fix deletes it" \
  "$(cd "$WS/services/svc-a" && git for-each-ref --format='%(refname)' refs/wkt/base/ghost)" ""

summary 12
