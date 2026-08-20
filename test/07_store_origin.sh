# test/07_store_origin.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo svc-a || exit 1
wt init >/dev/null; wt new task-7 --all >/dev/null
TD="$(wt_task_dir task-7)"

WANT="$(cd "$WS/svc-a" && git remote get-url origin)"
# --path-format needs git 2.31; our floor is 2.29, so resolve by hand.
STORE="$(cd "$TD/svc-a" && cd "$(git rev-parse --git-common-dir)" && pwd)"
GOT="$(git -C "$STORE" remote get-url origin)"
assert_eq "store origin equals the workspace origin" "$GOT" "$WANT"
( cd "$TD/svc-a" && G push -q -u origin task-7 ) && pass "push from the tree reaches the real remote" \
  || fail "push from the tree reaches the real remote"
assert_eq "the branch landed on the remote" \
  "$(git -C "$REMOTES/svc-a.git" rev-parse --verify --quiet task-7 >/dev/null && echo yes)" "yes"
summary 07
