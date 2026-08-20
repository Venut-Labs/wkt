# test/10_exclude_nested.sh
#!/usr/bin/env bash
# Spec §5.3 rule 6: a genuine nested repository is refused by name, with
# --exclude as the escape hatch, "recorded in container state" — so a later
# init must honour it without the flag being repeated.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1
mk_repo services/svc-a/vendored || exit 1

wt init >"$TMP/out" 2>"$TMP/err"
assert_eq "init refuses a nested repository" "$?" "1"
if grep -q "WKT_NESTED_REPO" "$TMP/err"; then pass "the refusal names the code"
else fail "the refusal names the code — got $(cat "$TMP/err")"; fi
if grep -q -- "--exclude" "$TMP/err"; then pass "the refusal points at the escape hatch"
else fail "the refusal points at the escape hatch"; fi

wt init --exclude services/svc-a/vendored >/dev/null 2>&1
assert_eq "init --exclude adopts the workspace" "$?" "0"
assert_file "the exclusion is recorded in container state" "$WS.worktrees/state/container.json"

wt init >/dev/null 2>&1
assert_eq "a later init honours the recorded exclusion" "$?" "0"

wt new t1 --all >/dev/null 2>&1
assert_eq "a task over the outer repository works" "$?" "0"

wt init --exclude services/typo >"$TMP/out2" 2>"$TMP/err2"
assert_eq "excluding a path that is not nested fails" "$?" "1"
if grep -q "WKT_NO_SUCH_NESTED_REPO" "$TMP/err2"; then pass "the typo is named"
else fail "the typo is named — got $(cat "$TMP/err2")"; fi

summary 10
