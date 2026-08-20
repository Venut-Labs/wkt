# test/05_staging_fence.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env
CONTAINER=""
fence_cleanup() { [ -n "$CONTAINER" ] && [ -d "$CONTAINER" ] && chmod -R u+w "$CONTAINER" 2>/dev/null; wt_cleanup_env; }
trap fence_cleanup EXIT
mk_repo docs || exit 1
wt init >/dev/null; wt new task-5 --all >/dev/null
TD="$(wt_task_dir task-5)"
CONTAINER="$(dirname "$(dirname "$TD")")"
STAGED="$CONTAINER/staging/task-5"

echo "draft" > "$TD/docs/untracked.md"
# Lock a directory INSIDE the tree, not the container's staging/ itself.
# staging/ has to stay writable or the rename that is the fence can never
# even start — renaming into a directory needs write on the *destination*
# parent, confirmed empirically before writing this test: chmod 500 on
# staging/ makes the rename itself fail with EACCES, never reaching a
# delete to observe at all. Locking content already inside the tree instead
# leaves both rename endpoints writable, so the fence gets to fire, and only
# the recursive delete that follows it trips — on the locked subtree, not
# on the move.
chmod 500 "$TD/docs"

wt rm task-5 --force >/dev/null 2>&1
RC=$?
chmod -R u+w "$CONTAINER"
assert_eq "rm --force reports the incomplete cleanup rather than pretending it finished" "$RC" "1"
assert_no_file "the fence already moved the tree off its original path" "$TD"
assert_file "the content the delete couldn't reach survived under staging" "$STAGED/docs/untracked.md"
summary 05
