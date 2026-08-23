# test/23_own_settings.sh
#!/usr/bin/env bash
# Plenty of repositories carry their own .claude/settings.json in git.
# Refusing the whole task over one made wkt unusable on them; the file still
# has to survive, and the gap has to be said out loud.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo docs || exit 1
mk_repo services/svc-a || exit 1
mkdir -p "$WS/docs/.claude"
printf '{\n  "permissions": { "allow": ["Bash(make *)"] }\n}\n' > "$WS/docs/.claude/settings.json"
MINE="$(cat "$WS/docs/.claude/settings.json")"
( cd "$WS/docs" && G add -A && G commit -qm "own claude settings" && G push -q origin main ) >/dev/null
wt init >/dev/null || { fail "init"; summary 23; exit 1; }

wt new task-23 --all >"$TMP/out" 2>"$TMP/err"
assert_eq "a repository with its own settings does not fail the task" "$?" "0"
TD="$(head -1 "$TMP/out")"
assert_file "the tree is built" "$TD/docs"
assert_eq "the repository's own settings survive untouched" "$(cat "$TD/docs/.claude/settings.json")" "$MINE"
if grep -q "WKT_PERIMETER_SKIPPED" "$TMP/err"; then pass "the uncovered directory is named"; else fail "the gap was silent: $(cat "$TMP/err")"; fi
assert_file "the rest of the tree is still covered" "$TD/.claude/settings.json"
assert_file "and so is the repository wkt does own" "$TD/services/svc-a/.claude/settings.json"
wt rm task-23 >/dev/null 2>&1
assert_eq "and it still removes cleanly" "$?" "0"
summary 23
