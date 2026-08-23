# test/22_seam_concurrency.sh
#!/usr/bin/env bash
# The seam releases the container lock and then runs for minutes. Whatever
# else happens to the task in that window must survive it.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo docs || exit 1
mk_repo services/svc-a || exit 1
wt init >/dev/null || { fail "init"; summary 22; exit 1; }
WT_BIN="$(cd "$(dirname "$WT_CMD")" && pwd)/$(basename "$WT_CMD")"
mkdir -p "$WS/.wkt"

# --- a concurrent graft must not be lost ----------------------------------
cat > "$WS/.wkt/post-create" <<EOS
#!/bin/sh
[ -n "\$WKT_ADDED_REPO" ] && exit 0
"$WT_BIN" add "\$WKT_TASK" --repos services/svc-a --no-post-create --workspace "$WS" >/dev/null 2>&1
echo \$? > "\$WKT_TREE/graft-exit"
EOS
chmod +x "$WS/.wkt/post-create"
wt new task-22 --repos docs >"$TMP/out" 2>"$TMP/err"
assert_eq "new exits 0" "$?" "0"
TD="$(head -1 "$TMP/out")"
assert_eq "the concurrent graft succeeded" "$(cat "$TD/graft-exit" 2>/dev/null)" "0"
# Parse the repos array, never grep the whole file: the stale snapshot still
# carries a back-fill link slot whose rel_path is services/svc-a, so a plain
# grep passes while the graft is gone.
if python3 -c "
import json,sys
t=json.load(open(sys.argv[1]))
sys.exit(0 if any(r['rel_path']=='services/svc-a' for r in t.get('repos') or []) else 1)
" "$WS.worktrees/state/tasks/task-22.json" 2>/dev/null; then
  pass "the graft survived the seam's state write"
else
  fail "the seam clobbered the concurrent graft: state's repos no longer holds services/svc-a"
fi
assert_file "the grafted worktree is on disk" "$TD/services/svc-a/.git"

# --- a task removed mid-seam must not come back ---------------------------
cat > "$WS/.wkt/post-create" <<EOS
#!/bin/sh
"$WT_BIN" rm "\$WKT_TASK" --force --workspace "$WS" >/dev/null 2>&1
echo \$? > "$TMP/rm-exit"
EOS
chmod +x "$WS/.wkt/post-create"
wt new task-22b --repos docs >"$TMP/out2" 2>"$TMP/err2"
assert_eq "new exits 0 even though the removal was refused" "$?" "0"
if [ "$(cat "$TMP/rm-exit" 2>/dev/null)" = "0" ]; then
  fail "a removal ran to completion while the task's setup was in progress"
else
  pass "removal is refused while the task's setup is running"
fi
assert_file "and the task is still there afterwards" "$WS.worktrees/state/tasks/task-22b.json"
wt rm task-22b --force >/dev/null 2>&1
assert_eq "it removes cleanly once the setup is done" "$?" "0"

summary 22
