# Helpers for the driver, which runs on the Studio and acts only on the
# MacBook. Source after env.sh. State is read over SSH (remote-lib.sh); keys,
# pointer, and pictures go through the headless VNC client (vnc.py). Nothing
# here opens a window or sends input on the Studio.
here=${here:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}
LOG=${LOG:-$L/logs/acceptance.log}
SHOTS=${SHOTS:-$L/shots}
note() { printf '\n##### %s [%s]\n' "$*" "$(date -u +%H:%M:%SZ)" >> "$LOG"; echo "## $*"; }
say() { printf '%s\n' "$*" >> "$LOG"; echo "$*"; }
facts() { printf '\n--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }
fail=0
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then say "PASS: $what"; else say "FAIL: $what"; fail=$((fail+1)); fi; }
waitfor() { local n=$(( $1 * 2 )); shift; while [ $n -gt 0 ]; do "$@" >/dev/null 2>&1 && return 0; sleep 0.5; n=$((n-1)); done; return 1; }
# rq 'COMMAND': runs a command on the MacBook with the remote helpers loaded.
rq() { printf '. %s/remote-lib.sh\n%s\n' "$R" "$1" | rr; }
screen() { rq "screen $1"; }
shas() { screen "$1" | grep -qF -- "$2"; }
swait() {
  if waitfor "${3:-20}" shas "$1" "$2"; then return 0; fi
  say "FAIL: timed out waiting for '$2' on the $1 screen"; fail=$((fail+1))
  printf '\n=== %s screen at timeout ===\n' "$1" >> "$LOG"; screen "$1" | cat -s >> "$LOG"; return 1
}
sshot() { printf '\n=== %s screen: %s [%s] ===\n' "$1" "$2" "$(date -u +%H:%M:%SZ)" >> "$LOG"; screen "$1" | cat -s >> "$LOG"; }
pstate() { rq "pstate $1"; }
pfmt() { rq "pfmt $1 '$2'"; }
clip() { rq 'LC_ALL=en_US.UTF-8 pbpaste'; }
setclip() { printf '%s' "$1" | $SSH "$H" 'LC_ALL=en_US.UTF-8 pbcopy'; }
mark() { rq mark; }
since() { rq since; }
since_has() { since | grep -qF -- "$1"; }
wait_log() { waitfor "${2:-10}" since_has "$1"; }
# --- input, on the MacBook ----------------------------------------------------
vnc() { python3 "$here/vnc.py" "$@"; }
# The framebuffer has two pixels per point.
px() { echo $(( $1 * 2 )); }
key() { vnc key "$@"; sleep 0.3; }
typetext() { vnc type "$1"; sleep 0.3; }
click() { vnc click "$(px $1)" "$(px $2)" ${3:+"$3"}; }
rclick() { vnc rclick "$(px $1)" "$(px $2)"; }
dclick() { vnc dclick "$(px $1)" "$(px $2)"; }
drag() { vnc drag "$(px $1)" "$(px $2)" "$(px $3)" "$(px $4)" ${5:+"$5"}; }
scroll() { vnc scroll "$(px $1)" "$(px $2)" "$3"; }
# --- geometry, in points ------------------------------------------------------
# The workspace window is placed by its saved frame before the app starts.
WX=10; WY=40; WW=1100; WH=700; SCREEN_H=1117; SCREEN_W=1728
FRAME="$WX $((SCREEN_H-WY-WH)) $WW $WH 0 0 $SCREEN_W $SCREEN_H"
# The terminal pane: right of the 240-point sidebar and its divider, under the title bar.
PX=$((WX+241)); PY=$((WY+32)); PW=$((WW-241)); PH=$((WH-32))
# cellpoint PROJECT COL ROW [DXPOINTS]: the middle of a terminal cell.
cellpoint() {
  local grid; grid=$(pfmt "$1" '#{window_width} #{window_height}')
  python3 -c "
cols,rows=[int(v) for v in '$grid'.split()]
cw=($PW-4)/cols; ch=($PH-4)/rows
print(round($PX+2+($2+0.5)*cw+${4:-0}), round($PY+2+($3+0.5)*ch))"
}
textcell() { rq "textcell $1 '$2'"; }
# textpoint PROJECT TEXT [DCOLS] [DXPOINTS]
textpoint() { local cr; cr=$(textcell "$1" "$2"); [ -n "$cr" ] || return 1; set -- "$1" $cr "${3:-0}" "${4:-0}"; cellpoint "$1" $(( $2 + $4 )) "$3" "$5"; }
# shot NAME: the workspace window's rectangle of the MacBook's screen.
shot() { vnc shot "$SHOTS/$1.png" "$(px $WX)" "$(px $WY)" "$(px $WW)" "$(px $WH)" && say "screenshot: $1.png (the workspace window's rectangle on the MacBook)"; }
# --- conditions for checks ----------------------------------------------------
# ev 'EXPRESSION': a compound condition, evaluated in this shell.
ev() { eval "$1"; }
attached() { [ "$(rq "attached $1")" = 1 ]; }
inhistory() { [ "$(pfmt "$1" '#{pane_in_mode}')" = 1 ]; }
live() { [ "$(pfmt "$1" '#{pane_in_mode}')" = 0 ]; }
# hasline PROJECT TEXT: a screen line that is exactly TEXT, apart from margins.
hasline() { screen "$1" | sed -e 's/^[[:space:]│⏺]*//' -e 's/[[:space:]│┃█]*$//' | grep -qxF -- "$2"; }
notline() { ! hasline "$1" "$2"; }
# adjacent PROJECT A B: a line holding A directly above a line holding B.
adjacent() { screen "$1" | awk -v a="$2" -v b="$3" 'index(prev,a) && index($0,b) {found=1} {prev=$0} END {exit !found}'; }
busy() { screen "$1" | grep -qE 'esc to interrupt|esc interrupt|Working \(|◎ Working'; }
idle() { ! busy "$1"; }
# --- real HID events on the MacBook (hid.swift), for what VNC cannot carry ----
# wheel X Y LINES (points); hidkeys CODE[:MODS]...; both print the modifiers
# the MacBook reports held afterwards, which must be "none".
wheel() { rq "$R/bin/hid scroll $1 $2 $3" > "$L/hid.state"; sleep 0.6; }
hidkeys() { rq "$R/bin/hid keys $*" > "$L/hid.state"; sleep 0.4; }
hidclean() { [ "$(rq "$R/bin/hid state")" = none ]; }
# gui 'COMMAND': runs a command inside the MacBook's GUI session, as a child
# of System Events: an input source can only be enabled and selected there.
gui() { rq "perl -e 'alarm 20; exec @ARGV' osascript -e 'tell application \"System Events\" to do shell script \"$1; true\"' 2>&1"; }
