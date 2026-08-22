# test/21_post_create.sh
#!/usr/bin/env bash
# The seam runs the workspace's own script after the tree is built. Three
# things it must not do: leak into stdout, reach the developer's repositories
# through a back-fill link, or make removal demand --force. And one it must:
# leave the container free while it runs.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo docs || exit 1
mk_repo services/svc-a || exit 1
wt init >/dev/null || { fail "init"; # The verb runs it again on an existing task, with the same protections.
cat > "$WS/.wkt/post-create" <<'EOS'
#!/bin/sh
for d in */; do touch "$d/late"; done
echo "$WKT_REPOS" | while read -r r; do [ -n "$r" ] && touch "$r/late-set-up"; done
EOS
chmod +x "$WS/.wkt/post-create"
wt new task-21c --repos services/svc-a --no-post-create >"$TMP/out3" 2>&1
assert_eq "new --no-post-create exits 0" "$?" "0"
TD3="$(head -1 "$TMP/out3")"
assert_no_file "--no-post-create skipped the script" "$TD3/services/svc-a/late-set-up"
wt post-create task-21c >"$TMP/pc" 2>&1
assert_eq "wkt post-create exits 0" "$?" "0"
assert_file "the verb ran the script" "$TD3/services/svc-a/late-set-up"
assert_no_file "the verb kept the workspace out of reach" "$WS/docs/late"

summary 21; exit 1; }

WT_BIN="$(cd "$(dirname "$WT_CMD")" && pwd)/$(basename "$WT_CMD")"
mkdir -p "$WS/.wkt"
cat > "$WS/.wkt/post-create" <<EOS
#!/bin/sh
echo "setting up \$WKT_TASK"
# The idiom people write. services/svc-a is the selected one, so docs is the
# back-fill link — and docs sits at the tree root, where this glob reaches it.
for d in */; do touch "\$d/installed"; done
# The sanctioned iteration.
echo "\$WKT_REPOS" | while read -r r; do [ -n "\$r" ] && touch "\$r/set-up"; done
# Ignored content the script produces, of a kind the artifact allowlist does
# not name.
echo local > services/svc-a/local.sqlite
# The container must be free while this runs: another task being created is
# the ordinary case, not an exotic one. --no-post-create breaks the recursion.
"$WT_BIN" new probe-lock --repos services/svc-a --no-post-create --workspace "$WS" >/dev/null 2>&1
echo \$? > "\$WKT_TREE/lock-probe"
EOS
chmod +x "$WS/.wkt/post-create"
printf '*.sqlite\ninstalled\nset-up\n' >> "$WS/services/svc-a/.gitignore"
( cd "$WS/services/svc-a" && G add -A && G commit -qm ignore && G push -q origin main ) >/dev/null

wt new task-21 --repos services/svc-a >"$TMP/out" 2>"$TMP/err"
assert_eq "new exits 0" "$?" "0"
TD="$(head -1 "$TMP/out")"
assert_eq "stdout holds exactly one line" "$(wc -l < "$TMP/out" | tr -d ' ')" "1"
assert_file "and that line is the tree" "$TD"
if grep -q "setting up task-21" "$TMP/err"; then pass "the script's output reached stderr"; else fail "script output missing from stderr"; fi
if grep -q "setting up task-21" "$TMP/out"; then fail "script output leaked into stdout"; else pass "stdout stayed clean"; fi

assert_file "the script set up the materialised repository" "$TD/services/svc-a/set-up"
assert_no_file "the script did not reach the workspace through the back-fill link" "$WS/docs/installed"
if [ -L "$TD/docs" ]; then pass "the back-fill link is back after the run"; else fail "the back-fill link did not come back as a symlink"; fi
assert_eq "the container was free while the script ran" "$(cat "$TD/lock-probe" 2>/dev/null)" "0"

wt rm probe-lock --force >/dev/null 2>&1
wt rm task-21 >"$TMP/rm" 2>&1
assert_eq "removal does not demand --force for what the script produced" "$?" "0"

# A failing script leaves the task standing, and says why.
cat > "$WS/.wkt/post-create" <<'EOS'
#!/bin/sh
echo 'registry unreachable' >&2
exit 3
EOS
chmod +x "$WS/.wkt/post-create"
wt new task-21b --repos services/svc-a >"$TMP/out2" 2>"$TMP/err2"
assert_eq "a failing seam exits non-zero" "$?" "1"
TD2="$(head -1 "$TMP/out2")"
assert_file "the tree is still there to go and fix" "$TD2"
if grep -q "WKT_POST_CREATE_FAILED" "$TMP/err2"; then pass "the failure is named"; else fail "expected WKT_POST_CREATE_FAILED: $(cat "$TMP/err2")"; fi
if grep -q "registry unreachable" "$TMP/err2"; then pass "the script's own words survive"; else fail "the script's output was discarded: $(cat "$TMP/err2")"; fi

# The verb runs it again on an existing task, with the same protections.
cat > "$WS/.wkt/post-create" <<'EOS'
#!/bin/sh
for d in */; do touch "$d/late"; done
echo "$WKT_REPOS" | while read -r r; do [ -n "$r" ] && touch "$r/late-set-up"; done
EOS
chmod +x "$WS/.wkt/post-create"
wt new task-21c --repos services/svc-a --no-post-create >"$TMP/out3" 2>&1
assert_eq "new --no-post-create exits 0" "$?" "0"
TD3="$(head -1 "$TMP/out3")"
assert_no_file "--no-post-create skipped the script" "$TD3/services/svc-a/late-set-up"
wt post-create task-21c >"$TMP/pc" 2>&1
assert_eq "wkt post-create exits 0" "$?" "0"
assert_file "the verb ran the script" "$TD3/services/svc-a/late-set-up"
assert_no_file "the verb kept the workspace out of reach" "$WS/docs/late"

summary 21
