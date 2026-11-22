#!/usr/bin/env bash
# Block until at least one job finishes (or timeout), then print its report.
# Usage: wait.sh [timeout-seconds, default 480]
RUN=/home/tae5609/jellyide/docs/plan/logs/jobs
LOGS=/home/tae5609/jellyide/docs/plan/logs

# Print planner inbox items that are not yet in INBOX.done. Planner orders
# override everything; they are printed on every return so they cannot be missed.
inbox() {
  [ -s "$LOGS/INBOX.md" ] || return 0
  local pending=""
  for id in $(grep -oP '^## \K\S+' "$LOGS/INBOX.md"); do
    grep -q "^DONE $id\b" "$LOGS/INBOX.done" 2>/dev/null || pending="$pending $id"
  done
  [ -z "$pending" ] && return 0
  echo
  echo "!!!!! PLANNER INBOX: UNHANDLED ORDERS:$pending !!!!!"
  echo "Handle each BEFORE dispatching anything else, then append 'DONE <id> <note>' to $LOGS/INBOX.done:"
  for id in $pending; do
    awk -v id="$id" '/^## /{p=($2==id)} p' "$LOGS/INBOX.md"
  done
}
deadline=$(( $(date +%s) + ${1:-480} ))
while :; do
  found=0
  for d in "$RUN"/*.done; do
    [ -e "$d" ] || continue
    n=$(basename "$d" .done)
    [ -e "$RUN/$n.reported" ] && continue
    touch "$RUN/$n.reported"; found=1
    if sed 's/\x1b\[[0-9;]*m//g' "$RUN/$n.log" | grep -qE 'Falling back to default agent|^> build · '; then
      echo "=== AGENT_FALLBACK $n: ran on the default agent/model, NOT the requested Orion agent. Discard its report, review its commits with extra care, and redispatch. ==="
    fi
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
  [ $found -eq 1 ] && { "$(dirname "$0")/status.sh"; inbox; exit 0; }
  [ "$(date +%s)" -ge "$deadline" ] && { echo "NONE_FINISHED"; "$(dirname "$0")/status.sh"; inbox; exit 0; }
  sleep 15
done
