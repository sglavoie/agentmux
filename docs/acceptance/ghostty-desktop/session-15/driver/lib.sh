# Helpers shared by the session-15 acceptance steps. Source after env.sh.
LOG=${LOG:-$R/logs/acceptance.log}
# The observer tmux hosting the utility views (see amx-shim.sh).
obs() { $R/obs.sh "$@"; }
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
    t=s.get('target') or {}
    print(s['id'], s['state'], s.get('relation',''), s.get('project',''), (t.get('instance') or {}).get('kind',''), t.get('ref',''), 'conv='+str((s.get('conversation') or {}).get('session_id')), 'parent='+str(s.get('parent')))
PY
}
# live: the IDs of the live runtime sessions, oldest first.
live() { registry | awk '$2=="live"{print $1}'; }
# paneof ID: the tmux pane of a runtime session.
paneof() { registry | awk -v id="$1" '$1==id{print $6}'; }
# The process that has the keyboard, by pid.
frontpid() { osascript -e 'tell application "System Events" to get unix id of first process whose frontmost is true'; }
# The app's unified log since it started (notice level and up), without the
# timestamp and process columns.
applog() { /usr/bin/log show --start "$(cat $R/app.started)" --style compact --predicate "subsystem == \"io.github.sglavoie.agentmux\" AND processIdentifier == $(apppid)" 2>/dev/null | sed -E -e '1d' -e 's/^[0-9-]+ ([0-9:.]+) +[A-Za-z]+ +Agent Mux\[[0-9:a-f]+\] /\1 /'; }
logged() { applog | grep -qF -- "$1"; }
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
# app_start: starts the app copy in the world's environment.
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
clickat() { $R/bin/mouse click "$1" "$2"; }
# rowindex TEXT: the 1-based sidebar row whose description contains TEXT.
rowindex() { sidebar | grep -nF -- "$1" | head -1 | cut -d: -f1; }
# shot NAME: a picture of the workspace window alone, by window number, so
# nothing else on the operator's desktop can be in it whatever is in front.
shot() {
  local id; id=$($R/bin/winid "$(apppid)") || return 1
  screencapture -x -o -l "$id" "$SHOTS/$1.png" && say "screenshot: $1.png (window $id only)"
}
# The world's daemon, detached, started with no Kitty variable at all: it has
# no Kitty to know about.
daemon_start() { ( nohup amx daemon start >> $R/logs/daemon.out 2>&1 & ); waitfor 10 test -S $HOME/.local/share/agent-mux/daemon.sock; }
# The companion snapshot's sessions as a v2 subscriber gets them.
snapshot() { python3 - <<'PY'
import json,os,socket
s=socket.socket(socket.AF_UNIX); s.settimeout(5); s.connect(os.path.expanduser('~/.local/share/agent-mux/daemon.sock'))
s.sendall((json.dumps({"type":"subscribe","subscribe":{"protocol":1,"client":"acceptance-probe","features":["runtime_sessions"]}})+"\n").encode())
f=s.makefile(); ack=json.loads(f.readline()); frame=json.loads(f.readline())
d=frame['data']
print('snapshot v%s kitty.ready=%s kitty.socket=%s attention.total=%s' % (d.get('v'), d['kitty'].get('ready'), d['kitty'].get('socket'), d['attention'].get('total')))
for x in d.get('sessions',[]):
    rt=x.get('runtime') or {}
    print('  %-6s %-9s %-7s icon=%-9s attention=%-5s muted=%-5s locked=%-5s looping=%-5s window=%s runtime=%s %s %s' % (x.get('project'), x.get('profile'), x.get('harness'), x.get('icon_state') or '-', x.get('attention',False), x.get('muted',False), x.get('locked',False), x.get('looping',False), x.get('window_id',0), rt.get('backend','-'), rt.get('pane',''), rt.get('session_id','')))
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
pane_has() { priv capture-pane -p -t "$1" | grep -qF -- "$2"; }
row_is() { sidebar | sed -n "$1p" | grep -qF -- "$2"; }
selected_row() { sidebar | grep -F ' > ' | sed 's/^ > //'; }
cell_texts() { ax "get value of static texts of UI element 1 of row $1 of table 1 of scroll area 1 of splitter group 1 of $WS"; }
# typetext TEXT: types into whatever has the keyboard, without Return.
typetext() { osascript -e "tell application \"System Events\" to keystroke \"$1\""; }
# since MARK: app log lines after the mark line count recorded by `mark`.
mark() { applog | wc -l | tr -d ' ' > $R/logmark; }
since() { applog | tail -n +$(( $(cat $R/logmark) + 1 )); }
since_has() { since | grep -qF -- "$1"; }

# --- utility views, through the observer tmux -------------------------------
# vscreen VIEW: the screen of a utility view (dashboard overview shell edit pager).
vscreen() { obs capture-pane -p -t "=$1:" 2>/dev/null | sed -e 's/[[:space:]]*$//' | cat -s; }
vhas() { vscreen "$1" | grep -qF -- "$2"; }
# vwait VIEW TEXT [SECONDS]: waits for text on a view's screen; records the
# screen and fails the step when it never appears.
vwait() {
  if waitfor "${3:-15}" vhas "$1" "$2"; then return 0; fi
  say "FAIL: timed out waiting for '$2' on the $1 view"; fail=$((fail+1))
  printf '\n=== %s view at timeout ===\n' "$1" >> "$LOG"; vscreen "$1" >> "$LOG"
  return 1
}
# vshot VIEW TITLE: records a view's screen in the log.
vshot() { printf '\n=== %s view: %s [%s] ===\n' "$1" "$2" "$(date -u +%H:%M:%SZ)" >> "$LOG"; vscreen "$1" >> "$LOG"; }
# vkey VIEW KEY...: presses keys in a view; vtype types literal text.
vkey() { local v=$1; shift; obs send-keys -t "=$v:" "$@"; }
vtype() { obs send-keys -t "=$1:" -l "$2"; }
vlive() { obs has-session -t "=$1" 2>/dev/null; }
# menu MENU ITEM: chooses an item of the app's main menu.
menu() { ax "click menu item \"$2\" of menu \"$1\" of menu bar item \"$1\" of menu bar 1" >/dev/null; sleep 0.8; }
# The world daemon's health: its subscribers and what each one hosts.
health() { python3 - <<'PY'
import json,os,socket
s=socket.socket(socket.AF_UNIX); s.settimeout(5); s.connect(os.path.expanduser('~/.local/share/agent-mux/daemon.sock'))
s.sendall(b'{"type":"health"}\n'); d=json.loads(s.makefile().readline())
for c in (d.get('companion') or {}).get('clients',[]):
    print('subscriber %s client=%r snapshot_version=%s workspace_views=%s' % (c.get('subscriber_id'), c.get('client'), c.get('snapshot_version'), c.get('workspace_views', False)))
print('kitty_snapshot.ready=%s' % (d.get('kitty_snapshot') or {}).get('ready'))
PY
}
# The processes under the app, as a tree of pid, parent, and command.
apptree() { ps -axo pid=,ppid=,command= | python3 -c "
import sys
rows=[l.split(None,2) for l in sys.stdin if l.strip()]
kids={}
for pid,ppid,cmd in rows: kids.setdefault(ppid,[]).append((pid,cmd))
def walk(pid,depth):
    for k,cmd in kids.get(pid,[]):
        print('  '*depth+k+' '+cmd[:150]); walk(k,depth+1)
walk('$(apppid)',0)"; }
# viewpid VIEW: the process a utility view runs; viewcmd VIEW: its command.
viewpid() { obs list-panes -t "=$1:" -F '#{pane_pid}' 2>/dev/null | head -1; }
viewcmd() { ps -p "$(viewpid "$1")" -o command= 2>/dev/null; }

# --- session 15: keys, pointer, clipboard, panes ------------------------------
# key CODE [MODS]: one real key press by hardware key code, with modifiers
# named as System Events does ("shift down, command down").
key() { if [ -n "${2:-}" ]; then osascript -e "tell application \"System Events\" to key code $1 using {$2}"; else osascript -e "tell application \"System Events\" to key code $1"; fi; sleep 0.25; }
# Hardware key codes used below.
K_RETURN=36; K_ESC=53; K_TAB=48; K_DELETE=51; K_LEFT=123; K_RIGHT=124; K_DOWN=125; K_UP=126
K_PGUP=116; K_PGDN=121; K_HOME=115; K_END=119; K_F12=111; K_F3=99
K_A=0; K_C=8; K_D=2; K_E=14; K_F=3; K_G=5; K_J=38; K_K=40; K_L=37; K_O=31; K_U=32; K_V=9; K_W=13; K_RBRACKET=30; K_LBRACKET=33
# pane PROJECT: the tmux pane of a project's live session.
rsof() { registry | awk -v p="$1" '$2=="live" && $4==p {print $1}' | tail -1; }
pane() { paneof "$(rsof "$1")"; }
# screen PROJECT: the pane's visible screen, as the tmux server holds it.
screen() { priv capture-pane -p -t "$(pane "$1")" | sed -e 's/[[:space:]]*$//'; }
shas() { screen "$1" | grep -qF -- "$2"; }
swait() {
  if waitfor "${3:-15}" shas "$1" "$2"; then return 0; fi
  say "FAIL: timed out waiting for '$2' on the $1 screen"; fail=$((fail+1))
  printf '\n=== %s screen at timeout ===\n' "$1" >> "$LOG"; screen "$1" | cat -s >> "$LOG"
  return 1
}
sshot() { printf '\n=== %s screen: %s [%s] ===\n' "$1" "$2" "$(date -u +%H:%M:%SZ)" >> "$LOG"; screen "$1" | cat -s >> "$LOG"; }
# pfmt PROJECT FORMAT: a tmux format of the pane.
pfmt() { priv display-message -p -t "$(pane "$1")" "$2"; }
pstate() { pfmt "$1" 'alternate=#{alternate_on} mouse=#{mouse_any_flag} in_history=#{pane_in_mode} position=#{scroll_position} history=#{history_size} pid=#{pane_pid} dead=#{pane_dead}'; }
# The terminal pane of the workspace window in screen points: x y w h.
panerect() {
  local wp ws sp ss
  wp=$(ax "get position of $WS"); ws=$(ax "get size of $WS")
  sp=$(ax "get position of scroll area 1 of splitter group 1 of $WS"); ss=$(ax "get size of scroll area 1 of splitter group 1 of $WS")
  python3 -c "
wx,wy=[int(v) for v in '$wp'.split(',')]; ww,wh=[int(v) for v in '$ws'.split(',')]
sx,sy=[int(v) for v in '$sp'.split(',')]; sw,sh=[int(v) for v in '$ss'.split(',')]
x=sx+sw+1; y=sy
print(x, y, wx+ww-x, wy+wh-y)"
}
# cellpoint PROJECT COL ROW: the screen point at the middle of a terminal cell
# (0-based), from the pane rectangle and the grid tmux reports for the viewer.
cellpoint() {
  local grid; grid=$(pfmt "$1" '#{window_width} #{window_height}')
  python3 -c "
x,y,w,h=[float(v) for v in '$(panerect)'.split()]
cols,rows=[int(v) for v in '$grid'.split()]
cw=(w-4)/cols; ch=(h-4)/rows
print(round(x+2+($2+0.5)*cw), round(y+2+($3+0.5)*ch))"
}
# textcell PROJECT TEXT: the column and row of the first screen line holding TEXT.
textcell() {
  screen "$1" | python3 -c "
import sys,unicodedata
needle=sys.argv[1]
for row,line in enumerate(sys.stdin.read().split('\n')):
    i=line.find(needle)
    if i>=0:
        col=sum(2 if unicodedata.east_asian_width(c) in 'WF' else 1 for c in line[:i])
        print(col,row); break
" "$2"
}
# textpoint PROJECT TEXT [DX]: the screen point of TEXT's first cell plus DX cells.
textpoint() { local cr; cr=$(textcell "$1" "$2"); [ -n "$cr" ] || return 1; set -- "$1" $cr "${3:-0}"; cellpoint "$1" $(( $2 + $4 )) "$3"; }
mouse() { $R/bin/mouse "$@"; sleep 0.3; }
# The general pasteboard as text.
clip() { LC_ALL=en_US.UTF-8 pbpaste; }
setclip() { printf '%s' "$1" | LC_ALL=en_US.UTF-8 pbcopy; }
# Shows a project's session with a real click on its sidebar row.
showsession() { front "$(apppid)"; clickrow "$(rowindex "$1, $2")"; sleep 0.8; }

# --- a real input method, borrowed and given back -----------------------------
# The Japanese romaji input method is not one of the operator's enabled input
# sources. ime_begin records the enabled list and the selected source, then
# selects it (which enables it); ime_end selects the recorded source again and
# disables every source that was not enabled before, whatever macOS added
# beside it. ime_begin installs ime_end as the exit trap, so a step that dies
# midway still gives the keyboard back as it was.
IME_JP=com.apple.inputmethod.Kotoeri.RomajiTyping.Japanese
ime_begin() {
  $R/bin/inputsource enabled | sort > $R/ime.before
  $R/bin/inputsource current > $R/ime.current
  trap ime_end EXIT
  $R/bin/inputsource select $IME_JP >/dev/null || return 1
  sleep 1.5
  [ "$($R/bin/inputsource current)" = "$IME_JP" ]
}
ime_end() {
  trap - EXIT
  [ -f $R/ime.before ] || return 0
  $R/bin/inputsource select "$(cat $R/ime.current)" >/dev/null
  for id in $($R/bin/inputsource enabled | sort | comm -13 $R/ime.before -); do
    $R/bin/inputsource disable "$id"
  done
  $R/bin/inputsource enabled | sort > $R/ime.after
  cmp -s $R/ime.before $R/ime.after && [ "$($R/bin/inputsource current)" = "$(cat $R/ime.current)" ]
  local ok=$?
  rm -f $R/ime.before $R/ime.current
  return $ok
}

# --- the link target: a local listener the world owns --------------------------
# linkpid: the process listening on the link port, but only when it is the
# world's own (python's http.server, started in the world's log directory).
# Anything else on that port is somebody else's and is reported, never touched.
linkpid() {
  local pid; pid=$(lsof -nP -t -iTCP:$LINKPORT -sTCP:LISTEN 2>/dev/null | head -1)
  [ -n "$pid" ] || return 1
  ps -o command= -p "$pid" | grep -q "http.server $LINKPORT" || { echo "port $LINKPORT is held by a process that is not the world's: $(ps -o pid=,command= -p "$pid")" >&2; return 2; }
  [ "$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p')" = "/private$R/logs" ] || { echo "port $LINKPORT listener $pid does not run in the world" >&2; return 2; }
  echo "$pid"
}
# link_start: starts the listener and proves it answers, by fetching a marker
# path and finding that request in the log the checks read.
link_start() {
  if lsof -nP -t -iTCP:$LINKPORT -sTCP:LISTEN >/dev/null 2>&1; then
    linkpid >/dev/null || return 1
    link_stop || return 1
  fi
  : > $R/logs/link.log
  ( cd $R/logs && nohup python3 -u -m http.server $LINKPORT --bind 127.0.0.1 >> $R/logs/link.log 2>&1 & )
  waitfor 10 sh -c "lsof -nP -t -iTCP:$LINKPORT -sTCP:LISTEN >/dev/null" || return 1
  curl -s -o /dev/null "http://127.0.0.1:$LINKPORT/s15-selfcheck"
  waitfor 5 grep -q "GET /s15-selfcheck" $R/logs/link.log
}
link_stop() {
  local pid; pid=$(linkpid) || { [ $? -eq 1 ] && return 0; return 1; }
  kill "$pid"; waitfor 5 sh -c "! lsof -nP -t -iTCP:$LINKPORT -sTCP:LISTEN >/dev/null 2>&1"
}
linkhits() { grep -c "GET /s15-link " $R/logs/link.log 2>/dev/null; }
