#!/bin/bash
# Runs on the MacBook. Exercises the packaged Release Agent Mux.app, installed
# in the world's install root by the repository's installer, as a workspace:
# its embedded Ghostty terminals on agents the private tmux server holds, the
# Overview and Dashboard views, and what quitting and killing the app leave
# behind.
#
# No input is synthesized: no key, no pointer, no accessibility grant. The app
# is moved by the same workspace_view request the Dashboard sends (dview.py),
# quit by an Apple event, and killed by a signal; everything is read from the
# registry, the private tmux server, the daemon's snapshot, the process table,
# the app's saved preferences, and the app's unified log. The window is on
# the MacBook's screen and is never looked at as pixels.
# usage: drive_packaged.sh LOG
R=/tmp/amxs19; . $R/driver/remote-env.sh
LOG=$1; : > "$LOG"
note() { printf '\n##### %s [%s]\n' "$*" "$(date -u +%H:%M:%SZ)" >> "$LOG"; echo "## $*"; }
fail=0; passed=0
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then printf 'PASS: %s\n' "$what" >> "$LOG"; passed=$((passed+1)); else printf 'FAIL: %s\n' "$what" >> "$LOG"; echo "FAIL: $what"; fail=$((fail+1)); fi; }
# wcheck WHAT SECONDS CONDITION: waits for the condition, then records it.
wcheck() { local what=$1 n=$(( $2 * 4 )); shift 2; while [ $n -gt 0 ]; do if eval "$1" >/dev/null 2>&1; then printf 'PASS: %s\n' "$what" >> "$LOG"; passed=$((passed+1)); return 0; fi; sleep 0.25; n=$((n-1)); done; printf 'FAIL: %s (not within the wait)\n' "$what" >> "$LOG"; echo "FAIL: $what"; fail=$((fail+1)); }
ev() { eval "$1"; }
facts() { printf -- '--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }

apppid() { pgrep -f "$DEST/Contents/MacOS/Agent Mux" | head -1; }
applog() { /usr/bin/log show --start "$(cat $R/app.started)" --style compact --predicate "subsystem == \"io.github.sglavoie.agentmux\" AND processIdentifier == $(apppid)" 2>/dev/null | sed -E -e '1d' -e 's/^[0-9-]+ ([0-9:.]+) +[A-Za-z]+ +Agent Mux\[[0-9:a-f]+\] /\1 /'; }
clients() { priv list-clients -F 'client_pid=#{client_pid} term=#{client_termname} pane=#{pane_id} size=#{client_width}x#{client_height}' 2>/dev/null | sort; }
panes() { priv list-panes -a -F '#{pane_id} pid=#{pane_pid} dead=#{pane_dead} size=#{pane_width}x#{pane_height}' 2>&1; }
rsof() { registry | awk -v p="project=$1" '$2=="live" && $6==p {print $1}' | tail -1; }
paneof() { registry | awk -v p="project=$1" '$2=="live" && $6==p {print $5}' | tail -1; }
pidof() { registry | awk -v p="project=$1" '$2=="live" && $6==p {print $7}' | tail -1 | cut -d= -f2; }
agentproc() { LC_ALL=C TZ=UTC ps -o pid=,lstart= -p "$(pidof "$1")" 2>/dev/null | sed -e 's/^ *//' -e 's/  */ /g'; }
attached() { clients | grep -c "pane=$(paneof "$1") "; }
prefs() { echo "window_open=$(defaults read $BUNDLE amxWorkspaceWindowOpen 2>/dev/null || echo unset) place=$(defaults read $BUNDLE amxWorkspacePlace 2>/dev/null || echo unset)"; }
snapshot() { python3 - <<'PY'
import json,os,socket
s=socket.socket(socket.AF_UNIX); s.settimeout(5); s.connect(os.path.expanduser('~/.local/share/agent-mux/daemon.sock'))
s.sendall((json.dumps({"type":"subscribe","subscribe":{"protocol":1,"client":"acceptance-probe","features":["runtime_sessions"]}})+"\n").encode())
f=s.makefile(); ack=json.loads(f.readline()); frame=json.loads(f.readline())
d=frame['data']
print('snapshot v%s daemon.pid=%s attention.total=%s' % (d.get('v'), d['daemon'].get('pid'), d['attention'].get('total')))
for x in d.get('sessions',[]):
    rt=x.get('runtime') or {}
    print('  %-6s icon=%-10s attention=%-5s runtime=%s %s %s' % (x.get('project'), x.get('icon_state') or '-', x.get('attention',False), rt.get('backend','-'), rt.get('pane',''), rt.get('session_id','')))
PY
}
# under_app PATTERN: a process matching PATTERN descends from the app.
under_app() { local p; p=$(pgrep -f "$1" | head -1); while [ -n "$p" ] && [ "$p" != 1 ]; do [ "$p" = "$(apppid)" ] && return 0; p=$(ps -o ppid= -p "$p" | tr -d ' '); done; return 1; }
dview() { python3 $R/driver/dview.py "$@"; }
app_start() {
  date '+%Y-%m-%d %H:%M:%S' > $R/app.started
  open -n "$DEST" --env HOME="$HOME" --env TMPDIR="$TMPDIR" --env PATH="$PATH"
}
world() {
  facts "registry" registry
  facts "panes on the private tmux server" panes
  facts "terminals attached to the private tmux server" clients
  facts "what the daemon lists" snapshot
  facts "the app's saved workspace" prefs
}

note "the packaged app and a fresh world: the daemon and two agents on the private tmux server, all from the bundle's own amx and tmux"
facts "the Mac" sh -c 'scutil --get ComputerName; sw_vers -productVersion; uname -m'
facts "the installed bundle" sh -c "echo '$DEST'; /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' '$DEST/Contents/Info.plist'; '$AMX' --version; '$TM' -V; codesign --verify --strict --deep --verbose=2 '$DEST' 2>&1"
check "the installed bundle passes strict signature verification" codesign --verify --strict --deep "$DEST"
check "the app is not running and has no preferences yet" ev '[ -z "$(apppid)" ] && ! defaults read $BUNDLE'
amx daemon stop >/dev/null 2>&1; sleep 1
nohup amx daemon start >> $R/logs/daemon.out 2>&1 </dev/null &
wcheck "the daemon started by the bundled amx answers" 15 'snapshot'
for p in alpha beta; do amx project launch $p --profile work --backend tmux --json > $R/logs/launch-$p.json 2>&1 || cat $R/logs/launch-$p.json >> "$LOG"; done
wcheck "the daemon lists both agents on the tmux backend" 15 '[ "$(snapshot | grep -c runtime=tmux)" = 2 ]'
check "no terminal is attached to either agent" ev '[ -z "$(clients)" ]'
A=$(rsof alpha); B=$(rsof beta)
alpha_before=$(agentproc alpha); beta_before=$(agentproc beta); server_before=$(priv display-message -p '#{pid}')
echo "alpha $A: $alpha_before; beta $B: $beta_before; server pid $server_before" >> "$LOG"

install -d $R/shim; install -m 0755 $R/driver/amx-shim.sh $R/shim/amx
defaults write $BUNDLE amxBinaryPath -string $R/shim/amx
defaults write $BUNDLE amxUseAppNotifications -bool false
defaults write $BUNDLE amxEnvironment -dict PATH "$PATH"
defaults write $BUNDLE amxOpenWorkspaceAtLaunch -bool true
app_start
wcheck "the packaged app runs from the install root" 15 '[ -n "$(apppid)" ]'
wcheck "its feed from the daemon is live" 30 'applog | grep -q "feed: live"'
wcheck "it found the tmux it bundles, verified" 30 'applog | grep -q "backend: tmux 3.7c (bundled with Agent Mux, verified)"'
wcheck "its workspace window opened" 30 'applog | grep -q "workspace: opened"'
facts "the app's log so far" applog

note "embedded terminal: the workspace shows alpha in an embedded Ghostty terminal attached to the agent's pane"
facts "workspace_view session alpha" dview session "$A"
wcheck "one terminal is attached to alpha's pane" 20 '[ "$(attached alpha)" = 1 ]'
facts "terminals attached" clients
check "it is the app's embedded Ghostty (xterm-ghostty)" ev 'clients | grep "pane=$(paneof alpha) " | grep -q "term=xterm-ghostty"'
facts "the attaching process and its ancestry" sh -c "p=\$(pgrep -f 'bin/amx attach $A' | head -1); while [ -n \"\$p\" ] && [ \"\$p\" != 1 ]; do ps -o pid=,ppid=,command= -p \$p | cut -c1-160; p=\$(ps -o ppid= -p \$p | tr -d ' '); done"
check "the terminal runs the bundled amx attach, started by the app" under_app "Contents/Resources/bin/amx attach $A"
wcheck "the app saved where it is" 10 'prefs | grep -q "place=session:$A"'
check "beta has no terminal: showing one session takes nothing from another" ev '[ "$(attached beta)" = 0 ]'

note "attention while viewing another session: alpha asks for permission while the workspace is moved to beta"
facts "workspace_view session beta" dview session "$B"
wcheck "the app's terminal is now on beta" 20 '[ "$(attached beta)" = 1 ]'
# The workspace keeps one terminal per session while its window is open, so
# alpha's stays; what must not happen is a second one, or a changed agent.
wcheck "alpha keeps its one terminal and its agent is the process it was" 10 '[ "$(attached alpha)" = 1 ] && [ "$(agentproc alpha)" = "$alpha_before" ]'
priv send-keys -t "$(paneof alpha)" -l ask; priv send-keys -t "$(paneof alpha)" Enter
wcheck "the daemon's feed marks alpha as needing attention while the workspace shows beta" 15 'snapshot | grep -q "alpha .*attention=True"'
facts "what the daemon lists" snapshot

note "utility views: the Overview and the Dashboard run as the bundled amx in terminals of the app"
facts "workspace_view overview" dview overview
wcheck "an Overview process runs under the app" 20 'under_app "bin/amx workspace-view overview"'
check "the session terminals are still one per session, none added" ev '[ "$(attached alpha)" = 1 ] && [ "$(attached beta)" = 1 ] && [ "$(clients | wc -l | tr -d " ")" = 2 ]'
facts "workspace_view dashboard" dview dashboard
wcheck "a Dashboard process runs under the app" 20 'under_app "bin/amx workspace-view dashboard"'
facts "the app's amx processes" sh -c "pgrep -fl 'bin/amx (attach|workspace-view)' | cut -c1-160"
check "both agents are the processes they were" ev '[ "$(agentproc alpha)" = "$alpha_before" ] && [ "$(agentproc beta)" = "$beta_before" ]'

note "quit and reopen: quitting the app ends no agent, and it comes back where it was"
dview session "$B" >> "$LOG" 2>&1
wcheck "the workspace is back on beta" 20 '[ "$(attached beta)" = 1 ]'
wcheck "the app saved that" 10 'prefs | grep -q "place=session:$B"'
osascript -e "tell application id \"$BUNDLE\" to quit" >> "$LOG" 2>&1
wcheck "the app quit" 15 '[ -z "$(apppid)" ]'
wcheck "quitting detached every terminal" 10 '[ -z "$(clients)" ]'
check "both agents are the processes they were, in the same server" ev '[ "$(agentproc alpha)" = "$alpha_before" ] && [ "$(agentproc beta)" = "$beta_before" ] && [ "$(priv display-message -p "#{pid}")" = "$server_before" ]'
check "the registry still holds both sessions live" ev '[ "$(registry | grep -c " live ")" = 2 ]'
app_start
wcheck "the app is running again with its feed live" 30 '[ -n "$(apppid)" ] && applog | grep -q "feed: live"'
wcheck "it reopened its workspace on beta by itself" 30 '[ "$(attached beta)" = 1 ]'
facts "the app's log after reopening" applog

note "killed outright: the app is killed with its terminal on beta, and started again"
kill -9 "$(apppid)"
wcheck "the app is gone" 10 '[ -z "$(apppid)" ]'
wcheck "its terminal went with it and the agent did not" 15 '[ -z "$(clients)" ] && [ "$(agentproc beta)" = "$beta_before" ]'
app_start
wcheck "started again, it returns to beta" 40 '[ -n "$(apppid)" ] && [ "$(attached beta)" = 1 ]'
check "both agents are still the processes they were at the start" ev '[ "$(agentproc alpha)" = "$alpha_before" ] && [ "$(agentproc beta)" = "$beta_before" ]'
world

note "teardown of this run: the app is quit, the agents ended through the bundled amx, the daemon stopped"
osascript -e "tell application id \"$BUNDLE\" to quit" >> "$LOG" 2>&1
wcheck "the app quit" 15 '[ -z "$(apppid)" ]'
facts "amx backend stop --yes" amx backend stop --yes
facts "amx daemon stop" amx daemon stop
check "no private tmux server is left" ev '! priv list-panes -a'
defaults delete $BUNDLE >/dev/null 2>&1; rm -f "$REAL_HOME/Library/Preferences/$BUNDLE.plist"
check "the app's preferences are removed again" ev '! defaults read $BUNDLE'
rm -rf $DATA/runtime-sessions.json $HOME/.local/state
printf '\n##### RESULT: %d passed, %d failed\n' "$passed" "$fail" >> "$LOG"; echo "packaged: $passed passed, $fail failed"
