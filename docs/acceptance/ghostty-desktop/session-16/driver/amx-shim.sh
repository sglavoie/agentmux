#!/bin/sh
# The amx the app under test runs (its amxBinaryPath), on the MacBook.
#
# libghostty starts a terminal's command through login(1), which resets HOME
# to the account's real one; this puts the isolated world back, including the
# bundled tmux on PATH.
#
# A utility view (`amx workspace-view KIND ...`) is additionally hosted in the
# observer tmux, in a session named for its kind, so the driver can read its
# screen. Agent terminals (`amx attach`) and the app's status questions
# (`amx attach --json`) are not wrapped: they are exactly what the app runs.
R=/tmp/amxs16
export HOME=$R/home TMPDIR=$R/tmp PATH=$R/bin:$R/tmux/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
export TERMINFO_DIRS=$R/tmux/share/terminfo:/usr/share/terminfo
if [ "$1" = workspace-view ]; then
  kind=$2
  envfile=$R/tmp/view-env.$$
  export -p > "$envfile"
  cmd=". $envfile; rm -f $envfile; exec $R/bin/amx"
  for a in "$@"; do cmd="$cmd '$a'"; done
  $R/obs.sh kill-session -t "=$kind" 2>/dev/null
  exec $R/obs.sh new-session -s "$kind" "$cmd" \; set-option status off
fi
exec $R/bin/amx "$@"
