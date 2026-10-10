#!/bin/sh
# Stand-in harness: no model, no network. Installed as the world's `claude`.
# It prints who it is, reports itself to the daemon with real hooks through
# `amx hook-indicator`, and then reads commands from its terminal: `ask` sends
# a PermissionRequest hook, `work` a UserPromptSubmit hook, `quit` ends it,
# and anything else is echoed.
case "$1" in --version) echo "2.1.296 (stand-in)"; exit 0;; esac
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
    quit) exit 0;;
    *) echo "echo[$i]: $line";;
  esac
done
