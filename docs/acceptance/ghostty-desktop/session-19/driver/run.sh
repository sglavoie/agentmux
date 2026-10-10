#!/bin/bash
# Runs on the Studio. Sends the driver to the MacBook, runs the steps there,
# and brings back what they wrote. Nothing is shown or driven on the Studio.
# usage: run.sh REPO OUTDIR STEP...
#   setup     build the world around the packaged Release app (needs `just mac-app Release`)
#   tmux      drive_tmux.sh      -> isolated-tmux.log
#   kitty     drive_kitty.sh     -> isolated-kitty-legacy.log, isolated-kitty-tmux.log
#   packaged  drive_packaged.sh  -> packaged-workspace.log
#   cockpit   drive_cockpit.sh   -> isolated-install-cockpit.log
#   down      end the world, remove it, and show the MacBook user's own state is unchanged
set -u
repo=$1; out=$2; shift 2
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"
mkdir -p "$out"
push() { $SSH "$H" "mkdir -p $R/driver" </dev/null && rsync -a --delete -e "$SSH" "$here/" "$H:$R/driver/" </dev/null; }
pull() { rsync -a -e "$SSH" "$H:$R/out/" "$out/" </dev/null; }
for step in "$@"; do
  case $step in
  setup)
    "$repo/macos/scripts/remote-ui.sh" sync || exit 1
    copy=$("$repo/macos/scripts/remote-ui.sh" dir)
    push || exit 1
    rs setup.sh "\$HOME/$copy" 2>&1 | tee "$out/setup.log"
    ;;
  tmux) push; rs drive_tmux.sh $R/out/isolated-tmux.log; pull ;;
  kitty) push; rs drive_kitty.sh $R/out/isolated-kitty-legacy.log $R/out/isolated-kitty-tmux.log; pull ;;
  packaged) push; rs drive_packaged.sh $R/out/packaged-workspace.log; pull ;;
  cockpit) push; rs drive_cockpit.sh $R/out/isolated-install-cockpit.log; pull ;;
  down)
    push; rs down.sh $R/out
    $SSH "$H" "cat /tmp/amxs19-teardown.log; rm -f /tmp/amxs19-teardown.log" </dev/null > "$out/teardown.log"
    cat "$out/teardown.log"
    ;;
  *) echo "unknown step $step"; exit 1 ;;
  esac
done
