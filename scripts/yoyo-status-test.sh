#!/usr/bin/env bash
#
# yoyo-status-test.sh - exercise bin/yoyo-status against a fabricated state
# directory holding both runs and conversations.
#
#   scripts/yoyo-status-test.sh
#
# The tool reads state a run or a conversation left behind, so it can be tested
# without a provider, a repository, or the harness: what it needs is event logs
# and state records in the shape the harness writes them, and this builds those.
# Everything lives under one temporary root that is removed on exit, and
# YOYODYNE_STATE_HOME points at it, so an operator's real state is never read.
#
# Requires bash and jq. The cost report needs jq and says so itself; the checks
# that need it are skipped rather than passed when it is absent.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
status="$repository/bin/yoyo-status"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/yoyo-status-test.XXXXXX")"
failures=0
skips=0

trap 'rm -rf "$scratch"' EXIT

export YOYODYNE_STATE_HOME="$scratch/state"
# Run from a directory with no .yoyodyne/config.yaml, so the product comes from
# the flag rather than from whatever checkout this was started in.
cd "$scratch"

step()   { printf '\n=== %s\n' "$*"; }
pass()   { printf '  ok: %s\n' "$*"; }
fail()   { printf '  FAIL: %s\n' "$*"; failures=$((failures + 1)); }
skip()   { printf '  SKIPPED: %s\n' "$*"; skips=$((skips + 1)); }

contains() {
  case "$1" in (*"$2"*) pass "$3" ;; (*) fail "$3 -- got: $1" ;; esac
}
missing() {
  case "$1" in (*"$2"*) fail "$3 -- got: $1" ;; (*) pass "$3" ;; esac
}

demo="$YOYODYNE_STATE_HOME/products/demo"
chatty="$YOYODYNE_STATE_HOME/products/chatty"
mkdir -p "$demo/runs" "$demo/conversations" "$chatty/conversations"

# One completed provider invocation, in the shape both a run and a conversation
# turn record it: the usage and the provider's own cost report.
completed() {
  local id="$1" sequence="$2" cost="$3"
  printf '{"schema_version":1,"run_id":"%s","sequence":%s,"timestamp":"2026-08-01T10:0%s:00.123456Z","type":"run.completed","source":"claude-code","payload":{"is_error":false,"total_cost_usd":%s,"usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":30,"cache_read_input_tokens":40}}}\n' \
    "$id" "$sequence" "$sequence" "$cost"
}
started() {
  local id="$1" sequence="$2"
  printf '{"schema_version":1,"run_id":"%s","sequence":%s,"timestamp":"2026-08-01T10:0%s:00.123456Z","type":"run.started","source":"claude-code","payload":{"model":"claude-opus-5"}}\n' \
    "$id" "$sequence" "$sequence"
}

run_state() {
  local id="$1" status="$2"
  cat > "$demo/runs/$id.json" <<JSON
{
  "schema_version": 1,
  "run_id": "$id",
  "status": "$status",
  "started_at": "2026-08-01T10:00:00.123456Z"
}
JSON
}

conversation_state() {
  local directory="$1" role="$2" id="$3"
  cat > "$directory/$role.json" <<JSON
{
  "schema_version": 1,
  "conversation_id": "$id",
  "product_id": "demo",
  "role": "$role",
  "turns": 2
}
JSON
}

one_run="run-1111111111111111111111111111111a"
other_run="run-2222222222222222222222222222222b"
waiting_chat="chat-3333333333333333333333333333333c"
ended_chat="chat-4444444444444444444444444444444d"
answering_chat="chat-5555555555555555555555555555555e"

{ started "$one_run" 1; completed "$one_run" 2 2.00; completed "$one_run" 3 1.00; } \
  > "$demo/runs/$one_run.events.jsonl"
run_state "$one_run" succeeded
{ started "$other_run" 1; } > "$demo/runs/$other_run.events.jsonl"
run_state "$other_run" running

{ started "$waiting_chat" 1; completed "$waiting_chat" 2 1.00
  started "$waiting_chat" 3; completed "$waiting_chat" 4 0.50; } \
  > "$demo/conversations/$waiting_chat.events.jsonl"
conversation_state "$demo/conversations" product-manager "$waiting_chat"

{ started "$ended_chat" 1; completed "$ended_chat" 2 0.25; } \
  > "$demo/conversations/$ended_chat.events.jsonl"

{ started "$answering_chat" 1; completed "$answering_chat" 2 0.50
  started "$answering_chat" 3; } \
  > "$demo/conversations/$answering_chat.events.jsonl"
conversation_state "$demo/conversations" architect "$answering_chat"

cp "$demo/conversations/$waiting_chat.events.jsonl" "$chatty/conversations/"
conversation_state "$chatty/conversations" product-manager "$waiting_chat"

