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
