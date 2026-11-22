#!/usr/bin/env bash
# Block until at least one job finishes (or timeout), then print its report.
# Usage: wait.sh [timeout-seconds, default 480]
RUN=/home/tae5609/jellyide/docs/plan/logs/jobs
deadline=$(( $(date +%s) + ${1:-480} ))
while :; do
  found=0
  for d in "$RUN"/*.done; do
    [ -e "$d" ] || continue
    n=$(basename "$d" .done)
    [ -e "$RUN/$n.reported" ] && continue
    touch "$RUN/$n.reported"; found=1
    echo "=== FINISHED $n (exit $(cat "$d")) — report (last 60 lines of $RUN/$n.log) ==="
    sed 's/\x1b\[[0-9;]*m//g' "$RUN/$n.log" | tail -60
  done
  for p in "$RUN"/*.pid; do
    [ -e "$p" ] || continue
    n=$(basename "$p" .pid)
    if [ ! -e "$RUN/$n.done" ] && ! kill -0 "$(cat "$p")" 2>/dev/null && [ ! -e "$RUN/$n.reported" ]; then
      touch "$RUN/$n.reported"; found=1
      echo "=== DEAD $n (process gone without exit code) — redispatch it ==="
      sed 's/\x1b\[[0-9;]*m//g' "$RUN/$n.log" | tail -20
    fi
  done
  [ $found -eq 1 ] && { "$(dirname "$0")/status.sh"; exit 0; }
  [ "$(date +%s)" -ge "$deadline" ] && { echo "NONE_FINISHED"; "$(dirname "$0")/status.sh"; exit 0; }
  sleep 15
done
