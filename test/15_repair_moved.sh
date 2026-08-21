# test/15_repair_moved.sh
#!/usr/bin/env bash
# Moving a workspace breaks every absolute path wkt recorded. repair is the
# command that adopts the new location — spec §6.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1
mk_repo shared || exit 1

wt init >/dev/null 2>&1
wt new task-15 --repos services/svc-a >/dev/null 2>&1

# Move the workspace and its container together, as a person would.
NEW="$TESTDIR/relocated"
mkdir -p "$NEW"
mv "$WS" "$NEW/workspace"
mv "$WS.worktrees" "$NEW/workspace.worktrees"
WS="$NEW/workspace"

"$WT_CMD" status task-15 --workspace "$WS" >/dev/null 2>&1
assert_eq "before repair the task is broken" "$?" "3"

"$WT_CMD" repair task-15 --workspace "$WS" >"$TMP/repair" 2>&1
assert_eq "repair exits 0" "$?" "0"
if grep -q "previous location" "$TMP/repair"; then pass "and says it adopted the new location"
else fail "and says it adopted the new location — got $(cat "$TMP/repair")"; fi

"$WT_CMD" status task-15 --workspace "$WS" >/dev/null 2>&1
assert_eq "after repair the task is healthy" "$?" "0"

TD="$WS.worktrees/trees/task-15"
assert_eq "the worktree is attached to its store again" \
  "$(cd "$TD/services/svc-a" && git rev-parse --abbrev-ref HEAD)" "task-15"
# Compare by suffix, not by prefix: readlink answers with the canonical
# spelling (/private/var on macOS) while $WS is as typed (/var), and a test
# that compares those two directly fails on a link that is perfectly correct.
LINK="$(readlink "$TD/shared")"
case "$LINK" in
  */relocated/workspace/shared) pass "the back-fill link points into the new workspace" ;;
  *)                            fail "the back-fill link still points at $LINK" ;;
esac

"$WT_CMD" rm task-15 --workspace "$WS" >/dev/null 2>&1
assert_eq "and the task can still be removed cleanly" "$?" "0"

summary 15
