#!/bin/bash
# Drives the AgentMux dashboard inside a real, separate Kitty instance through
# remote control: first the legacy Kitty backend, then the tmux backend hosted
# by the same Kitty. usage: drive_kitty.sh LEGACY_LOG TMUX_LOG
R=/tmp/amxs11; LOG=$1; TLOG=$2
. $R/env.sh
K() { /Applications/kitty.app/Contents/MacOS/kitten @ --to unix:$R/kitty.sock "$@"; }
priv() { tmux -S $R/tmp/agent-mux-$(id -u)/tmux.sock "$@"; }
note() { printf '\n##### %s\n' "$*" >> "$LOG"; echo "## $*"; }
shot() { printf '\n=== %s (kitty window %s) [%s] ===\n' "$2" "$1" "$(date -u +%H:%M:%SZ)" >> "$LOG"; K get-text --match id:$1 2>&1 | sed -e 's/[[:space:]]*$//' | cat -s >> "$LOG"; }
txt() { K send-text --match id:$1 "$2"; }
key() { K send-key --match id:$1 "$2"; }
waitfor() { # window text [seconds]
  local n=$(( ${3:-15} * 4 ))
  while [ $n -gt 0 ]; do K get-text --match id:$1 2>/dev/null | grep -qF -- "$2" && return 0; sleep 0.25; n=$((n-1)); done
  printf '\n!!! TIMEOUT waiting in window %s for: %s\n' "$1" "$2" >> "$LOG"; echo "TIMEOUT: $2"; shot $1 "screen at timeout"; return 1
}
fail=0; step() { "$@" || fail=$((fail+1)); }
facts() { printf '\n--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }
tree() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    print('tab %d%s title=%r' % (t['id'], ' [active]' if t['is_active'] else '', t['title']))
    for w in t['windows']:
      uv={k:v for k,v in w.get('user_vars',{}).items() if k in ('amx_profile','amx_project','amx_harness','amx_locked','cc_mute','amx_harness_session','amx_runtime_session_id')}
      fg=[' '.join(p['cmdline'][:3]) for p in w.get('foreground_processes',[])][:2]
      print('   window %d overlay=%s fg=%s vars=%s' % (w['id'], w.get('is_self') or bool(w.get('overlay_for')), fg, uv))"; }
managed_window() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    for w in t['windows']:
      if w.get('user_vars',{}).get('amx_project')=='$1': print(w['id'])" | tail -1; }
D=1  # the window the dashboard runs in

note "environment"
facts "kitty" /Applications/kitty.app/Contents/MacOS/kitty --version
facts "amx build" amx --version
facts "kitty window tree before anything" tree
facts "private tmux server (expect none)" priv list-panes -a

note "L1 bare amx inside Kitty: dashboard on the Kitty backend, no sessions"
txt $D $'amx\r'; step waitfor $D "No running sessions"; shot $D "empty dashboard in Kitty"

note "L2 launch on the Kitty backend: a new Kitty tab, the dashboard quits and Kitty shows the tab"
txt $D "l"; step waitfor $D "Launch Sessions"; txt $D " "; sleep 0.3; txt $D $'\r'
sleep 5
A=$(managed_window alpha); echo "alpha window: $A"
facts "kitty window tree after launch" tree
step waitfor "$A" "stand-in claude pid"; shot "$A" "the launched agent in its own Kitty tab"
facts "private tmux server (expect none: Kitty launch)" priv list-panes -a
facts "runtime-session registry (expect absent: Kitty launches do not register)" ls $R/home/.local/share/agent-mux/runtime-sessions.json

note "L3 legacy hook from the Kitty tab: the daemon renders the tab title"
txt "$A" $'ask\r'; step waitfor "$A" "asked (1)"; sleep 2.5
facts "kitty window tree after the PermissionRequest hook" tree

note "L4 dashboard reopened in Kitty lists the Kitty session with daemon state"
K focus-tab --match id:1; sleep 0.5
txt $D $'amx\r'; step waitfor $D "Needs permission"; shot $D "dashboard: Kitty session needing permission"

note "L5 session actions on the Kitty row: clear, mark active, lock, mute"
txt $D "a"; step waitfor $D "Actions for alpha"; shot $D "actions menu for a Kitty session"
txt $D "x"; step waitfor $D "requested status clear"; sleep 2; facts "tree after Clear status" tree
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "b"; step waitfor $D "Working"; sleep 1.5; facts "tree after Mark active" tree
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "L"; sleep 2.5; facts "tree after Lock" tree
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "m"; sleep 2.5; facts "tree after Mute" tree
shot $D "dashboard after lock + mute"
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "L"; sleep 2.5
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "m"; sleep 2.5

note "L6 peek keeps the native Kitty overlay pager"
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "e"; sleep 3
facts "tree with the overlay pager (less) over the agent window" tree
P=$(K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    for w in t['windows']:
      if any('less' in ' '.join(p['cmdline']) for p in w.get('foreground_processes',[])): print(w['id'])" | tail -1)
echo "pager window: $P"
[ -n "$P" ] && { shot "$P" "native overlay pager"; txt "$P" "q"; sleep 1; } || { fail=$((fail+1)); echo "NO PAGER OVERLAY" >> "$LOG"; }
K focus-tab --match id:1; sleep 0.5

note "L7 restart replaces the Kitty tab's agent"
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "r"; step waitfor $D "restarted alpha" 25; sleep 2
A2=$(managed_window alpha); echo "alpha window after restart: $A2"
facts "tree after Restart (agent window was $A)" tree

note "L8 focus (enter) brings the Kitty tab forward and the dashboard quits"
K focus-tab --match id:1; sleep 4
shot $D "dashboard after restart, row refreshed"
txt $D $'\r'; sleep 2.5
facts "tree after focus: the agent tab is active" tree
shot $D "dashboard window after focus: back at the shell"

note "L9 overview inside Kitty"
K focus-tab --match id:1; sleep 0.5
txt $D $'amx overview\r'; step waitfor $D "Live sessions"; sleep 2; shot $D "overview in Kitty with one Kitty session"
txt $D "q"; sleep 1.5

LEG=$fail
LOG=$TLOG; fail=0
note "environment"
facts "kitty" /Applications/kitty.app/Contents/MacOS/kitty --version
facts "amx build" amx --version
facts "kitty window tree (one Kitty-backed session, alpha, is still running)" tree
facts "private tmux server before (expect none)" priv list-panes -a

note "T1 amx --backend tmux inside Kitty: the Kitty session is listed; a launch goes to tmux and the dashboard stays"
txt $D $'amx --backend tmux\r'; step waitfor $D "Running (1)"; shot $D "dashboard in Kitty before the tmux launch"
txt $D "l"; step waitfor $D "Launch Sessions"; txt $D "j"; sleep 0.3; txt $D " "; sleep 0.3; txt $D $'\r'
step waitfor $D "Launched 1/1" 20; step waitfor $D "beta"; sleep 2; shot $D "one list: alpha on Kitty and beta on tmux"
facts "private tmux server after launch" priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} dead=#{pane_dead} #{pane_current_path}'
facts "kitty window tree (no new Kitty tab for beta)" tree
facts "runtime-session registry" python3 -c "
import json;d=json.load(open('$R/home/.local/share/agent-mux/runtime-sessions.json'))
for s in d['sessions']: print(s['id'],s['state'],s['relation'],s['target']['instance']['kind'],s['target']['ref'])"

note "T2 select the tmux row and focus it: attached inside the Kitty window; hook; F12 back"
for i in 1 2 3; do K get-text --match id:$D | grep -qF "│beta" && break; txt $D "j"; sleep 0.6; done
K get-text --match id:$D | grep -qF "│beta" || { txt $D "k"; sleep 0.5; txt $D "k"; sleep 0.6; }
step waitfor $D "│beta" 4; shot $D "tmux row selected: inspector reads the tmux pane"
txt $D $'\r'; step waitfor $D "conversation: standin-"; shot $D "attached to the tmux session inside the Kitty window"
facts "private server clients while attached" priv list-clients -F '#{client_session} #{client_termname}'
txt $D $'ask\r'; step waitfor $D "asked (1)"
key $D f12; step waitfor $D "Needs permission"; shot $D "back on the dashboard: beta (tmux) needs permission, alpha (Kitty) beside it"
facts "private server clients after detach (expect none)" priv list-clients -F '#{client_session}'

note "T3 actions follow the row's backend: peek of the tmux row is the in-terminal pager, not a Kitty overlay"
txt $D "a"; step waitfor $D "Actions for beta"; txt $D "b"; step waitfor $D "Working"; sleep 1
txt $D "a"; step waitfor $D "Actions for beta"; txt $D "e"; step waitfor $D "(END)"; shot $D "peek of a tmux session from Kitty: less in this window"
facts "kitty window tree during the pager (no overlay window was created)" tree
txt $D "q"; step waitfor $D "Sessions"

note "T4 overview with both backends"
txt $D "q"; sleep 1.5
txt $D $'amx overview --backend tmux\r'; step waitfor $D "Live sessions  2"; sleep 2.5; shot $D "overview in Kitty: a Kitty tile and a tmux tile"
txt $D "q"; sleep 1.5

note "T5 a plain amx inside Kitty lists tmux sessions once the private server exists; close each through its own backend"
txt $D $'amx\r'; step waitfor $D "beta"; sleep 1.5; shot $D "plain amx in Kitty: both backends listed"
for i in 1 2 3; do K get-text --match id:$D | grep -qF "│beta" && break; txt $D "j"; sleep 0.6; done
K get-text --match id:$D | grep -qF "│beta" || { txt $D "k"; sleep 0.5; txt $D "k"; sleep 0.6; }
step waitfor $D "│beta" 4
txt $D "a"; step waitfor $D "Actions for beta"; txt $D "X"; sleep 3.5; shot $D "after closing the tmux session"
facts "private tmux server after close (expect none)" priv list-panes -a
facts "registry after close" python3 -c "
import json;d=json.load(open('$R/home/.local/share/agent-mux/runtime-sessions.json'))
for s in d['sessions']: print(s['id'],s['state'])"
sleep 5
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "X"; sleep 4; shot $D "after closing the Kitty session"
facts "kitty window tree after close (alpha tab gone)" tree
txt $D "q"; sleep 1
printf '\n##### RESULT: %d step(s) failed\n' "$fail" >> "$LOG"; echo "tmux-in-kitty failed steps: $fail"
printf '\n##### RESULT: %d step(s) failed\n' "$LEG" >> "$1"; echo "legacy failed steps: $LEG"
