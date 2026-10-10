#!/bin/sh
# Stand-in editor: says what it was asked to edit and waits for Enter.
echo "EDITOR VIEW (stand-in editor) in $(pwd)"
echo "editing: $*"
echo "identity variables in the editor's environment: $(env | grep -cE '^(KITTY_|AMX_RUNTIME_SESSION_ID=|TMUX|CLAUDECODE=)')"
printf 'press enter to close the editor: '
read -r _
