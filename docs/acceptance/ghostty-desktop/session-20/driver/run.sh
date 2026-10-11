#!/bin/bash
# Runs on the Studio. Rehearses the daily-driver transition and its rollback
# on the MacBook, in the Session 19 world (/tmp/amxs19, built by that
# session's setup.sh around the packaged Release app). Nothing is shown or
# driven on the Studio.
# usage: run.sh REPO OUTDIR STEP...
#   setup       the Session 19 world (needs `just mac-app Release`)
#   transition  drive_transition.sh -> transition-rollback.log
#   old         build the earlier release (amx and app at OLD_REV) for the rollback: amx here, headless, from a
#               detached worktree under /tmp; the app on the MacBook, in /tmp/amxs20-old
#   rollback4   drive_rollback4.sh -> rollback-step4.log
#   down        the Session 19 teardown -> teardown.log (also removes /tmp/amxs20-old)
set -u
repo=$1; out=$2; shift 2
here=$(cd "$(dirname "$0")" && pwd)
s19=$here/../../session-19/driver
. "$s19/env.sh"
OLD_REV=${OLD_REV:-5645f028f120} # what the operator ran before the candidate
OLD=/tmp/amxs20-old              # on both Macs
mkdir -p "$out"
# The world's driver directory is the Session 19 driver plus this one's script.
push() {
  $SSH "$H" "mkdir -p $R/driver" </dev/null &&
    rsync -a --delete -e "$SSH" "$s19/" "$H:$R/driver/" </dev/null &&
    rsync -a -e "$SSH" "$here/drive_transition.sh" "$here/drive_rollback4.sh" "$H:$R/driver/" </dev/null
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
  old)
    rm -rf "$OLD"; git -C "$repo" worktree prune
    git -C "$repo" worktree add --detach "$OLD" "$OLD_REV" >/dev/null || exit 1
    (cd "$OLD" && go build -o amx . && ./amx --version) 2>&1 | tee "$out/old-build.log" || exit 1
    $SSH "$H" "rm -rf $OLD && mkdir -p $OLD/macos/.cache" </dev/null || exit 1
    rsync -a -e "$SSH" --exclude '/.git' "$OLD/" "$H:$OLD/" </dev/null || exit 1
    for tool in "$repo"/macos/.cache/xcodegen-*; do [ -d "$tool" ] && rsync -a -e "$SSH" "$tool" "$H:$OLD/macos/.cache/" </dev/null; done
    $SSH "$H" "cd $OLD && exec /bin/zsh -lc 'AMX_BINARY=$OLD/amx AMX_CODESIGN_IDENTITY=- macos/scripts/build-app.sh Release'" </dev/null 2>&1 | tee -a "$out/old-build.log"
    git -C "$repo" worktree remove --force "$OLD"; git -C "$repo" worktree prune
    ;;
  rollback4)
    push; rs drive_rollback4.sh $R/out/rollback-step4.log
    rsync -a -e "$SSH" "$H:$R/out/rollback-step4.log" "$out/" </dev/null
    ;;
  down)
    push; rs down.sh $R/out
    $SSH "$H" "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -u '$R/Applications/Agent Mux.app' >/dev/null 2>&1; rm -rf $OLD" </dev/null
    $SSH "$H" "cat /tmp/amxs19-teardown.log; rm -f /tmp/amxs19-teardown.log" </dev/null > "$out/teardown.log"
    cat "$out/teardown.log"
    ;;
  *) echo "unknown step $step"; exit 1 ;;
  esac
done
