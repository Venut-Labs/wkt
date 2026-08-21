# test/17_identity.sh
#!/usr/bin/env bash
# Commits made in a task tree must be attributed to whoever the repository
# says, not to whatever the global identity happens to be — and building a
# store must not run the developer's template hooks.
. "$(dirname "$0")/lib.sh"
wt_init_env; trap wt_cleanup_env EXIT
mk_repo services/svc-a || exit 1

# A repository-specific identity, the way people separate work from personal.
( cd "$WS/services/svc-a" && git config user.email "work@company.com" \
    && git config user.name "Work Identity" )

wt init >/dev/null 2>&1
wt new task-17 --all >/dev/null 2>&1
TD="$(wt_task_dir task-17)"

assert_eq "the task tree resolves the repository's identity" \
  "$(cd "$TD/services/svc-a" && git config user.email)" "work@company.com"

( cd "$TD/services/svc-a" && echo change >> src/index.js && git add -A \
    && git commit -qm "work in the task" >/dev/null )
assert_eq "and a commit made there carries it" \
  "$(cd "$TD/services/svc-a" && git log -1 --format='%ae')" "work@company.com"

# Changing it in the repository reaches existing stores, so an identity is
# never frozen at the moment a task happened to be created.
( cd "$WS/services/svc-a" && git config user.email "changed@company.com" )
wt new task-17b --all >/dev/null 2>&1
assert_eq "a later task picks up the change" \
  "$(cd "$(wt_task_dir task-17b)/services/svc-a" && git config user.email)" "changed@company.com"

# The store carries nothing that can execute.
STORE="$(ls -d "$WS.worktrees"/store/*.git | head -1)"
for key in filter.evil.smudge core.sshCommand credential.helper gpg.program init.templateDir; do
  if git -C "$STORE" config --local --get "$key" >/dev/null 2>&1; then
    fail "the store carries $key"
  else
    pass "the store does not carry $key"
  fi
done

summary 17
