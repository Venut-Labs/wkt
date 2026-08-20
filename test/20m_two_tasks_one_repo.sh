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
