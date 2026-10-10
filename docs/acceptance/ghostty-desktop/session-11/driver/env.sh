export HOME=/tmp/amxs11/home TMPDIR=/tmp/amxs11/tmp PATH=/tmp/amxs11/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
export XDG_STATE_HOME=/tmp/amxs11/home/.local/state XDG_CONFIG_HOME=/tmp/amxs11/home/.config XDG_DATA_HOME=/tmp/amxs11/home/.local/share
unset KITTY_LISTEN_ON KITTY_WINDOW_ID KITTY_PID KITTY_PUBLIC_KEY KITTY_INSTALLATION_DIR AMX_CONFIG AMX_RUNTIME_SESSION_ID CLAUDECODE TMUX TMUX_PANE
for v in $(env | grep -E '^(ASL_|AMX_|CLAUDE_CODE)' | cut -d= -f1); do unset $v; done
export TERM=xterm-256color
