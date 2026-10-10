# Helpers shared by the session-13 acceptance steps. Source after env.sh.
LOG=${LOG:-$R/logs/acceptance.log}
KITTEN=/Applications/kitty.app/Contents/MacOS/kitten
ksock() { cat $R/kitty.sockname; }
K() { $KITTEN @ --to "$(ksock)" "$@"; }
priv() { tmux -S $R/tmp/agent-mux-$(id -u)/tmux.sock "$@"; }
apppid() { cat $R/app.pid 2>/dev/null; }
note() { printf '\n##### %s [%s]\n' "$*" "$(date -u +%H:%M:%SZ)" >> "$LOG"; echo "## $*"; }
facts() { printf '\n--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }
say() { printf '%s\n' "$*" >> "$LOG"; echo "$*"; }
fail=0
# check DESCRIPTION COMMAND...: runs the command, records PASS or FAIL.
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then say "PASS: $what"; else say "FAIL: $what"; fail=$((fail+1)); fi; }
# waitfor SECONDS COMMAND...: polls until the command succeeds.
waitfor() { local n=$(( $1 * 4 )); shift; while [ $n -gt 0 ]; do "$@" >/dev/null 2>&1 && return 0; sleep 0.25; n=$((n-1)); done; return 1; }

# What the private tmux server has attached: one line per client.
clients() { priv list-clients -F '#{client_pid} #{session_name} pane=#{pane_id} term=#{client_termname}' 2>/dev/null | sort; }
# The runtime-session registry, one line per session.
registry() { python3 - <<'PY'
import json,os
p=os.path.expanduser('~/.local/share/agent-mux/runtime-sessions.json')
for s in json.load(open(p))['sessions']:
    print(s['id'], s['state'], s.get('project',''), s['target']['instance']['kind'], s['target'].get('ref',''), 'attached' if s.get('attachment') else 'detached')
PY
}
# rsid PROFILE: the live runtime session of alpha launched with PROFILE.
rsid() { cat $R/rs-$1; }
pane() { cat $R/pane-$1; }
# The Kitty tree: tabs, their focus, and the AgentMux vars of each window.
tree() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    print('tab %d%s title=%r' % (t['id'], ' [active]' if t['is_active'] else '', t['title']))
    for w in t['windows']:
      uv={k:v for k,v in w.get('user_vars',{}).items() if k in ('amx_profile','amx_project','amx_harness')}
      print('   window %d focused=%s vars=%s' % (w['id'], w.get('is_focused'), uv))"; }
# The Kitty window showing PROJECT.
kwin() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    for w in t['windows']:
      if w.get('user_vars',{}).get('amx_project')=='$1': print(w['id'])" | tail -1; }
