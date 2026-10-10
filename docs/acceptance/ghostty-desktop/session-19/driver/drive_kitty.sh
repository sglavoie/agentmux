#!/bin/bash
# Runs on the MacBook. Starts a real Kitty of its own there (no configuration
# file, its own remote-control socket, its own instance group) and drives the
# AgentMux dashboard inside it through `kitten @`: first on the Kitty backend
# as it has always worked, then with the tmux backend hosted by the same
# Kitty. Keys are sent and screens are read through Kitty's remote control;
# nothing is typed on a keyboard and no other window is touched.
# usage: drive_kitty.sh LEGACY_LOG TMUX_LOG
R=/tmp/amxs19; . $R/driver/remote-env.sh
LEGACY=$1; TLOG=$2; LOG=$LEGACY; : > "$LEGACY"; : > "$TLOG"
note() { printf '\n##### %s\n' "$*" >> "$LOG"; echo "## $*"; }
shot() { printf '\n=== %s (kitty window %s) [%s] ===\n' "$2" "$1" "$(date -u +%H:%M:%SZ)" >> "$LOG"; K get-text --match id:$1 2>&1 | sed -e 's/[[:space:]]*$//' | cat -s >> "$LOG"; }
txt() { K send-text --match id:$1 "$2"; }
key() { K send-key --match id:$1 "$2"; }
waitfor() { # window text [seconds]
  local n=$(( ${3:-15} * 4 ))
  while [ $n -gt 0 ]; do K get-text --match id:$1 2>/dev/null | grep -qF -- "$2" && { printf 'PASS: on screen in window %s: %s\n' "$1" "$2" >> "$LOG"; return 0; }; sleep 0.25; n=$((n-1)); done
  printf '\nFAIL: TIMEOUT waiting in window %s for: %s\n' "$1" "$2" >> "$LOG"; echo "TIMEOUT: $2"; shot $1 "screen at timeout"; return 1
}
fail=0; passed=0; step() { if "$@"; then passed=$((passed+1)); else fail=$((fail+1)); fi; }
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then printf 'PASS: %s\n' "$what" >> "$LOG"; passed=$((passed+1)); else printf 'FAIL: %s\n' "$what" >> "$LOG"; echo "FAIL: $what"; fail=$((fail+1)); fi; }
ev() { eval "$1"; }
facts() { printf '\n--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }
panes() { priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} dead=#{pane_dead} #{pane_current_path}'; }
tree() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    print('tab %d%s title=%r' % (t['id'], ' [active]' if t['is_active'] else '', t['title']))
    for w in t['windows']:
      uv={k:v for k,v in w.get('user_vars',{}).items() if k in ('amx_profile','amx_project','amx_harness','amx_locked','cc_mute','amx_harness_session','amx_runtime_session_id')}
      fg=[' '.join(p['cmdline'][:3]) for p in w.get('foreground_processes',[])][:2]
      print('   window %d fg=%s vars=%s' % (w['id'], fg, uv))"; }
titles() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']: print(t['title'])"; }
tabcount() { titles | wc -l | tr -d ' '; }
managed_window() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    for w in t['windows']:
      if w.get('user_vars',{}).get('amx_project')=='$1': print(w['id'])" | tail -1; }
select_row() { # moves the dashboard cursor to the row named $1
  for i in 1 2 3; do K get-text --match id:$D | grep -qF "│$1" && return 0; txt $D "j"; sleep 0.6; done
  K get-text --match id:$D | grep -qF "│$1" || { txt $D "k"; sleep 0.5; txt $D "k"; sleep 0.6; }
  K get-text --match id:$D | grep -qF "│$1"
}
D=1  # the window the dashboard runs in

# --- A Kitty of this run's own --------------------------------------------------
rm -f $R/kitty.sock
nohup env -i HOME=$HOME TMPDIR=$TMPDIR PATH=$PATH LC_ALL=en_US.UTF-8 KITTY_CONFIG_DIRECTORY=$R/kittycfg \
  $KITTY/kitty --config NONE -o allow_remote_control=yes -o env=ZDOTDIR=$R/zdot -o confirm_os_window_close=0 \
  -o shell=/bin/zsh -o remember_window_size=no -o initial_window_width=150c -o initial_window_height=42c \
  --listen-on $KSOCK --instance-group amxs19 --directory $R > $R/logs/kitty.out 2>&1 </dev/null &
echo $! > $R/kitty.pid
n=80; while [ $n -gt 0 ] && ! K ls >/dev/null 2>&1; do sleep 0.25; n=$((n-1)); done
K ls >/dev/null 2>&1 || { echo "Kitty did not start: $(tail -5 $R/logs/kitty.out)"; printf 'FAIL: Kitty did not start\n%s\n' "$(cat $R/logs/kitty.out)" >> "$LOG"; exit 1; }
sleep 2

note "environment"
facts "the Mac" sh -c 'scutil --get ComputerName; sw_vers -productVersion; uname -m'
facts "kitty" $KITTY/kitty --version
facts "this run's Kitty process" sh -c "ps -o pid=,command= -p $(cat $R/kitty.pid) | cut -c1-200"
facts "amx build (the packaged amx)" amx --version
facts "kitty window tree before anything" tree
check "this Kitty is separate from any other: one process of its own, one tab" ev '[ "$(tabcount)" = 1 ]'
check "the private tmux server does not exist" ev '! priv list-panes -a'
txt $D $'echo "HOME=$HOME KITTY_WINDOW_ID=$KITTY_WINDOW_ID $(command -v amx)"\r'
step waitfor $D "HOME=$R/home KITTY_WINDOW_ID=1 $R/bin/amx"; shot $D "the shell inside Kitty is in the world"

note "L1 bare amx inside Kitty: dashboard on the Kitty backend, no sessions"
txt $D $'amx\r'; step waitfor $D "No running sessions"; shot $D "empty dashboard in Kitty"

note "L2 launch on the Kitty backend: a new Kitty tab, the dashboard quits and Kitty shows the tab"
txt $D "l"; step waitfor $D "Launch Sessions"; txt $D " "; sleep 0.3; txt $D $'\r'
sleep 5
A=$(managed_window alpha); echo "alpha window: $A"
facts "kitty window tree after launch" tree
step waitfor "$A" "stand-in claude pid"; shot "$A" "the launched agent in its own Kitty tab"
check "the launch made a second Kitty tab carrying the session's identity" ev '[ "$(tabcount)" = 2 ] && [ -n "$A" ]'
check "a Kitty launch started no tmux server" ev '! priv list-panes -a'
check "a Kitty launch registered no runtime session" ev '[ ! -e $DATA/runtime-sessions.json ]'

note "L3 legacy hook from the Kitty tab: the daemon renders the tab title"
txt "$A" $'ask\r'; step waitfor "$A" "asked (1)"; sleep 2.5
facts "kitty window tree after the PermissionRequest hook" tree
check "the daemon retitled the tab with the permission mark" ev 'titles | grep -q "🔑🟠alpha"'

note "L4 dashboard reopened in Kitty lists the Kitty session with daemon state"
K focus-tab --match id:1; sleep 0.5
txt $D $'amx\r'; step waitfor $D "Needs permission"; shot $D "dashboard: Kitty session needing permission"

note "L5 session actions on the Kitty row: clear, mark active, lock, mute"
txt $D "a"; step waitfor $D "Actions for alpha"; shot $D "actions menu for a Kitty session"
txt $D "x"; step waitfor $D "requested status clear"; sleep 2; facts "tree after Clear status" tree
check "Clear status: the tab title is the plain one" ev 'titles | grep -qx "🟠alpha"'
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "b"; step waitfor $D "Working"; sleep 1.5; facts "tree after Mark active" tree
check "Mark active: the tab title carries the working mark" ev 'titles | grep -qx "⚡🟠alpha"'
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "L"; sleep 2.5; facts "tree after Lock" tree
check "Lock: the tab title carries the lock" ev 'titles | grep -qx "🔒⚡🟠alpha"'
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "m"; sleep 2.5; facts "tree after Mute" tree
check "Mute: the tab title carries the mute mark" ev 'titles | grep -qx "🔒🔇⚡🟠alpha"'
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
check "Peek opened a Kitty overlay window running the pager" ev '[ -n "$P" ]'
[ -n "$P" ] && { shot "$P" "native overlay pager"; txt "$P" "q"; sleep 1; }
K focus-tab --match id:1; sleep 0.5

note "L7 restart replaces the Kitty tab's agent"
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "r"; step waitfor $D "restarted alpha" 25; sleep 2
A2=$(managed_window alpha); echo "alpha window after restart: $A2"
facts "tree after Restart (agent window was $A)" tree
check "Restart: another Kitty window now holds alpha, and there is still one alpha tab" ev '[ -n "$A2" ] && [ "$A2" != "$A" ] && [ "$(titles | grep -c alpha)" = 1 ]'

note "L8 focus (enter) brings the Kitty tab forward and the dashboard quits"
K focus-tab --match id:1; sleep 4
shot $D "dashboard after restart, row refreshed"
txt $D $'\r'; sleep 2.5
facts "tree after focus: the agent tab is active" tree
check "Enter made the agent's tab the active one" ev 'tree | grep -q "\[active\] title=.*alpha"'
shot $D "dashboard window after focus: back at the shell"

note "L9 overview inside Kitty"
K focus-tab --match id:1; sleep 0.5
txt $D $'amx overview\r'; step waitfor $D "Live sessions"; sleep 2; shot $D "overview in Kitty with one Kitty session"
txt $D "q"; sleep 1.5
printf '\n##### RESULT: %d passed, %d failed\n' "$passed" "$fail" >> "$LOG"; echo "legacy kitty: $passed passed, $fail failed"

LOG=$TLOG; fail=0; passed=0
note "environment"
facts "kitty" $KITTY/kitty --version
facts "amx build (the packaged amx)" amx --version
facts "kitty window tree (one Kitty-backed session, alpha, is still running)" tree
check "the private tmux server does not exist yet" ev '! priv list-panes -a'

note "T1 amx --backend tmux inside Kitty: the Kitty session is listed; a launch goes to tmux and the dashboard stays"
before=$(tabcount)
txt $D $'amx --backend tmux\r'; step waitfor $D "Running (1)"; shot $D "dashboard in Kitty before the tmux launch"
txt $D "l"; step waitfor $D "Launch Sessions"; txt $D "j"; sleep 0.3; txt $D " "; sleep 0.3; txt $D $'\r'
step waitfor $D "Launched 1/1" 20; step waitfor $D "beta"; sleep 2; shot $D "one list: alpha on Kitty and beta on tmux"
facts "private tmux server after launch" panes
facts "kitty window tree (no new Kitty tab for beta)" tree
facts "runtime-session registry" registry
check "beta runs in a pane of the private tmux server" ev '[ "$(panes | grep -c "dead=0.*proj/beta")" = 1 ]'
check "the tmux launch made no Kitty tab" ev '[ "$(tabcount)" = "$before" ]'
check "the registry holds one live tmux session, for beta" ev '[ "$(registry | grep -c " live .* tmux .*project=beta")" = 1 ]'
check "the server runs the packaged tmux, from the copy outside the app" ev 'ps -o command= -p "$(priv display-message -p "#{pid}")" | grep -q "$DATA/runtime/tmux-"'

note "T2 select the tmux row and focus it: attached inside the Kitty window; hook; F12 back"
step select_row beta; shot $D "tmux row selected: inspector reads the tmux pane"
txt $D $'\r'; step waitfor $D "conversation: standin-"; shot $D "attached to the tmux session inside the Kitty window"
facts "private server clients while attached" priv list-clients -F '#{client_session} #{client_termname}'
check "the Kitty window is the one terminal attached, as xterm-kitty" ev '[ "$(priv list-clients -F "#{client_termname}")" = xterm-kitty ]'
txt $D $'ask\r'; step waitfor $D "asked (1)"
key $D f12; step waitfor $D "Needs permission"; shot $D "back on the dashboard: beta (tmux) needs permission, alpha (Kitty) beside it"
check "no terminal is attached after F12" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 0 ]'

