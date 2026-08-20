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
