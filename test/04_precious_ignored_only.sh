# test/04_precious_ignored_only.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo app || exit 1
wt init >/dev/null; wt new task-4 --all >/dev/null
TD="$(wt_task_dir task-4)"

# The ONE blocker, and nothing else: a gitignored file, no commit, no
# untracked non-ignored file, no modification. This is the class of work
# git itself never guards (spec H1), and the only one test 03 leaves
# unpinned — a battery that always refuses would still pass a scenario
# with four simultaneous blockers. Isolate it.
echo "TOKEN=secret" > "$TD/app/.env"

wt rm task-4 >/dev/null 2>&1
assert_eq "rm refuses on a gitignored file alone" "$?" "1"
assert_file ".env survives the refusal" "$TD/app/.env"
assert_file "tree still present after the refusal" "$TD/app"

( cd "$TD/app" && G clean -fdX -q )
wt rm task-4 >/dev/null 2>&1
assert_eq "rm succeeds once the only blocker is gone — no --force needed" "$?" "0"
assert_no_file "tree removed" "$TD/app"
summary 04
