# test/18_carry.sh
#!/usr/bin/env bash
# The gitignored-file carry: a worktree is a fresh checkout, so a service that
# needs a local .env cannot run in a new tree until one arrives (issue #3).
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1

# mk_repo already gitignores .env and dist/.
printf 'TOKEN=local\n' > "$WS/services/svc-a/.env"
printf 'secret\n' > "$WS/services/svc-a/untracked-but-not-ignored.txt"
mkdir -p "$WS/services/svc-a/dist" && printf 'build\n' > "$WS/services/svc-a/dist/app.js"
printf '.env\n' > "$WS/.wktinclude"

wt init >/dev/null 2>&1
wt new task-18 --all >/dev/null 2>&1
TD="$(wt_task_dir task-18)"

assert_file "the ignored file named in .wktinclude is carried" "$TD/services/svc-a/.env"
assert_eq "with its contents" "$(cat "$TD/services/svc-a/.env")" "TOKEN=local"
assert_no_file "a file not named in .wktinclude is not" "$TD/services/svc-a/dist/app.js"
assert_no_file "and neither is an untracked file that is not ignored" \
  "$TD/services/svc-a/untracked-but-not-ignored.txt"

# A copy, not a link: editing it in the task must not reach back.
if [ -L "$TD/services/svc-a/.env" ]; then fail "carried files are copies"; else pass "carried files are copies"; fi
printf 'TOKEN=changed\n' > "$TD/services/svc-a/.env"
assert_eq "editing it does not touch the developer's own file" \
  "$(cat "$WS/services/svc-a/.env")" "TOKEN=local"

# Edited, so it now holds something the original does not.
wt rm task-18 >/dev/null 2>&1
assert_eq "an edited carried file blocks removal" "$?" "1"

# Put it back, and removal is clean again: nothing is lost.
printf 'TOKEN=local\n' > "$TD/services/svc-a/.env"
wt rm task-18 >/dev/null 2>&1
assert_eq "an untouched one does not" "$?" "0"

summary 18
