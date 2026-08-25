#!/usr/bin/env bash
#
# Fill a workspace with simulated agents, so agentfs has something to watch.
#
#   scripts/demo-agents.sh [workspace-directory]
#   task demo -- /tmp/agentfs-workspace
#
# The workspace holds one directory per agent, and the five agents together
# cover the contract's vocabulary: running, idle, blocked, done alongside a
# recovered problem, and error alongside the problem that stopped it.
#
# Every state document written here conforms to agentfs/v1 and is written
# atomically — to a temporary file in the same directory, then renamed over the
# target. A reader opens the file at moments the writer does not choose, so a
# writer that truncates the target and streams into it hands that reader a
# document that is not JSON. The reference writers under contrib/ additionally
# flush and fsync the file and its directory before the rename, which orders
# the bytes against a crash rather than against a concurrent reader.
#
# Check what it wrote:
#
#   agentfs validate /tmp/agentfs-workspace

set -euo pipefail

readonly SCHEMA="agentfs/v1"
readonly MODEL="claude-opus-5"

# HEARTBEAT is the undertaking each agent document makes: rewrite me at least
# this often. The loop rewrites every TICK seconds, well inside it, so a
# workspace this script is still driving never reads as stale.
readonly HEARTBEAT=30
readonly TICK=2

# STEPS_TOTAL is the length of the researcher's task, and RUN_RING is how many
# run directories it reuses. Both are bounded so a demo left running overnight
# holds a workspace of the same size as one started a minute ago.
readonly STEPS_TOTAL=8
readonly RUN_RING=6

# TEMP_PREFIX names the temporary file every atomic write goes through. One
# prefix for all of them is what lets the exit trap find a write a signal
# interrupted between the write and the rename.
readonly TEMP_PREFIX=".agentfs-demo"

DIR="${1:-/tmp/agentfs-workspace}"
readonly DIR

# rfc3339 prints the current instant with an offset, which is the only form the
# contract accepts: a local date-time names no instant to a reader on another
# host.
rfc3339() {
  date -u '+%Y-%m-%dT%H:%M:%SZ'
}

# write_atomic writes stdin to the named path through a temporary file in the
# same directory. The rename is what makes the replacement atomic; a temporary
# file on another filesystem would be copied rather than renamed, which is the
# torn read this avoids.
write_atomic() {
  local target="$1" dir tmp
  dir="$(dirname "$target")"
  tmp="$(mktemp "${dir}/${TEMP_PREFIX}.XXXXXX")"
  cat >"$tmp"
  mv -f "$tmp" "$target"
}

# cleanup removes a temporary file left behind when a signal arrives between
# the write and the rename.
cleanup() {
  find "$DIR" -name "${TEMP_PREFIX}.*" -type f -delete 2>/dev/null || true
}

trap cleanup EXIT
trap 'exit 0' INT TERM

# agent-reviewer holds no subdirectory: an agent is recognized by the state
# document it writes, and a workspace that carries one of the conventional
# directories — logs, memory, artifacts, tools, runs — is recognized even
# before it writes one.
mkdir -p \
  "$DIR/agent-researcher/logs" \
  "$DIR/agent-researcher/memory" \
  "$DIR/agent-researcher/runs" \
  "$DIR/agent-writer/logs" \
  "$DIR/agent-writer/artifacts" \
  "$DIR/agent-reviewer" \
  "$DIR/agent-indexer/runs/run-index" \
  "$DIR/agent-fetcher/logs"

printf 'workspace  %s\n' "$DIR"
printf 'watch      agentfs %s\n' "$DIR"
printf 'check      agentfs validate %s\n' "$DIR"
printf 'stop       ctrl+c\n\n'

started="$(rfc3339)"
step=0
run_seq=0

# The indexer's run is archived once, before the loop: it is the record of the
# work the agent reports as done.
write_atomic "$DIR/agent-indexer/runs/run-index/state.json" <<EOF
{
  "schema": "$SCHEMA",
  "status": "done",
  "agent": "indexer",
  "task": "Index the workspace tree",
  "step": $STEPS_TOTAL,
  "steps_total": $STEPS_TOTAL,
  "model": "$MODEL",
  "run_id": "run-index",
  "problem": "The first index pass timed out and was retried.",
  "started_at": "$started",
  "updated_at": "$started"
}
EOF

