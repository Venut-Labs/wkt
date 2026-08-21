# test/14_add_sync_fetch.sh
#!/usr/bin/env bash
# add grafts at the task's epoch; sync reports drift without moving anything;
# fetch brings work back fast-forward only, and never forces a branch you own.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1
mk_repo shared || exit 1

wt init >/dev/null 2>&1
wt new task-14 --repos services/svc-a >/dev/null 2>&1
TD="$(wt_task_dir task-14)"

assert_file "the unselected repository starts as a back-fill link" "$TD/shared"
if [ -L "$TD/shared" ]; then pass "and it really is a link"; else fail "and it really is a link"; fi

wt add task-14 --repos shared >/dev/null 2>&1
assert_eq "add exits 0" "$?" "0"
if [ -L "$TD/shared" ]; then fail "add replaces the link with a worktree"; else pass "add replaces the link with a worktree"; fi
assert_eq "and puts it on the task branch" \
  "$(cd "$TD/shared" && git rev-parse --abbrev-ref HEAD)" "task-14"

# The workspace moves on; sync says so and changes nothing.
BASE_BEFORE="$(cd "$TD/services/svc-a" && git rev-parse HEAD)"
( cd "$WS/services/svc-a" && G commit -qm "moved on" --allow-empty >/dev/null )
wt sync task-14 >"$TMP/sync" 2>&1
assert_eq "sync exits 3 when the base has drifted" "$?" "3"
if grep -q "behind" "$TMP/sync"; then pass "and says how far behind"; else fail "and says how far behind — got $(cat "$TMP/sync")"; fi
assert_eq "sync does not move the task" \
  "$(cd "$TD/services/svc-a" && git rev-parse HEAD)" "$BASE_BEFORE"

# Work done in the task comes back.
( cd "$TD/services/svc-a" && G commit -qm "task work" --allow-empty >/dev/null )
WANT="$(cd "$TD/services/svc-a" && git rev-parse HEAD)"
wt fetch task-14 >/dev/null 2>&1
assert_eq "fetch exits 0" "$?" "0"
assert_eq "and the branch arrives in the workspace repository" \
  "$(cd "$WS/services/svc-a" && git rev-parse refs/heads/task-14)" "$WANT"

# A branch of the same name that went elsewhere is never forced.
( cd "$WS/shared" && G branch task-14-theirs && G checkout -q task-14-theirs \
    && G commit -qm "unrelated" --allow-empty >/dev/null && G checkout -q - )
( cd "$TD/shared" && G commit -qm "task work in shared" --allow-empty >/dev/null )
THEIRS="$(cd "$WS/shared" && git rev-parse refs/heads/task-14-theirs)"
wt fetch task-14 --as task-14-theirs >"$TMP/ff" 2>&1
assert_eq "a non-fast-forward is refused" "$?" "1"
if grep -q "WKT_NOT_FAST_FORWARD" "$TMP/ff"; then pass "with the code that says why"; else fail "with the code that says why"; fi
assert_eq "and the branch they own has not moved" \
  "$(cd "$WS/shared" && git rev-parse refs/heads/task-14-theirs)" "$THEIRS"

summary 14
