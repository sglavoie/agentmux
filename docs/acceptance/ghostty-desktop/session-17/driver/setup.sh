#!/bin/bash
# Builds the world on the MacBook: an empty install root, a HOME and TMPDIR
# of its own, a stand-in agent, and nothing else. It installs nothing: that is
# what run.sh tests. The bundle under test is the one `just mac-app Release`
# left in the checkout copy macos/scripts/remote-ui.sh keeps on the MacBook;
# this syncs the scripts beside it, so the installer that runs there is this
# checkout's.
# usage: setup.sh REPO_ROOT
set -euo pipefail
repo=$1; here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"
"$repo/macos/scripts/remote-ui.sh" sync
copy=$("$repo/macos/scripts/remote-ui.sh" dir)
$SSH "$H" "rm -rf $R && mkdir -p $R/bin $R/home/.config/agent-mux $R/tmp $R/logs $R/Applications $R/proj/alpha $R/proj/beta && chmod 700 $R/tmp && echo \"\$HOME/$copy\" > $R/copy" </dev/null
rsync -a -e "$SSH" "$here/remote-env.sh" "$here/remote-lib.sh" "$here/agent.sh" "$here/config.toml" "$H:$R/" </dev/null
{ echo ". $R/remote-env.sh"; cat; } <<'REMOTE' | $SSH "$H" /bin/sh -s
set -e
install -m 0755 $R/agent.sh $R/bin/claude
cp $R/config.toml $HOME/.config/agent-mux/config.toml
# AgentMux starts a harness through a login shell, whose system profile puts
# the system directories first: the world's bin goes back in front.
printf "export PATH=$R/bin:\$PATH\n" > $HOME/.zshrc
# The operator's own files, which uninstalling must leave alone.
mkdir -p $DATA/handoffs $DATA/worktrees/keep
echo "a handoff" > $DATA/handoffs/keep.md
echo "a worktree file" > $DATA/worktrees/keep/file
echo '{}' > $DATA/launch_overrides.json
echo '{"schema_version":1}' > $DATA/transcripts.json
test -d "$SRC/Contents" || { echo "no Release bundle at $SRC: run just mac-app Release"; exit 1; }
echo "world ready on $(scutil --get ComputerName); bundle under test: $SRC"
REMOTE
