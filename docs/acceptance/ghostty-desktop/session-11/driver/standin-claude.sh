#!/bin/sh
# Stand-in harness for AgentMux session-11 acceptance: no model, no network.
case "$1" in --version) echo "2.1.296 (stand-in)"; exit 0;; esac
plan=""; prev=""
for a in "$@"; do
  case "$prev" in --add-dir) plan="$a";; esac
  prev="$a"
done
if [ -n "$plan" ] && [ -f "$plan/session-1.md" ]; then
  # One worker turn for `asl run`: hold the turn open, then complete the session.
  echo "worker turn for $plan (runtime session ${AMX_RUNTIME_SESSION_ID:-none})"
  sleep "${STANDIN_TURN_SECONDS:-45}"
  perl -pi -e 's/\*\*Status\*\*: pending/**Status**: complete/' "$plan/session-1.md"
  echo "Runner status: complete"
  exit 0
fi
sid="standin-$$"; prev=""
for a in "$@"; do
  case "$prev" in --resume|-r) sid="$a";; esac
  prev="$a"
done
case " $* " in *" --fork-session "*) sid="fork-$$";; esac
echo "stand-in claude pid $$ in $(pwd)"
echo "runtime session: ${AMX_RUNTIME_SESSION_ID:-none}  kitty window: ${KITTY_WINDOW_ID:-none}"
echo "conversation: $sid"
echo "args: $*"
hook() { printf '{"session_id":"%s","hook_event_name":"%s","cwd":"%s"%s}' "$sid" "$1" "$(pwd)" "$2" | amx hook-indicator --harness claude "$1" >/dev/null 2>&1; }
hook SessionStart ',"source":"startup"'
i=0
while IFS= read -r line; do
  i=$((i+1))
  case "$line" in
    ask) hook PermissionRequest ',"tool_name":"Bash"'; echo "asked ($i)";;
    work) hook UserPromptSubmit ',"prompt":"x"'; echo "working ($i)";;
    asl) asl run /tmp/amxs11/aslplan --cwd /tmp/amxs11/aslrepo --harness claude --no-supervisor-audit --allow-not-loop-ready --max-sessions 1 --timeout 300 > /tmp/amxs11/aslrun.log 2>&1 &
         echo "asl run started ($i)";;
    quit) exit 0;;
    *) echo "echo[$i]: $line";;
  esac
done
