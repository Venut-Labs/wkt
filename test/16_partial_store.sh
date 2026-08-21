# test/16_partial_store.sh
#!/usr/bin/env bash
# A store build interrupted after the clone leaves a directory that looks
# finished. Adopting it silently is how a task's commits become unreadable
# later, so wkt verifies before reuse — and never deletes what it refuses.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1

wt init >/dev/null 2>&1

# Build the store by hand, the way an interrupted "wkt new" leaves it: the
# clone happened, the hardening did not.
STORE_ID="$("$WT_CMD" new probe --all --workspace "$WS" >/dev/null 2>&1; ls "$WS.worktrees/store" | head -1)"
wt rm probe --force >/dev/null 2>&1
rm -rf "$WS.worktrees/store/$STORE_ID"
G clone --shared --bare -q "$WS/services/svc-a" "$WS.worktrees/store/$STORE_ID"
printf '#!/bin/sh\necho planted >&2\n' > "$WS.worktrees/store/$STORE_ID/hooks/pre-commit"
chmod +x "$WS.worktrees/store/$STORE_ID/hooks/pre-commit"

wt new task-16 --all >"$TMP/out" 2>"$TMP/err"
assert_eq "new refuses an unfinished store" "$?" "1"
if grep -q "WKT_STORE_INCOMPLETE" "$TMP/err"; then pass "and names the code"
else fail "and names the code — got $(cat "$TMP/err")"; fi
if grep -q "borrows objects" "$TMP/err"; then pass "and says what is wrong with it"
else fail "and says what is wrong with it"; fi

assert_file "the store is left exactly as found" "$WS.worktrees/store/$STORE_ID/hooks/pre-commit"
assert_no_file "and no half-made tree is left behind" "$WS.worktrees/trees/task-16"

wt doctor >"$TMP/doc" 2>&1
assert_eq "doctor reports it too (exit 3)" "$?" "3"
if grep -q "WKT_STORE_INCOMPLETE" "$TMP/doc"; then pass "by name"; else fail "by name"; fi

wt doctor --fix >/dev/null 2>&1
assert_file "--fix does not touch it either" "$WS.worktrees/store/$STORE_ID/hooks/pre-commit"

# Once the unfinished store is out of the way, everything works again.
rm -rf "$WS.worktrees/store/$STORE_ID"
wt new task-16 --all >/dev/null 2>&1
assert_eq "and a fresh build succeeds" "$?" "0"

summary 16
