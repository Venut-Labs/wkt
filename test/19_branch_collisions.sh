# test/19_branch_collisions.sh
#!/usr/bin/env bash
# Branch names live in a filesystem-shaped namespace, and git only says so at
# the moment it creates something. These are the collisions wkt has to answer
# for before it starts building, plus the fast-forward it must not refuse.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo docs || exit 1
mk_repo services/svc-a || exit 1
wt init >/dev/null || { fail "init"; summary 19; exit 1; }

# --- fetch is not a one-shot: work, fetch, work again, fetch again ----------
wt new task-19 --repos docs >/dev/null || { fail "new task-19"; summary 19; exit 1; }
TD="$(wt_task_dir task-19)"
echo one > "$TD/docs/a.md"; ( cd "$TD/docs" && G add -A && G commit -qm one ) >/dev/null
wt fetch task-19 >/dev/null 2>&1
assert_eq "the first fetch is taken" "$?" "0"
echo two > "$TD/docs/b.md"; ( cd "$TD/docs" && G add -A && G commit -qm two ) >/dev/null
WANT="$(cd "$TD/docs" && git rev-parse HEAD)"
wt fetch task-19 >"$TMP/second" 2>&1
assert_eq "the second fetch is a fast-forward and is taken" "$?" "0"
assert_eq "the workspace branch reached the task's tip" \
  "$(cd "$WS/docs" && git rev-parse refs/heads/task-19)" "$WANT"

# --- a hierarchical collision is refused before any ref moves ---------------
wt new task-20 --repos docs,services/svc-a >/dev/null || { fail "new task-20"; summary 19; exit 1; }
TD2="$(wt_task_dir task-20)"
for r in docs services/svc-a; do
  echo x > "$TD2/$r/w.md"; ( cd "$TD2/$r" && G add -A && G commit -qm w ) >/dev/null
done
# The blocker sits in the second repository of the set, so a fetch that did not
# check first would already have moved the first one's ref.
( cd "$WS/services/svc-a" && G branch task-20/leftover ) >/dev/null
wt fetch task-20 >"$TMP/df" 2>&1
assert_eq "fetch refuses a hierarchical collision" "$?" "1"
if grep -q "WKT_BRANCH_DF_CONFLICT" "$TMP/df"; then pass "the refusal names the collision"; else fail "expected WKT_BRANCH_DF_CONFLICT, got: $(cat "$TMP/df")"; fi
if grep -q "task-20/leftover" "$TMP/df"; then pass "the refusal names the branch in the way"; else fail "the blocking branch is not named: $(cat "$TMP/df")"; fi
if ( cd "$WS/docs" && git rev-parse --verify --quiet refs/heads/task-20 >/dev/null ); then
  fail "the first repository's ref moved even though the set could not complete"
else
  pass "no ref moved anywhere in the set"
fi

# --- a store outlives its task, and still blocks the name ------------------
# task-19's store keeps every branch it holds; drop the task and leave a branch
# beneath the name a new task wants.
for STORE in "$WS.worktrees/store"/*.git; do
  [ -d "$STORE" ] || continue
  case "$(cd "$STORE" && git config --get remote.workspace.url)" in
    */docs) ( cd "$STORE" && G branch task-21/leftover ) >/dev/null ;;
  esac
done
wt new task-21 --repos docs >"$TMP/store" 2>&1
assert_eq "new refuses a collision that only the store has" "$?" "1"
if grep -q "WKT_BRANCH_DF_CONFLICT" "$TMP/store"; then pass "the store refusal names the collision"; else fail "expected WKT_BRANCH_DF_CONFLICT, got: $(cat "$TMP/store")"; fi
assert_no_file "no tree was built for the refused task" "$WS.worktrees/trees/task-21"

summary 19
