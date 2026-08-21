# test/20m_two_tasks_one_repo.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo svc-a || exit 1
wt init >/dev/null
# task-a is driven through the "create"/"cleanup" aliases end to end, task-b
# through "new"/(never removed here) — between this script and the rest of
# the battery both spellings of both verbs are exercised. Both alias exit
# codes are checked directly, not just inferred from what happens later —
# a broken "create" alias would otherwise only surface several lines down,
# as an unrelated-looking cascading failure once $TA resolves to nothing.
wt create task-a --all >/dev/null
assert_eq "create (alias for new) exits 0" "$?" "0"
wt new task-b --all >/dev/null
TA="$(wt_task_dir task-a)"; TB="$(wt_task_dir task-b)"

# Review finding Important 6: both tasks' worktrees sit at a path whose
# basename is the repository's own leaf name ("svc-a"), but they share one
# store, so git disambiguates the second registration with a numeric suffix
# ("svc-a1"). Both task files must record their own actual registration
# name — repair cannot work without it (spec §5.4) — not the tree-path
# basename, which is "svc-a" for both regardless of which task it is.
CONTAINER="$(dirname "$(dirname "$TA")")"
NAME_A="$(grep '"store_worktree_name"' "$CONTAINER/state/tasks/task-a.json" | sed -E 's/.*: *"([^"]*)".*/\1/')"
NAME_B="$(grep '"store_worktree_name"' "$CONTAINER/state/tasks/task-b.json" | sed -E 's/.*: *"([^"]*)".*/\1/')"
if [ -z "$NAME_A" ] || [ "$NAME_A" = "$NAME_B" ]; then
  fail "the two tasks must record different store worktree registration names, got '$NAME_A' and '$NAME_B'"
else
  pass "the two tasks record different store worktree registration names ($NAME_A vs $NAME_B)"
fi

echo "A" >> "$TA/svc-a/src/index.js"
( cd "$TA/svc-a" && G add -A && G commit -qm "A change" >/dev/null )
assert_eq "task B does not see task A's commit" \
  "$(cd "$TB/svc-a" && git log --oneline | wc -l | tr -d ' ')" "1"
# Clean apart from the perimeter wkt writes into every materialised
# repository — an untracked .claude/settings.json is wkt's own file, not the
# developer's work.
assert_eq "task B stays clean" \
  "$(cd "$TB/svc-a" && git status --porcelain -uall | grep -v '\.claude/settings\.json$')" ""
assert_eq "the workspace stays on its own branch" \
  "$(cd "$WS/svc-a" && git rev-parse --abbrev-ref HEAD)" "main"

( cd "$TA/svc-a" && G push -q -u origin task-a >/dev/null 2>&1 )
wt cleanup task-a >/dev/null 2>&1
assert_eq "cleanup (alias for rm) exits 0" "$?" "0"
assert_no_file "task A removed" "$TA"
assert_file "task B still works" "$TB/svc-a"
assert_eq "task B still on its branch" "$(cd "$TB/svc-a" && git rev-parse --abbrev-ref HEAD)" "task-b"
summary 20m
