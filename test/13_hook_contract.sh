# test/13_hook_contract.sh
#!/usr/bin/env bash
# Claude Code's worktree hooks. The whole contract is one line of stdout, and
# every warning wkt might grow is a chance to break it.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1
mk_repo shared || exit 1
wt init >/dev/null 2>&1

out="$(printf '{"session_id":"s","cwd":"%s","name":"feat-42"}' "$WS" | "$WT_CMD" hook worktree-create --workspace "$WS" 2>/dev/null)"
assert_eq "worktree-create exits 0" "$?" "0"
assert_eq "and prints exactly one line" "$(printf '%s' "$out" | wc -l | tr -d ' ')" "0"
assert_file "which is a directory that exists" "$out"
assert_file "holding every repository" "$out/services/svc-a"

again="$(printf '{"name":"feat-42","cwd":"%s"}' "$WS" | "$WT_CMD" hook worktree-create --workspace "$WS" 2>/dev/null)"
assert_eq "re-firing returns the same tree (--resume re-fires the event)" "$again" "$out"

slugged="$(printf '{"name":"feature/x","cwd":"%s"}' "$WS" | "$WT_CMD" hook worktree-create --workspace "$WS" 2>/dev/null)"
assert_eq "a slug with a separator is sanitised, not refused" "$(basename "$slugged")" "feature-x"

# Claude Code's own bookkeeping must not make the task unremovable.
mkdir -p "$out/.claude/.cc-writes" && echo '{}' > "$out/.claude/.cc-writes/log.jsonl"
printf '{"worktree_path":"%s"}' "$out" | "$WT_CMD" hook worktree-remove --workspace "$WS" >/dev/null 2>&1
assert_eq "worktree-remove clears a tree whose only extra content is Claude Code's" "$?" "0"
assert_no_file "and the tree is gone" "$out"

# The refusals survive the hook path.
echo "uncommitted" > "$slugged/services/svc-a/scratch.txt"
printf '{"worktree_path":"%s"}' "$slugged" | "$WT_CMD" hook worktree-remove --workspace "$WS" >/dev/null 2>"$TMP/err"
assert_eq "uncommitted work still blocks a hook-driven removal" "$?" "1"
if grep -q "WKT_WOULD_LOSE_WORK" "$TMP/err"; then pass "and the reason reaches stderr, where the user sees it"
else fail "the reason reaches stderr — got $(cat "$TMP/err")"; fi

summary 13
