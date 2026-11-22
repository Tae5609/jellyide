#!/usr/bin/env bash
# Start an opencode worker in the background (non-blocking).
# Usage: dispatch.sh <job-name> <agent> <worktree> <prompt-file>
#   agent ending in "-b" runs on Orion key B (max 3 parallel);
#   other agents run on key A (max 2 parallel; the orchestrator holds the 3rd slot).
set -euo pipefail
ROOT=/home/tae5609/jellyide
RUN="$ROOT/docs/plan/logs/jobs"
mkdir -p "$RUN"
[ $# -eq 4 ] || { echo "usage: dispatch.sh <job-name> <agent> <worktree> <prompt-file>"; exit 2; }
name=$1 agent=$2 wt=$3 prompt=$4
[ -d "$wt" ] || { echo "NO_WORKTREE $wt"; exit 2; }
[ -f "$prompt" ] || { echo "NO_PROMPT_FILE $prompt"; exit 2; }
case "$agent" in *-b) lane=B; max=3 ;; *) lane=A; max=2 ;; esac

running=0
for f in "$RUN"/*.pid; do
  [ -e "$f" ] || continue
  if kill -0 "$(cat "$f")" 2>/dev/null && [ "$(cat "${f%.pid}.lane" 2>/dev/null)" = "$lane" ]; then
    running=$((running + 1))
  fi
done
if [ "$running" -ge "$max" ]; then echo "LANE_FULL lane=$lane running=$running max=$max"; exit 3; fi
if [ -e "$RUN/$name.pid" ] && kill -0 "$(cat "$RUN/$name.pid")" 2>/dev/null; then
  echo "ALREADY_RUNNING $name"; exit 4
fi

cp "$prompt" "$RUN/$name.prompt.md"
echo "$lane" > "$RUN/$name.lane"
rm -f "$RUN/$name.done" "$RUN/$name.reported"
setsid nohup bash -c '
  opencode run --pure --agent "$1" --auto --dir "$2" --title "$3" \
    "Follow the attached job instructions exactly. Your final message is your report." \
    -f "$4" > "$5" 2>&1
  echo $? > "$6"
' _ "$agent" "$wt" "$name" "$RUN/$name.prompt.md" "$RUN/$name.log" "$RUN/$name.done" \
  >/dev/null 2>&1 < /dev/null &
echo $! > "$RUN/$name.pid"
echo "DISPATCHED $name agent=$agent lane=$lane log=$RUN/$name.log"
