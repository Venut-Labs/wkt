# test/01_nested_discovery.sh
#!/usr/bin/env bash
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
for r in services/svc-a services/svc-b docs shared; do mk_repo "$r" || exit 1; done
mkdir -p "$WS/notes"; echo scratch > "$WS/notes/s.md"; echo '# conv' > "$WS/CONVENTIONS.md"

wt init >/dev/null || { fail "init"; summary 01; exit 1; }
wt new task-1 --all >/dev/null || { fail "new"; summary 01; exit 1; }
TD="$(wt_task_dir task-1)"

for r in services/svc-a services/svc-b docs shared; do
  assert_file "$r materialised" "$TD/$r"
  if path_has_symlink "$TD" "$r"; then fail "$r: a path component is a symlink"; else pass "$r: no symlinked path component"; fi
  assert_eq "$r on the task branch" "$(cd "$TD/$r" && git rev-parse --abbrev-ref HEAD)" "task-1"
done
assert_file "non-git directory carried" "$TD/notes"
assert_file "loose file carried" "$TD/CONVENTIONS.md"
assert_eq "copied loose file matches the workspace original byte for byte" \
  "$(cat "$TD/CONVENTIONS.md")" "$(cat "$WS/CONVENTIONS.md")"
summary 01
