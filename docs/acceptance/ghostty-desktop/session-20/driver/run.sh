#!/bin/bash
# Runs on the Studio. Rehearses the daily-driver transition and its rollback
# on the MacBook, in the Session 19 world (/tmp/amxs19, built by that
# session's setup.sh around the packaged Release app). Nothing is shown or
# driven on the Studio.
# usage: run.sh REPO OUTDIR STEP...
#   setup       the Session 19 world (needs `just mac-app Release`)
#   transition  drive_transition.sh -> transition-rollback.log
#   down        the Session 19 teardown -> teardown.log
set -u
repo=$1; out=$2; shift 2
here=$(cd "$(dirname "$0")" && pwd)
s19=$here/../../session-19/driver
. "$s19/env.sh"
mkdir -p "$out"
# The world's driver directory is the Session 19 driver plus this one's script.
push() {
  $SSH "$H" "mkdir -p $R/driver" </dev/null &&
    rsync -a --delete -e "$SSH" "$s19/" "$H:$R/driver/" </dev/null &&
    rsync -a -e "$SSH" "$here/drive_transition.sh" "$H:$R/driver/" </dev/null
}
for step in "$@"; do
  case $step in
  setup)
    "$repo/macos/scripts/remote-ui.sh" sync || exit 1
    copy=$("$repo/macos/scripts/remote-ui.sh" dir)
    push || exit 1
    rs setup.sh "\$HOME/$copy" 2>&1 | tee "$out/setup.log"
    ;;
  transition)
    push; rs drive_transition.sh $R/out/transition-rollback.log
    rsync -a -e "$SSH" "$H:$R/out/transition-rollback.log" "$out/" </dev/null
    ;;
  down)
    push; rs down.sh $R/out
    $SSH "$H" "cat /tmp/amxs19-teardown.log; rm -f /tmp/amxs19-teardown.log" </dev/null > "$out/teardown.log"
    cat "$out/teardown.log"
    ;;
  *) echo "unknown step $step"; exit 1 ;;
  esac
done
