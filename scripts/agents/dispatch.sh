#!/usr/bin/env bash
# Start an opencode worker in the background (non-blocking).
# Usage: dispatch.sh <job-name> <agent> <worktree> <prompt-file>
#   agent ending in "-b" runs on Orion key B (max 2 parallel; 1 slot kept for opencode small_model calls);
#   other agents run on key A (max 2 parallel; the orchestrator needs the 3rd slot —
#   a 4th request on a key is rejected with a 429 and kills the run).
set -euo pipefail
ROOT=/home/tae5609/jellyide
RUN="$ROOT/docs/plan/logs/jobs"
mkdir -p "$RUN"
[ $# -eq 4 ] || { echo "usage: dispatch.sh <job-name> <agent> <worktree> <prompt-file>"; exit 2; }
name=$1 agent=$2 wt=$3 prompt=$4
[ -d "$wt" ] || { echo "NO_WORKTREE $wt"; exit 2; }
[ -f "$prompt" ] || { echo "NO_PROMPT_FILE $prompt"; exit 2; }
case "$agent" in *-b) lane=B; max=2 ;; *) lane=A; max=2 ;; esac

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

# Guard: the agent must resolve to itself inside the worktree. A stale
# .opencode/agents in an old branch, or a missing global install, makes
# opencode silently fall back to its default agent on a non-Orion model.
proj="$wt/.opencode/agents/$agent.md"
if [ -e "$proj" ] && ! cmp -s "$proj" "$HOME/.config/opencode/agents/$agent.md"; then
  echo "STALE_AGENTS $proj shadows the global agent with an old version. Rebase the worktree on origin/main first (commit WIP, then git rebase origin/main)."; exit 5
fi
if ! (cd "$wt" && opencode agent list 2>/dev/null) | grep -q "^$agent (\(all\|primary\))"; then
  echo "AGENT_NOT_RUNNABLE $agent in $wt. Run scripts/agents/install.sh"; exit 5
fi

# Planner hold list: task ids (one per line, e.g. T19) that must not be dispatched.
HOLD="$ROOT/docs/plan/logs/HOLD"
if [ -f "$HOLD" ]; then
  task="${name%%-*}"
  if grep -qx "$task" "$HOLD"; then
    echo "ON_HOLD $task is held by the planner (see $HOLD and the inbox). Do not dispatch it."; exit 6
  fi
fi

cp "$prompt" "$RUN/$name.prompt.md"
echo "$lane" > "$RUN/$name.lane"
rm -f "$RUN/$name.done" "$RUN/$name.reported"
setsid nohup bash -c '
  # Auto-resume on Orion rate limits (max 3 parallel requests per key):
  # a 429 kills opencode run, so relaunch with a continuation note (max 6 tries).
  : > "$5"
  for try in 1 2 3 4 5 6; do
    start=$(stat -c %s "$5")
    if [ "$try" -eq 1 ]; then msg="Follow the attached job instructions exactly. Your final message is your report."
    else msg="Follow the attached job instructions exactly. NOTE: your previous attempt (try $((try-1))) was killed by an API rate limit mid-work. Inspect git status/log in the worktree and continue from the existing work; do not restart from scratch. Your final message is your report."; fi
    opencode run --pure --agent "$1" --auto --dir "$2" --title "$3" "$msg" -f "$4" >> "$5" 2>&1
    code=$?
    if tail -c +"$((start + 1))" "$5" | grep -q "Rate limit exceeded"; then
      echo "[dispatch] rate-limited on try $try; resuming in 90s" >> "$5"; sleep 90; continue
    fi
    break
  done
  echo $code > "$6"
' _ "$agent" "$wt" "$name" "$RUN/$name.prompt.md" "$RUN/$name.log" "$RUN/$name.done" \
  >/dev/null 2>&1 < /dev/null &
echo $! > "$RUN/$name.pid"
echo "DISPATCHED $name agent=$agent lane=$lane log=$RUN/$name.log"
