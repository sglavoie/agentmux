#!/bin/sh
# The amx the app under test runs (its amxBinaryPath).
#
# libghostty starts a terminal's command through login(1), which resets HOME
# to the account's real one; this puts the isolated world's HOME back.
#
# A utility view (`amx workspace-view KIND ...`) is additionally hosted in the
# observer tmux, in a session named for its kind, so the driver can read its
# screen and type into it. The real amx runs there with exactly the
# environment the app's terminal gave this shim (written to a file and
# sourced, because a tmux session does not inherit its client's environment).
# What the terminal's environment carried is logged first, so the driver can
# show that a borrowed session identity reached the view and stopped there.
R=/tmp/amxs14
export HOME=$R/home
if [ "$1" = workspace-view ]; then
  kind=$2
  printf '%s view: args=[%s] inherited AMX_RUNTIME_SESSION_ID=%s KITTY_WINDOW_ID=%s CLAUDECODE=%s\n' \
    "$kind" "$*" "${AMX_RUNTIME_SESSION_ID:-none}" "${KITTY_WINDOW_ID:-none}" "${CLAUDECODE:-none}" >> $R/logs/views.log
  envfile=$R/tmp/view-env.$$
  export -p > "$envfile"
  cmd=". $envfile; rm -f $envfile; exec $R/bin/amx"
  for a in "$@"; do cmd="$cmd '$a'"; done
  $R/obs.sh kill-session -t "=$kind" 2>/dev/null
  exec $R/obs.sh new-session -s "$kind" "$cmd" \; set-option status off
fi
exec $R/bin/amx "$@"
