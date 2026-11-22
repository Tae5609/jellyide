#!/usr/bin/env bash
# List all worker jobs and their state.
RUN=/home/tae5609/jellyide/docs/plan/logs/jobs
printf '%-28s %-4s %-14s %s\n' JOB LANE STATE LAST_UPDATE
for f in "$RUN"/*.lane; do
  [ -e "$f" ] || { echo "(no jobs)"; exit 0; }
  n=$(basename "$f" .lane)
  if [ -e "$RUN/$n.done" ]; then st="done(exit=$(cat "$RUN/$n.done"))"
  elif kill -0 "$(cat "$RUN/$n.pid" 2>/dev/null)" 2>/dev/null; then st=running
  else st=dead; fi
  printf '%-28s %-4s %-14s %s\n' "$n" "$(cat "$f")" "$st" "$(date -r "$RUN/$n.log" +%H:%M:%S 2>/dev/null)"
done
for l in A B; do
  c=0; for f in "$RUN"/*.pid; do [ -e "$f" ] || continue
    kill -0 "$(cat "$f")" 2>/dev/null && [ "$(cat "${f%.pid}.lane")" = "$l" ] && c=$((c+1)); done
  echo "lane $l running: $c / $([ $l = A ] && echo 2 || echo 3)"
done
