#!/bin/bash
# Runs on the MacBook, inside the world's own Kitty (drive_kitty.sh left it
# running). A rehearsal of the two recipes of the paired gate that act on an
# installation, with the installation being the world's: `just
# install-restart-smoke` and `just cockpit-smoke`, typed into the Kitty window
# as an operator would, with HOME the world's, so the amx that is installed,
# the daemon that is restarted, and the Kitty that is asked are all the
# world's. The MacBook user's own ~/.local/bin/amx and daemon are not touched;
# down.sh checks that afterwards.
#
# It records what happened and judges nothing: whether these recipes can be
# isolated at all is what the run is for.
# usage: drive_cockpit.sh LOG
R=/tmp/amxs19; . $R/driver/remote-env.sh
LOG=$1; : > "$LOG"; D=1
note() { printf '\n##### %s [%s]\n' "$*" "$(date -u +%H:%M:%SZ)" >> "$LOG"; echo "## $*"; }
shot() { printf '\n=== %s ===\n' "$1" >> "$LOG"; K get-text --match id:$D --extent all 2>&1 | sed -e 's/[[:space:]]*$//' | cat -s | tail -${2:-60} >> "$LOG"; }
txt() { K send-text --match id:$D "$1"; }
waitfor() { local n=$(( ${2:-15} * 2 )); while [ $n -gt 0 ]; do K get-text --match id:$D --extent all 2>/dev/null | grep -q -- "$1" && return 0; sleep 0.5; n=$((n-1)); done; return 1; }
facts() { printf '\n--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }
K ls >/dev/null 2>&1 || { echo "the world's Kitty is not running: run the kitty step first" | tee -a "$LOG"; exit 1; }

note "before: what is installed in the world, and the MacBook user's own amx"
facts "the world's ~/.local/bin and ~/.config/kitty" sh -c "ls -la $HOME/.local/bin $HOME/.config/kitty 2>&1"
facts "the MacBook user's own amx (must not change)" sh -c "/usr/bin/stat -f '%N %z %m' $REAL_HOME/.local/bin/amx 2>&1"
facts "tools the recipes need, outside the world's PATH" sh -c "ls -l /opt/homebrew/bin/just /opt/homebrew/bin/go; /opt/homebrew/bin/go version"

# pass LABEL PATHPREFIX: both recipes, typed in the world's Kitty window.
pass() {
  local label=$1 prefix=$2
  txt $'clear\r'; sleep 1
  note "$label: just install-restart-smoke, typed in the world's Kitty window"
  txt "cd $COPY && PATH=$prefix\$PATH:/opt/homebrew/bin just install-restart-smoke; echo SMOKE_EXIT=\$?"$'\r'
  if waitfor "SMOKE_EXIT=[0-9]" 1200; then echo "install-restart-smoke finished" >> "$LOG"; else echo "install-restart-smoke did not finish within 20 minutes" >> "$LOG"; fi
  shot "the Kitty window after install-restart-smoke" 200
  smoke=$(K get-text --match id:$D --extent all | sed -n 's/.*SMOKE_EXIT=\([0-9]*\).*/\1/p' | tail -1)
  facts "the world's ~/.local/bin and ~/.config/kitty afterwards" sh -c "ls -la $HOME/.local/bin $HOME/.config/kitty 2>&1"
  facts "the installed amx" sh -c "$HOME/.local/bin/amx --version 2>&1"
  facts "the world's daemon" sh -c "$HOME/.local/bin/amx daemon status 2>&1"
  txt $'clear\r'; sleep 1
  note "$label: just cockpit-smoke, typed in the same window"
  txt "PATH=$prefix\$PATH:/opt/homebrew/bin just cockpit-smoke; echo COCKPIT_EXIT=\$?"$'\r'
  if waitfor "COCKPIT_EXIT=[0-9]" 300; then echo "cockpit-smoke finished" >> "$LOG"; else echo "cockpit-smoke did not finish within 5 minutes" >> "$LOG"; fi
  shot "the Kitty window after cockpit-smoke" 200
  cockpit=$(K get-text --match id:$D --extent all | sed -n 's/.*COCKPIT_EXIT=\([0-9]*\).*/\1/p' | tail -1)
  printf '\n##### OBSERVED (%s): install-restart-smoke exit %s; cockpit-smoke exit %s\n' "$label" "${smoke:-none}" "${cockpit:-none}" >> "$LOG"
  echo "$label: install-restart-smoke exit ${smoke:-none}; cockpit-smoke exit ${cockpit:-none}"
}

# First as the world stands after the other runs: the packaged amx first on
# PATH, notifications as configured by default, no hooks installed.
pass "first pass" ""

# Then with what the first pass's doctor asked for, short of ASL: no
# notification may be shown, the installed amx is the amx on PATH, and the
# profile's hooks are installed by it.
note "between the passes: the world is prepared as doctor asked"
printf '\n[defaults.notifications]\nalerter_path = "/nonexistent/alerter"\nerror = false\npermission = false\nelicitation = false\nstop = false\nstop_failure = false\nstale_active = false\nhook_silence = false\n' >> $HOME/.config/agent-mux/config.toml
facts "amx config validate (the installed amx)" $HOME/.local/bin/amx config validate
facts "amx config hooks-sync (the installed amx, first on PATH)" env PATH=$HOME/.local/bin:$PATH $HOME/.local/bin/amx config hooks-sync
pass "second pass" "$HOME/.local/bin:"

facts "the MacBook user's own amx afterwards" sh -c "/usr/bin/stat -f '%N %z %m' $REAL_HOME/.local/bin/amx 2>&1"
facts "asl in the world" sh -c "command -v asl || echo 'no asl on the world PATH'"
txt "cd $R"$'\r'
$HOME/.local/bin/amx daemon stop >> "$LOG" 2>&1
