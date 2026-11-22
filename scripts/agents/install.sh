#!/usr/bin/env bash
# Install the JellyIDE opencode agents globally (~/.config/opencode/agents).
# Agents are global on purpose: workers run inside git worktrees, and a
# project-level .opencode/agents in an old branch would silently shadow them.
set -euo pipefail
src="$(cd "$(dirname "$0")" && pwd)/opencode-agents"
dst="$HOME/.config/opencode/agents"
mkdir -p "$dst"
for f in "$src"/*.md; do install -m 0644 "$f" "$dst/$(basename "$f")"; done
echo "installed: $(ls "$src" | tr '\n' ' ')→ $dst"
