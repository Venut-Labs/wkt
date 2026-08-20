# test/11_os_artifacts.sh
#!/usr/bin/env bash
# Live-run finding L2: on macOS, Finder writes .DS_Store into every directory
# a user opens — the task tree included. An artifact nobody created on purpose
# must never make a task drift or need --force to remove.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1
printf 'finder junk\n' > "$WS/.DS_Store"
printf 'real content\n' > "$WS/NOTES.md"

wt init >/dev/null 2>&1
wt new task-11 --all >/dev/null 2>&1
TD="$(wt_task_dir task-11)"

assert_no_file "the workspace .DS_Store is not copied into the tree" "$TD/.DS_Store"
assert_file    "a real loose file still is" "$TD/NOTES.md"

# Finder now opens the tree itself.
printf 'finder junk in the tree\n' > "$TD/.DS_Store"
printf 'finder junk deeper\n' > "$TD/services/.DS_Store"

wt status task-11 >"$TMP/status" 2>&1
assert_eq "an OS artifact in the tree is not drift" "$?" "0"
if grep -q "WKT_REGENERABLE_TREE_CONTENT" "$TMP/status"; then pass "it is still listed, marked informational"
else fail "it is still listed — got $(cat "$TMP/status")"; fi

wt rm task-11 >/dev/null 2>&1
assert_eq "rm needs no --force for it" "$?" "0"
assert_no_file "the tree is gone" "$TD"

summary 11