while true; do
  step=$(( step % STEPS_TOTAL + 1 ))
  ts="$(rfc3339)"
  run_id="$(printf 'run-%03d' "$(( run_seq % RUN_RING + 1 ))")"

  # researcher — running, and the only agent whose step advances.
  write_atomic "$DIR/agent-researcher/state.json" <<EOF
{
  "schema": "$SCHEMA",
  "status": "running",
  "agent": "researcher",
  "task": "Retrieve and rank sources",
  "step": $step,
  "steps_total": $STEPS_TOTAL,
  "model": "$MODEL",
  "run_id": "$run_id",
  "heartbeat_seconds": $HEARTBEAT,
  "started_at": "$started",
  "updated_at": "$ts",
  "labels": {
    "queue": "batch",
    "stage": "retrieval"
  }
}
EOF
  printf '%s INFO  [researcher] step=%d/%d run=%s retrieving\n' \
    "$ts" "$step" "$STEPS_TOTAL" "$run_id" >>"$DIR/agent-researcher/logs/run.log"

  if (( step % 3 == 0 )); then
    write_atomic "$DIR/agent-researcher/memory/context.json" <<EOF
{
  "run_id": "$run_id",
  "updated_at": "$ts",
  "facts": [
    "source-$step ranked above source-$(( step - 1 ))",
    "the retrieval budget holds $(( STEPS_TOTAL - step )) more steps"
  ]
}
EOF
  fi

  # A finished run is archived once and never rewritten, so it declares no
  # heartbeat: the member is an undertaking to rewrite the document, and a
  # record of work that ended undertakes nothing.
  if (( step == STEPS_TOTAL )); then
    mkdir -p "$DIR/agent-researcher/runs/$run_id"
    write_atomic "$DIR/agent-researcher/runs/$run_id/state.json" <<EOF
{
  "schema": "$SCHEMA",
  "status": "done",
  "agent": "researcher",
  "task": "Retrieve and rank sources",
  "step": $STEPS_TOTAL,
  "steps_total": $STEPS_TOTAL,
  "model": "$MODEL",
  "run_id": "$run_id",
  "started_at": "$started",
  "updated_at": "$ts"
}
EOF
    write_atomic "$DIR/agent-researcher/runs/$run_id/output.json" <<EOF
{
  "run_id": "$run_id",
  "ranked": [
    "source-a",
    "source-b",
    "source-c"
  ]
}
EOF
    run_seq=$(( run_seq + 1 ))
  fi

  # writer — running for the first half of the researcher's task, idle for the
  # second. An idle agent holds no work, so it declares no task rather than an
  # empty one.
  if (( step <= STEPS_TOTAL / 2 )); then
    write_atomic "$DIR/agent-writer/state.json" <<EOF
{
  "schema": "$SCHEMA",
  "status": "running",
  "agent": "writer",
  "task": "Draft the summary",
  "step": "drafting",
  "model": "$MODEL",
  "heartbeat_seconds": $HEARTBEAT,
  "started_at": "$started",
  "updated_at": "$ts"
}
EOF
    printf '# Draft %s\n\nWritten at %s from %s.\n' \
      "$run_id" "$ts" "$run_id" >"$DIR/agent-writer/artifacts/draft-$run_id.md"
    printf '%s INFO  [writer] drafting from %s\n' "$ts" "$run_id" \
      >>"$DIR/agent-writer/logs/run.log"
  else
    write_atomic "$DIR/agent-writer/state.json" <<EOF
{
  "schema": "$SCHEMA",
  "status": "idle",
  "agent": "writer",
  "model": "$MODEL",
  "heartbeat_seconds": $HEARTBEAT,
  "started_at": "$started",
  "updated_at": "$ts"
}
EOF
  fi

  # reviewer — blocked. The block is an external input it is waiting on, not a
  # fault, so the document names the wait in its task and declares no problem.
  write_atomic "$DIR/agent-reviewer/state.json" <<EOF
{
  "schema": "$SCHEMA",
  "status": "blocked",
  "agent": "reviewer",
  "task": "Awaiting approval to publish the summary",
  "step": "approval",
  "model": "$MODEL",
  "heartbeat_seconds": $HEARTBEAT,
  "started_at": "$started",
  "updated_at": "$ts",
  "labels": {
    "approver": "on-call"
  }
}
EOF

  # indexer — done, carrying the problem it recovered from. Status is
  # independent of problem: an agent that finished after a transient failure
  # declares both, and a reader that derived one from the other could not
  # represent it.
  write_atomic "$DIR/agent-indexer/state.json" <<EOF
{
  "schema": "$SCHEMA",
  "status": "done",
  "agent": "indexer",
  "task": "Index the workspace tree",
  "step": $STEPS_TOTAL,
  "steps_total": $STEPS_TOTAL,
  "model": "$MODEL",
  "run_id": "run-index",
  "problem": "The first index pass timed out and was retried.",
  "heartbeat_seconds": $HEARTBEAT,
  "started_at": "$started",
  "updated_at": "$ts"
}
EOF

  # fetcher — error, carrying what stopped it. A status of error with no
  # problem leaves a reader nothing to act on, and agentfs reports that.
  write_atomic "$DIR/agent-fetcher/state.json" <<EOF
{
  "schema": "$SCHEMA",
  "status": "error",
  "agent": "fetcher",
  "task": "Fetch the upstream corpus",
  "step": 2,
  "steps_total": 5,
  "model": "$MODEL",
  "problem": "The upstream endpoint refused the credential after three attempts.",
  "heartbeat_seconds": $HEARTBEAT,
  "started_at": "$started",
  "updated_at": "$ts"
}
EOF
  printf '%s ERROR [fetcher] upstream refused the credential\n' "$ts" \
    >>"$DIR/agent-fetcher/logs/run.log"

  sleep "$TICK"
done