kfocused() { K ls | python3 -c "
import json,sys
for o in json.load(sys.stdin):
  for t in o['tabs']:
    for w in t['windows']:
      if w.get('is_focused') and t['is_active']: print(w['id'])" | tail -1; }
# The process that has the keyboard, by pid.
frontpid() { osascript -e 'tell application "System Events" to get unix id of first process whose frontmost is true'; }
# The app's unified log since it started (notice level and up), without the
# timestamp and process columns.
applog() { /usr/bin/log show --start "$(cat $R/app.started)" --style compact --predicate "subsystem == \"io.github.sglavoie.agentmux\" AND processIdentifier == $(apppid)" 2>/dev/null | sed -E -e '1d' -e 's/^[0-9-]+ ([0-9:.]+) +[A-Za-z]+ +Agent Mux\[[0-9:a-f]+\] /\1 /'; }
logged() { applog | grep -qF -- "$1"; }
# newlog MARK: the log lines after the last line containing MARK.
# The app under test, in the world's environment, with its own preferences.
app_defaults() {
  defaults delete $BUNDLE >/dev/null 2>&1
  defaults write $BUNDLE amxBinaryPath -string $R/shim/amx
  defaults write $BUNDLE amxUseAppNotifications -bool false
  defaults write $BUNDLE amxHotKeyDashboard -string "ctrl+opt+shift+cmd+d"
  defaults write $BUNDLE amxHotKeyNextAttention -string "ctrl+opt+shift+cmd+n"
  defaults write $BUNDLE amxEnvironment -dict PATH "$PATH" XDG_STATE_HOME "$XDG_STATE_HOME" XDG_CONFIG_HOME "$XDG_CONFIG_HOME" XDG_DATA_HOME "$XDG_DATA_HOME"
  defaults write $BUNDLE amxOpenWorkspaceAtLaunch -bool "${1:-true}"
}
app_start() {
  date '+%Y-%m-%d %H:%M:%S' > $R/app.started
  ( nohup "$APP/Contents/MacOS/Agent Mux" >> $R/logs/app.out 2>&1 & echo $! > $R/app.pid )
  waitfor 20 logged "feed: live"
}
app_quit() { [ -f $R/app.pid ] || return 0; kill "$(apppid)" 2>/dev/null; waitfor 10 sh -c "! kill -0 $(apppid) 2>/dev/null"; }
# AX: run AppleScript against the app under test's process.
ax() { osascript -e "tell application \"System Events\" to tell (first process whose unix id is $(apppid))" -e "$1" -e "end tell"; }
# The workspace window: the app's one standard window (the answer panel is a
# floating one).
WS='(first window whose subrole is "AXStandardWindow")'
PANEL='window "Agent Mux — Supervisor question"'
# The workspace sidebar as accessibility describes it, one line per row.
sidebar() { ax 'set out to ""
repeat with r in rows of table 1 of scroll area 1 of splitter group 1 of '"$WS"'
  set mark to "   "
  if selected of r then set mark to " > "
  set out to out & mark & (description of UI element 1 of r) & linefeed
end repeat
return out'; }
# What the pane beside the sidebar and the sidebar footer say.
texts() { ax 'set out to ""
repeat with t in (static texts of splitter group 1 of '"$WS"')
  set out to out & (value of t) & linefeed
end repeat
return out'; }
wtitle() { ax "get name of $WS"; }
# clickrow N: a real mouse click on sidebar row N (1-based).
clickrow() {
  clickon "row $1 of table 1 of scroll area 1 of splitter group 1 of $WS"
}
# clickon ELEMENT: a real left click in the middle of an accessibility element.
clickon() {
  local pos size
  pos=$(ax "get position of $1") && size=$(ax "get size of $1") || return 1
  clickat $(python3 -c "
x,y=[int(v) for v in '$pos'.split(',')]; w,h=[int(v) for v in '$size'.split(',')]
print(x+w//2, y+h//2)")
}
# clickat X Y: a real left click at a screen point.
clickat() { $R/bin/click "$1" "$2"; }
# rowindex TEXT: the 1-based sidebar row whose description contains TEXT.
rowindex() { sidebar | grep -nF -- "$1" | head -1 | cut -d: -f1; }
# shot NAME: a screenshot of the workspace window.
shot() {
  local pos size win=${2:-$WS}
  pos=$(ax "get position of $win") && size=$(ax "get size of $win") || return 1
  screencapture -x -R"$(echo "$pos,$size" | tr -d ' ')" "$SHOTS/$1.png" && say "screenshot: $1.png (window at $pos, size $size)"
}
# typekeys TEXT: types into whatever has the keyboard, then Return.
typekeys() { osascript -e "tell application \"System Events\" to keystroke \"$1\"" -e 'tell application "System Events" to key code 36'; }
# The world's daemon, detached, knowing the world's Kitty from the start (as a
# daemon started from inside that Kitty does).
daemon_start() { ( KITTY_LISTEN_ON=$(ksock) nohup amx daemon start >> $R/logs/daemon.out 2>&1 & ); waitfor 10 test -S $HOME/.local/share/agent-mux/daemon.sock; }
daemon_stop() { amx daemon stop >> $R/logs/daemon.out 2>&1; waitfor 10 sh -c '! pgrep -f "^/tmp/amxs13/bin/amx daemon start" >/dev/null && ! pgrep -f "^amx daemon start" >/dev/null'; }
daemon_pid() { pgrep -f "amx daemon start" | while read p; do ps -p $p -o command= | grep -q "^/Users" || echo $p; done | head -1; }
# The companion snapshot's sessions as a v2 subscriber gets them.
snapshot() { python3 - <<'PY'
import json,os,socket
s=socket.socket(socket.AF_UNIX); s.settimeout(5); s.connect(os.path.expanduser('~/.local/share/agent-mux/daemon.sock'))
s.sendall((json.dumps({"type":"subscribe","subscribe":{"protocol":1,"client":"acceptance-probe","features":["runtime_sessions"]}})+"\n").encode())
f=s.makefile(); ack=json.loads(f.readline()); frame=json.loads(f.readline())
d=frame['data']
print('snapshot v%s features=%s kitty.socket=%s' % (d.get('v'), ack['subscription'].get('features'), d['kitty'].get('socket')))
for x in d.get('sessions',[]):
    rt=x.get('runtime') or {}
    print('  %-6s %-9s %-7s icon=%-9s attention=%-5s window=%s runtime=%s %s %s' % (x.get('project'), x.get('profile'), x.get('harness'), x.get('icon_state') or '-', x.get('attention',False), x.get('window_id',0), rt.get('backend','-'), rt.get('pane',''), rt.get('session_id','')))
for w in d.get('asl',{}).get('waiting',[]):
    print('  asl waiting run=%s unanswered=%s runtime=%s' % (w.get('run_id'), w.get('unanswered'), w.get('runtime_session_id','')))
PY
}
# The status item's menu, through accessibility. Opening blocks until the menu
# closes, so it runs beside the calls that read or click it.
statusmenu_open() { (ax 'click menu bar item 1 of menu bar 2' >/dev/null 2>&1 &); sleep 1.5; }
statusmenu_items() { ax 'set out to ""
repeat with i in menu items of menu 1 of menu bar item 1 of menu bar 2
  set n to name of i
  if n is missing value then set n to "----"
  set out to out & n & linefeed
end repeat
return out'; }
statusmenu_click() { ax "click menu item $1 of menu 1 of menu bar item 1 of menu bar 2" >/dev/null; }
menu_close() { osascript -e 'tell application "System Events" to key code 53'; sleep 0.5; }
# hotkey KEYCODE: presses ⌃⌥⇧⌘ and the key, as a global shortcut.
hotkey() { osascript -e "tell application \"System Events\" to key code $1 using {control down, option down, shift down, command down}"; }
# front PID: gives that process the keyboard.
front() { osascript -e "tell application \"System Events\" to set frontmost of (first process whose unix id is $1) to true"; sleep 0.7; }
kittypid() { ksock | sed 's/.*-//'; }
# Sends one line to a stand-in agent, by backend.
tell_tmux() { priv send-keys -t "$1" "$2" Enter; }
tell_kitty() { K send-text --match "id:$1" "$2"$'\r'; }
pane_has() { priv capture-pane -p -t "$1" | grep -qF -- "$2"; }
row_is() { sidebar | sed -n "$1p" | grep -qF -- "$2"; }
selected_row() { sidebar | grep -F ' > ' | sed 's/^ > //'; }
cell_texts() { ax "get value of static texts of UI element 1 of row $1 of table 1 of scroll area 1 of splitter group 1 of $WS"; }
# typetext TEXT: types into whatever has the keyboard, without Return.
typetext() { osascript -e "tell application \"System Events\" to keystroke \"$1\""; }
panel_texts() { ax "get value of every static text of group 1 of $PANEL"; ax "get value of every static text of scroll area 1 of group 1 of $PANEL"; }
# since MARK: app log lines after the mark line count recorded by `mark`.
mark() { applog | wc -l | tr -d ' ' > $R/logmark; }
since() { applog | tail -n +$(( $(cat $R/logmark) + 1 )); }
since_has() { since | grep -qF -- "$1"; }
