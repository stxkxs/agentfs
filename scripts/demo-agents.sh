#!/usr/bin/env bash
# Simulates two AI agents writing to a workspace directory.
# Usage: ./scripts/demo-agents.sh [workspace-dir]

set -euo pipefail

DIR="${1:-/tmp/agentfs-workspace}"

mkdir -p "$DIR/agent-researcher/"{memory,logs,runs}
mkdir -p "$DIR/agent-writer/"{memory,logs,artifacts}

echo "workspace: $DIR"
echo "start agentfs in another terminal: agentfs $DIR"
echo "press ctrl+c to stop"
echo ""

step=0
while true; do
  step=$((step + 1))

  # --- Agent: researcher ---
  status="running"
  task="research"
  if (( step % 10 == 0 )); then status="error"; task="research"; fi
  if (( step % 15 == 0 )); then status="done"; task="summarize"; fi

  cat > "$DIR/agent-researcher/state.json" <<EOF
{"status": "$status", "task": "$task", "step": $step, "model": "claude-sonnet-4-6"}
EOF

  ts=$(date '+%Y-%m-%d %H:%M:%S')
  level="INFO"
  if (( step % 10 == 0 )); then level="ERROR"; fi
  echo "$ts $level [researcher] step=$step task=$task status=$status" \
    >> "$DIR/agent-researcher/logs/run.log"

  # Memory updates
  if (( step % 3 == 0 )); then
    cat > "$DIR/agent-researcher/memory/context.json" <<EOF
{"facts": ["fact_$step", "fact_$((step-1))"], "updated": "$ts"}
EOF
  fi

  # Periodic run snapshots
  if (( step % 8 == 0 )); then
    run_id=$(printf "run-%03d" $((step / 8)))
    mkdir -p "$DIR/agent-researcher/runs/$run_id"
    cat > "$DIR/agent-researcher/runs/$run_id/state.json" <<EOF
{"status": "done", "task": "$task", "step": $step}
EOF
    echo '{"results": ["finding_a", "finding_b"]}' \
      > "$DIR/agent-researcher/runs/$run_id/output.json"
  fi

  # --- Agent: writer ---
  w_status="idle"
  w_task="waiting"
  if (( step % 4 == 0 )); then w_status="running"; w_task="drafting"; fi
  if (( step % 12 == 0 )); then w_status="done"; w_task="publish"; fi

  cat > "$DIR/agent-writer/state.json" <<EOF
{"status": "$w_status", "task": "$w_task", "step": $step}
EOF

  ts=$(date '+%Y-%m-%d %H:%M:%S')
  echo "$ts INFO [writer] step=$step task=$w_task status=$w_status" \
    >> "$DIR/agent-writer/logs/run.log"

  # Artifact output
  if (( step % 6 == 0 )); then
    cat > "$DIR/agent-writer/artifacts/draft-$step.md" <<EOF
# Draft $step
Generated at $ts by agent-writer.
Task: $w_task
EOF
  fi

  sleep 2
done