# Newest last, so the listing order is decided rather than left to the order the
# files happened to be written in.
touch -t 202608010900 "$demo/runs/$one_run.events.jsonl"
touch -t 202608010910 "$demo/conversations/$ended_chat.events.jsonl"
touch -t 202608010920 "$demo/conversations/$waiting_chat.events.jsonl"
touch -t 202608010930 "$demo/conversations/$answering_chat.events.jsonl"
touch -t 202608010940 "$demo/runs/$other_run.events.jsonl"

step "listing covers runs and conversations together, newest first"
listing="$("$status" --product demo -l 2>&1)"
printf '%s\n' "$listing"
contains "$listing" "$one_run" "the listing includes a run"
contains "$listing" "$waiting_chat" "the listing includes a conversation"
contains "$(printf '%s\n' "$listing" | head -1)" "$other_run" "the newest of either kind is listed first"
contains "$(printf '%s\n' "$listing" | tail -1)" "$one_run" "the oldest of either kind is listed last"

step "a run's own listed status is what its state file says"
contains "$listing" "$one_run     succeeded" "a run reports the status it recorded"
contains "$listing" "$other_run     running" "a running run still reports running"

step "a conversation's status answers whether it is alive"
contains "$listing" "$answering_chat    answering" "a conversation with a turn in flight is answering"
contains "$listing" "$waiting_chat    waiting" "a conversation between turns is waiting"
contains "$listing" "$ended_chat    ended" "a conversation no role is in any more has ended"

step "either kind can be selected by id or by prefix"
selected="$("$status" --product demo --no-follow "$waiting_chat" 2>&1)"
contains "$selected" "==> $waiting_chat [waiting]" "a conversation is selected by its full id"
selected="$("$status" --product demo --no-follow 3333 2>&1)"
contains "$selected" "==> $waiting_chat [waiting]" "a conversation is selected by an id prefix"
contains "$selected" '"type":"run.completed"' "following a conversation prints its events"
selected="$("$status" --product demo --no-follow 1111 2>&1)"
contains "$selected" "==> $one_run [succeeded]" "a run is still selected by an id prefix"
selected="$("$status" --product demo --no-follow nosuchthing 2>&1 || true)"
contains "$selected" "no run or conversation matching 'nosuchthing'" "an id that matches neither is refused by name"

step "either kind can be asked for on its own"
runs_only="$("$status" --product demo -l --runs 2>&1)"
contains "$runs_only" "$one_run" "--runs lists runs"
missing "$runs_only" "chat-" "--runs lists no conversations"
chats_only="$("$status" --product demo -l --chats 2>&1)"
contains "$chats_only" "$waiting_chat" "--chats lists conversations"
missing "$chats_only" "run-" "--chats lists no runs"

step "a product that has only ever chatted still works"
chatty_listing="$("$status" --product chatty -l 2>&1)"
contains "$chatty_listing" "$waiting_chat" "a product with no runs directory lists its conversations"

step "cost reporting counts conversation turns in the total"
if ! command -v jq >/dev/null 2>&1; then
  cost="$("$status" --product demo -c 2>&1 || true)"
  contains "$cost" "needs jq" "cost reporting says it needs jq when jq is absent"
  skip "the cost totals: this environment has no jq"
else
  cost="$("$status" --product demo -c 2>&1 || true)"
  printf '%s\n' "$cost"
  case "$cost" in
    (*"mkstemp failed"*|*"Operation not permitted"*)
      skip "the cost totals: this environment denies the temporary file the report needs" ;;
    (*)
      # The run is $3.00 and the conversations are $2.25 between them, so a
      # report that left conversations out would say $3.00 and be wrong by the
      # whole of the chat spend.
      contains "$cost" "$one_run" "the report prices runs"
      contains "$cost" "$waiting_chat" "the report prices conversations"
      contains "$cost" "cost: \$5.25" "the total is runs and conversations together"
      contains "$cost" "runs: \$3.00 from 2 invocation(s)   conversations: \$2.25 from 4 turn(s)" \
        "a mixed total says how much of it was each"
      missing "$cost" "$other_run" "a run with no completed invocation is not priced"

      one="$("$status" --product demo -c "$waiting_chat" 2>&1)"
      contains "$one" "cost: \$1.50" "one conversation can be priced on its own"
      missing "$one" "runs: \$" "a total of one kind is not split"

      only_runs="$("$status" --product demo -c --runs 2>&1)"
      contains "$only_runs" "cost: \$3.00" "--runs prices runs alone"
      ;;
  esac
fi

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'yoyo-status reports runs and conversations as documented\n'
else
  printf '%d claim(s) did not hold\n' "$failures"
fi
if [ "$skips" != "0" ]; then
  printf '%d claim(s) this environment could not exercise, named above\n' "$skips"
fi
exit "$failures"
