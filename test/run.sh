# test/run.sh
#!/usr/bin/env bash
set -u
: "${WT_CMD:?set WT_CMD to the wkt binary}"
dir="$(cd "$(dirname "$0")" && pwd)"
rc=0
for t in "$dir"/[0-9]*.sh; do
  printf '\n== %s ==\n' "$(basename "$t")"
  bash "$t" || rc=1
done
exit $rc
