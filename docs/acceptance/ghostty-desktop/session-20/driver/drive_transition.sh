#!/bin/bash
# Runs on the MacBook, in the Session 19 world. Rehearses what
# docs/daily-driver-trial.md tells the operator to do: move new launches from
# Kitty to the desktop workflow while a Kitty session is running, and then
# roll back. It is a rehearsal of the instructions with stand-in agents, not
# the trial.
#
# The world is given what the operator's Mac has: an amx outside the app
# (~/.local/bin/amx, here a copy of the bundle's) beside the installed app.
# Launches are requested with `amx project launch`; Kitty is reached through
# its remote-control socket. No key, pointer, or accessibility grant is used.
# usage: drive_transition.sh LOG
R=/tmp/amxs19; . $R/driver/remote-env.sh
LOG=$1; : > "$LOG"
CFG=$HOME/.config/agent-mux/config.toml
note() { printf '\n##### %s [%s]\n' "$*" "$(date -u +%H:%M:%SZ)" >> "$LOG"; echo "## $*"; }
fail=0; passed=0
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then printf 'PASS: %s\n' "$what" >> "$LOG"; passed=$((passed+1)); else printf 'FAIL: %s\n' "$what" >> "$LOG"; echo "FAIL: $what"; fail=$((fail+1)); fi; }
wcheck() { local what=$1 n=$(( $2 * 4 )); shift 2; while [ $n -gt 0 ]; do if eval "$1" >/dev/null 2>&1; then printf 'PASS: %s\n' "$what" >> "$LOG"; passed=$((passed+1)); return 0; fi; sleep 0.25; n=$((n-1)); done; printf 'FAIL: %s (not within the wait)\n' "$what" >> "$LOG"; echo "FAIL: $what"; fail=$((fail+1)); }
ev() { eval "$1"; }
facts() { printf -- '--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }
run() { printf -- '--- $ %s ---\n' "$1" >> "$LOG"; eval "$2" >> "$LOG" 2>&1; printf '(exit %s)\n' "$?" >> "$LOG"; }

apppid() { pgrep -f "$DEST/Contents/MacOS/Agent Mux" | head -1; }
app_start() {
  date '+%Y-%m-%d %H:%M:%S' > $R/app.started
  open -n "$DEST" --env HOME="$HOME" --env TMPDIR="$TMPDIR" --env PATH="$PATH"
}
applog() { /usr/bin/log show --start "$(cat $R/app.started)" --style compact --predicate "subsystem == \"io.github.sglavoie.agentmux\" AND processIdentifier == $(apppid)" 2>/dev/null | sed -E -e '1d' -e 's/^[0-9-]+ ([0-9:.]+) +[A-Za-z]+ +Agent Mux\[[0-9:a-f]+\] /\1 /'; }
applines() { applog | grep -E 'backend:|feed:|running ' | head -12; }
uninstall_app() { AMX_APP_INSTALL_DIR="$INSTALL" "$COPY/macos/scripts/uninstall-app.sh" "$@" </dev/null; }
panes() { priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} dead=#{pane_dead} #{pane_current_path}' 2>&1; }
clients() { priv list-clients -F 'client_pid=#{client_pid} term=#{client_termname} pane=#{pane_id}' 2>/dev/null | sort; }
rsof() { registry | awk -v p="project=$1" '$2=="live" && $6==p {print $1}' | tail -1; }
pidof() { registry | awk -v p="project=$1" '$2=="live" && $6==p {print $7}' | tail -1 | cut -d= -f2; }
proc() { LC_ALL=C TZ=UTC ps -o pid=,lstart= -p "$1" 2>/dev/null | sed -e 's/^ *//' -e 's/  */ /g'; }
tree() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    print('tab %d title=%r' % (t['id'], t['title']))
    for w in t['windows']:
      uv={k:v for k,v in w.get('user_vars',{}).items() if k in ('amx_profile','amx_project','amx_harness','amx_runtime_session_id')}
      print('   window %d pid=%s vars=%s' % (w['id'], w.get('pid'), uv))"; }
titles() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']: print(t['title'])"; }
# kwin PROJECT: 'window-id pid' of every Kitty window that carries the project.
kwin() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    for w in t['windows']:
      if w.get('user_vars',{}).get('amx_project')=='$1': print(w['id'], w.get('pid'))"; }
ktext() { K get-text --match id:$1 2>/dev/null; }
klaunch() { KITTY_LISTEN_ON=$KSOCK amx project launch "$@"; } # amx outside Kitty, pointed at the world's Kitty
backend_line() { grep -n '^launch_backend' "$CFG" || echo '(no launch_backend line: the default, kitty)'; }

# --- The operator's layout: an amx outside the app ------------------------------
mkdir -p $HOME/.local/bin
cp "$AMX" $HOME/.local/bin/amx
printf '#!/bin/sh\nexec "%s" "$@"\n' "$HOME/.local/bin/amx" > $R/bin/amx; chmod +x $R/bin/amx
SA=$HOME/.local/bin/amx
# The driver looks at the private server with the runtime copy outside the
# app, which is still there once the app is removed.
TM=$(ls -d $DATA/runtime/tmux-*/bin/tmux | head -1)

# --- A Kitty of this run's own, as in the Session 19 driver ---------------------
rm -f $R/kitty.sock
nohup env -i HOME=$HOME TMPDIR=$TMPDIR PATH=$PATH LC_ALL=en_US.UTF-8 KITTY_CONFIG_DIRECTORY=$R/kittycfg \
  $KITTY/kitty --config NONE -o allow_remote_control=yes -o env=ZDOTDIR=$R/zdot -o confirm_os_window_close=0 \
  -o shell=/bin/zsh -o remember_window_size=no -o initial_window_width=150c -o initial_window_height=42c \
  --listen-on $KSOCK --instance-group amxs19 --directory $R > $R/logs/kitty.out 2>&1 </dev/null &
echo $! > $R/kitty.pid
n=80; while [ $n -gt 0 ] && ! K ls >/dev/null 2>&1; do sleep 0.25; n=$((n-1)); done
K ls >/dev/null 2>&1 || { printf 'FAIL: Kitty did not start\n%s\n' "$(cat $R/logs/kitty.out)" >> "$LOG"; echo "Kitty did not start"; exit 1; }
sleep 2

amx daemon stop >/dev/null 2>&1; sleep 1
nohup amx daemon start >> $R/logs/daemon.out 2>&1 </dev/null &
sleep 2

note "0 the candidate and the world before the transition"
facts "the Mac" sh -c 'scutil --get ComputerName; sw_vers -productVersion; uname -m'
facts "the installed app" sh -c "echo '$DEST'; /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' '$DEST/Contents/Info.plist'; codesign -dv '$DEST' 2>&1 | grep -E 'Signature|TeamIdentifier'"
facts "amx outside the app ($SA)" amx --version
facts "amx inside the app" "$AMX" --version
facts "kitty" $KITTY/kitty --version
facts "launch backend in config.toml" backend_line
check "the installed bundle passes strict signature verification" codesign --verify --strict --deep "$DEST"
check "the amx outside the app is the same build as the one inside it" cmp -s "$SA" "$AMX"
check "no launch backend is configured: launches go to Kitty" ev '! grep -q "^launch_backend" "$CFG"'
check "no private tmux server runs" ev '! priv list-panes -a'

note "1 before the transition: a Kitty session, launched the way it always was"
run "amx project launch alpha --profile work --json   (KITTY_LISTEN_ON names the world's Kitty)" 'klaunch alpha --profile work --json'
wcheck "alpha runs in a Kitty tab of its own" 15 '[ "$(kwin alpha | wc -l | tr -d " ")" = 1 ]'
AW=$(kwin alpha | cut -d' ' -f1); AP=$(kwin alpha | cut -d' ' -f2)
wcheck "the agent in that tab is up" 15 'ktext $AW | grep -q "stand-in claude pid"'
alpha_before=$(proc "$AP"); echo "alpha: kitty window $AW, process $alpha_before" >> "$LOG"
facts "kitty window tree" tree
check "the default launch started no tmux server and registered no runtime session" ev '! priv list-panes -a && [ ! -e $DATA/runtime-sessions.json ]'
K send-text --match id:$AW $'work\r'
wcheck "its hook reaches the daemon: the tab is marked working" 15 'titles | grep -q "⚡🟠alpha"'

note "2 the transition: launch_backend = \"tmux\", and the app as the workspace"
run "amx backend status --check" 'amx backend status --check'
check "amx backend status --check passes before the switch" amx backend status --check
perl -0pi -e 's/^\[defaults\]\n/[defaults]\nlaunch_backend = "tmux"\n/m' "$CFG"
facts "launch backend in config.toml" backend_line
check "config.toml validates" amx config validate
run "amx backend status" 'amx backend status'
check "amx backend status names tmux as the launch backend and passes --check" ev 'amx backend status | grep -qi "launch backend.*tmux" && amx backend status --check'
mkdir -p $R/shim; install -m 0755 $R/driver/amx-shim.sh $R/shim/amx
defaults write $BUNDLE amxBinaryPath -string $R/shim/amx
defaults write $BUNDLE amxUseAppNotifications -bool false
defaults write $BUNDLE amxEnvironment -dict PATH "$PATH"
defaults write $BUNDLE amxOpenWorkspaceAtLaunch -bool true
defaults write $BUNDLE amxLaunchBackend -string workspace
app_start
wcheck "the app runs from the install root" 20 '[ -n "$(apppid)" ]'
wcheck "the app found its bundled tmux and its feed is live" 40 'applog | grep -q "backend: tmux 3.7c" && applog | grep -q "feed: live"'
facts "the app's log (backend and feed lines)" applines

note "3 after the transition: new launches go to tmux; the Kitty session carries on where it is"
run "amx project launch beta --profile work --json   (no --backend)" 'amx project launch beta --profile work --json'
wcheck "beta runs in a pane of the private tmux server" 15 '[ "$(panes | grep -c "dead=0.*proj/beta")" = 1 ]'
wcheck "the registry holds one live tmux session, for beta" 15 '[ "$(registry | grep -c " live .* tmux .*project=beta")" = 1 ]'
B=$(rsof beta); BP=$(pidof beta); BPANE=$(registry | awk '$2=="live" && $6=="project=beta" {print $5}'); beta_before=$(proc "$BP"); server_before=$(priv display-message -p '#{pid}')
echo "beta: runtime session $B, process $beta_before; server pid $server_before" >> "$LOG"
facts "registry" registry
facts "private tmux server" panes
facts "workspace_view session beta (the request the Dashboard sends when a row is chosen)" python3 $R/driver/dview.py session "$B"
wcheck "the app shows beta: one terminal of its own is attached to it" 30 '[ "$(clients | wc -l | tr -d " ")" = 1 ]'
facts "terminals attached to the private server" clients
check "the launch made no Kitty tab" ev '[ "$(titles | grep -c beta)" = 0 ]'
check "the Kitty session is the same process in the same tab" ev '[ "$(proc "$AP")" = "$alpha_before" ] && [ "$(kwin alpha | cut -d" " -f1)" = "$AW" ]'
K send-text --match id:$AW $'ask\r'
wcheck "the Kitty session's hooks still reach the daemon: the tab is marked for permission" 15 'titles | grep -q "🔑🟠alpha"'
check "amx attach --json finds the tmux session live" ev 'amx attach "$B" --json | grep -q "\"state\": *\"live\""'
run "amx attach $B --json" 'amx attach "$B" --json'
run "amx project launch beta --profile work --backend kitty --json   (Kitty named explicitly while tmux is the default)" 'klaunch beta --profile work --backend kitty --json'
wcheck "Kitty stays available while tmux is the default: beta also runs in a Kitty tab" 15 '[ "$(kwin beta | wc -l | tr -d " ")" = 1 ]'
check "that launch added no tmux pane" ev '[ "$(panes | grep -c "dead=0")" = 1 ]'
KB=$(kwin beta | cut -d' ' -f1)
facts "kitty window tree" tree

note "4 the Kitty session finishes in place"
K send-text --match id:$AW $'quit\r'
wcheck "the agent ended by itself and its Kitty tab is gone" 20 '[ -z "$(kwin alpha)" ]'
K send-text --match id:$KB $'quit\r'
wcheck "so did the one launched on Kitty after the transition" 20 '[ -z "$(kwin beta)" ]'
check "the tmux session was not touched by that" ev '[ "$(proc "$BP")" = "$beta_before" ]'

note "5 rollback, step 1: launches back to Kitty; the tmux agent keeps running"
perl -ni -e 'print unless /^launch_backend = "tmux"$/' "$CFG"
defaults write $BUNDLE amxLaunchBackend -string kitty
facts "launch backend in config.toml" backend_line
check "config.toml validates" amx config validate
run "amx project launch alpha --profile work --json   (no --backend)" 'klaunch alpha --profile work --json'
wcheck "alpha is launched in a Kitty tab again" 15 '[ "$(kwin alpha | wc -l | tr -d " ")" = 1 ]'
AW2=$(kwin alpha | cut -d' ' -f1)
wcheck "the agent in that tab is up" 15 'ktext $AW2 | grep -q "stand-in claude pid"'
check "the Kitty launch added no tmux pane and no runtime session" ev '[ "$(panes | grep -c "dead=0")" = 1 ] && [ "$(registry | grep -c " live ")" = 1 ]'
K send-text --match id:$AW2 $'ask\r'
wcheck "its hook reaches the daemon: the tab is marked for permission" 15 'titles | grep -q "🔑🟠alpha"'
check "the tmux agent is the same process on the same server" ev '[ "$(proc "$BP")" = "$beta_before" ] && [ "$(priv display-message -p "#{pid}")" = "$server_before" ]'

note "6 rollback, step 2: remove the app; the tmux agent keeps running and stays reachable"
run "uninstall-app.sh" 'uninstall_app'
check "the app is gone and no process of it is left" ev '[ ! -e "$DEST" ] && [ -z "$(apppid)" ]'
check "the tmux agent is the same process on the same server" ev '[ "$(proc "$BP")" = "$beta_before" ] && [ "$(priv display-message -p "#{pid}")" = "$server_before" ]'
check "the app's terminal is detached" ev '[ -z "$(clients)" ]'
check "the amx outside the app still finds the session live" ev 'amx attach "$B" --json | grep -q "\"state\": *\"live\""'
run "amx backend status   (the amx outside the app, the app gone)" 'amx backend status'
check "the Kitty session is still there" ev '[ "$(kwin alpha | cut -d" " -f1)" = "$AW2" ]'
priv send-keys -t "$BPANE" 'still here' Enter
wcheck "the tmux agent still answers its terminal" 10 'priv capture-pane -p -t "$BPANE" | grep -q "echo\[[0-9]*\]: still here"'
run "amx project launch alpha --profile work --backend tmux --json   (tmux named explicitly, the app gone)" 'amx project launch alpha --profile work --backend tmux --json'
wcheck "tmux remains usable without the app: a second agent runs on the same server" 15 '[ "$(panes | grep -c "dead=0")" = 2 ] && [ "$(priv display-message -p "#{pid}")" = "$server_before" ]'

note "7 rollback, step 3: end the tmux agents explicitly; Kitty is all that is left"
run "amx backend stop --yes" 'amx backend stop --yes'
wcheck "the private server and its agent are gone" 15 '! priv list-panes -a && ! kill -0 "$BP"'
wcheck "the registry no longer holds a live session" 20 '[ "$(registry | grep -c " live ")" = 0 ]'
facts "registry" registry
check "the Kitty session is untouched" ev '[ "$(kwin alpha | cut -d" " -f1)" = "$AW2" ]'
K send-text --match id:$AW2 $'work\r'
wcheck "and its hooks still reach the daemon" 15 'titles | grep -q "⚡🟠alpha"'
facts "kitty window tree" tree
check "config.toml is as it was before the transition" ev '! grep -q "^launch_backend" "$CFG" && amx config validate'
K send-text --match id:$AW2 $'quit\r'; sleep 2

printf '\n##### RESULT: %d passed, %d failed\n' "$passed" "$fail" >> "$LOG"; echo "transition and rollback: $passed passed, $fail failed"
