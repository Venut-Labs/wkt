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
