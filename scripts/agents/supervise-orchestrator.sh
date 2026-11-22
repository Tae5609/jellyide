#!/usr/bin/env bash
# Keep the orchestrator alive: restart it whenever it exits (rate limit,
# crash, reboot recovery) until it writes the final-report marker.
# Run detached: setsid nohup scripts/agents/supervise-orchestrator.sh >/dev/null 2>&1 &
ROOT=/home/tae5609/jellyide
LOGS="$ROOT/docs/plan/logs"
DONE_MARKER="$LOGS/ORCHESTRATOR_FINISHED"
mkdir -p "$LOGS"
n=0
while [ ! -e "$DONE_MARKER" ]; do
  n=$((n + 1))
  echo "$(date -Is) start #$n" >> "$LOGS/supervisor.log"
  opencode run --pure --agent orchestrator --variant medium --auto --dir "$ROOT" \
    --title "JellyIDE PO (supervised #$n)" \
    "Resume the roadmap (supervised restart #$n; your previous run exited, possibly from an Orion rate limit). Read your instructions, PLAN.md, CONVENTIONS.md, STATUS.md (keep uncommitted edits), BUGS.md and the inbox. Reconstruct state from scripts/agents/status.sh, job logs, worktrees and open PRs. Collect finished jobs with wait.sh, continue every task loop, keep all worker slots busy. When the final delivery report PR is merged, run: touch $DONE_MARKER" \
    > "$LOGS/orchestrator.log" 2>&1 < /dev/null
  echo "$(date -Is) exit #$n code=$? tail: $(tail -c 300 "$LOGS/orchestrator.log" | tr '\n' ' ')" >> "$LOGS/supervisor.log"
  [ -e "$DONE_MARKER" ] || sleep 60
done
echo "$(date -Is) finished" >> "$LOGS/supervisor.log"
