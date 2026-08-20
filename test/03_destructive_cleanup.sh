# test/03_destructive_cleanup.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo docs || exit 1
wt init >/dev/null; wt new task-3 --all >/dev/null
TD="$(wt_task_dir task-3)"

# Four classes of work, one of which git itself never protects (spec H1).
echo "TOKEN=secret" > "$TD/docs/.env"
echo "draft" > "$TD/docs/untracked.md"
echo "change" >> "$TD/docs/src/index.js"
( cd "$TD/docs" && G add -A && G commit -qm "unpushed" >/dev/null )

wt rm task-3 >/dev/null 2>&1
assert_eq "plain rm refuses" "$?" "1"
assert_file "ignored .env preserved" "$TD/docs/.env"
assert_file "untracked file preserved" "$TD/docs/untracked.md"
assert_file "tree still present" "$TD/docs"
summary 03
