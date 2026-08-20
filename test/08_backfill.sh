# test/08_backfill.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1
mk_repo shared || exit 1
wt init >/dev/null
wt new task-8 --repos services/svc-a >/dev/null
TD="$(wt_task_dir task-8)"

assert_file "services/svc-a materialised" "$TD/services/svc-a"
if path_has_symlink "$TD" "services/svc-a"; then fail "services/svc-a: unexpectedly linked"; else pass "services/svc-a: real worktree, no symlinked ancestor"; fi
assert_eq "services/svc-a on the task branch" "$(cd "$TD/services/svc-a" && git rev-parse --abbrev-ref HEAD)" "task-8"
if [ -d "$TD/services" ] && [ ! -L "$TD/services" ]; then pass "services stays a real directory, not collapsed into one link"; else fail "services should be a real directory"; fi

assert_eq "shared (unselected) back-fills as a symlink" "$([ -L "$TD/shared" ] && echo yes || echo no)" "yes"
GOT="$(cat "$TD/services/svc-a/../../shared/src/index.js" 2>/dev/null)"
WANT="$(cat "$WS/shared/src/index.js")"
assert_eq "shared resolves through the back-fill link from a sibling repo's path and matches the workspace" "$GOT" "$WANT"
assert_eq "the workspace's shared stays on its own branch, untouched by the task" \
  "$(cd "$WS/shared" && git rev-parse --abbrev-ref HEAD)" "main"
summary 08