note "T3 actions follow the row's backend: peek of the tmux row is the in-terminal pager, not a Kitty overlay"
windows_before=$(tree | grep -c '   window')
txt $D "a"; step waitfor $D "Actions for beta"; txt $D "b"; step waitfor $D "Working"; sleep 1
txt $D "a"; step waitfor $D "Actions for beta"; txt $D "e"; step waitfor $D "(END)"; shot $D "peek of a tmux session from Kitty: less in this window"
facts "kitty window tree during the pager" tree
check "the pager of a tmux session opened no Kitty window" ev '[ "$(tree | grep -c "   window")" = "$windows_before" ]'
txt $D "q"; step waitfor $D "Sessions"

note "T4 overview with both backends"
txt $D "q"; sleep 1.5
txt $D $'amx overview --backend tmux\r'; step waitfor $D "Live sessions  2"; sleep 2.5; shot $D "overview in Kitty: a Kitty tile and a tmux tile"
check "the overview attached no terminal to the tmux session" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 0 ]'
txt $D "q"; sleep 1.5

note "T5 a plain amx inside Kitty lists tmux sessions once the private server exists; close each through its own backend"
txt $D $'amx\r'; step waitfor $D "beta"; sleep 1.5; shot $D "plain amx in Kitty: both backends listed"
step select_row beta
txt $D "a"; step waitfor $D "Actions for beta"; txt $D "X"; sleep 3.5; shot $D "after closing the tmux session"
facts "registry after close" registry
check "closing the tmux row ended its pane and left the Kitty tab alone" ev '! priv list-panes -a && [ "$(titles | grep -c alpha)" = 1 ]'
check "its runtime session is recorded as ended" ev '[ "$(registry | grep -c " live ")" = 0 ]'
sleep 5
txt $D "a"; step waitfor $D "Actions for alpha"; txt $D "X"; sleep 4; shot $D "after closing the Kitty session"
facts "kitty window tree after close (alpha tab gone)" tree
check "closing the Kitty row removed its tab" ev '[ "$(titles | grep -c alpha)" = 0 ]'
txt $D "q"; sleep 1
printf '\n##### RESULT: %d passed, %d failed\n' "$passed" "$fail" >> "$LOG"; echo "tmux in kitty: $passed passed, $fail failed"
