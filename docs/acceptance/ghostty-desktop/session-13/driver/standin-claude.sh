#!/bin/sh
# Stand-in harness for AgentMux session-13 acceptance: no model, no network.
# Interactive, it prints its identity, sends real hooks through `amx
# hook-indicator` on typed commands, and echoes anything else. As an `asl run`
# worker (--add-dir PLAN) it asks one mailbox question, waits for the
# supervisor's reply, and completes its session.
case "$1" in --version) echo "2.1.296 (stand-in)"; exit 0;; esac
plan=""; prev=""
for a in "$@"; do
  case "$prev" in --add-dir) plan="$a";; esac
  prev="$a"
done
if [ -n "$plan" ] && [ -f "$plan/session-1.md" ]; then
  echo "worker turn for $plan (runtime session ${AMX_RUNTIME_SESSION_ID:-none})" >> /tmp/amxs13/logs/worker.log
  asl ask "$plan" "Proceed with the fixture change?" --wait --wait-timeout 600 --json > /tmp/amxs13/logs/ask.json 2> /tmp/amxs13/logs/ask.err
  echo "ask returned $?" >> /tmp/amxs13/logs/worker.log
  perl -pi -e 's/\*\*Status\*\*: pending/**Status**: complete/' "$plan/session-1.md"
  echo "Runner status: complete"
  exit 0
fi
sid="standin-$$"
echo "stand-in claude pid $$ in $(pwd)"
echo "runtime session: ${AMX_RUNTIME_SESSION_ID:-none}  kitty window: ${KITTY_WINDOW_ID:-none}"
hook() { printf '{"session_id":"%s","hook_event_name":"%s","cwd":"%s"%s}' "$sid" "$1" "$(pwd)" "$2" | amx hook-indicator --harness claude "$1" >/dev/null 2>&1; }
hook SessionStart ',"source":"startup"'
i=0
while IFS= read -r line; do
  i=$((i+1))
  case "$line" in
    ask) hook PermissionRequest ',"tool_name":"Bash"'; echo "asked ($i)";;
    work) hook UserPromptSubmit ',"prompt":"x"'; echo "working ($i)";;
    asl) asl run /tmp/amxs13/plans/demo --cwd /tmp/amxs13/aslrepo --harness claude --no-supervisor-audit --allow-not-loop-ready --max-sessions 1 --timeout 900 > /tmp/amxs13/logs/aslrun.log 2>&1 &
         echo "asl run started ($i)";;
    quit) exit 0;;
    *) echo "echo[$i]: $line";;
  esac
done
