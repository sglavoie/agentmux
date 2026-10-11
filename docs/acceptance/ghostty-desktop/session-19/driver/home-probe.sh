#!/usr/bin/env bash
# Runs each Go package's tests with HOME pointed at a disposable home seeded
# with sentinels, and reports every package that created or changed anything
# under it. Never touches the real home.
set -uo pipefail
repo=$1; shift
work=${AMX_PROBE_DIR:-$(mktemp -d /tmp/amx-home-probe.XXXXXX)}; out=$work/report.txt; : > "$out"
gopath=$(go env GOPATH); gomod=$(go env GOMODCACHE); gocache=$(go env GOCACHE)
seed() {
  rm -rf $work/home; mkdir -p $work/home/.local/share/agent-mux $work/home/.config/agent-mux $work/home/.local/state
  printf '{"version":2,"entries":[{"project_title":"SENTINEL-PROJECT","profile_name":"sentinel"}],"pinned_projects":[{"profile_name":"sentinel","project_title":"SENTINEL-PROJECT"}]}\n' > $work/home/.local/share/agent-mux/launch_overrides.json
  echo SENTINEL > $work/home/.local/share/agent-mux/transcripts.json
  echo '# SENTINEL' > $work/home/.config/agent-mux/config.toml
}
listing() { (cd $work/home && find . -path ./Library -prune -o \( -type f -o -type d -o -type s \) -print | sort | while read -r p; do if [ -f "$p" ]; then printf '%s %s\n' "$(shasum -a 256 "$p" | cut -c1-12)" "$p"; else printf 'dir %s\n' "$p"; fi; done); }
cd "$repo"
for pkg in "$@"; do
  seed; listing > $work/before
  env -u AMX_OVERRIDES_PATH -u AMX_CONFIG -u XDG_DATA_HOME -u XDG_STATE_HOME -u XDG_CONFIG_HOME HOME=$work/home GOPATH="$gopath" GOMODCACHE="$gomod" GOCACHE="$gocache" go test -count=1 "$pkg" > $work/last.log 2>&1
  code=$?
  listing > $work/after
  if ! diff $work/before $work/after > $work/d; then
    { echo "== $pkg (exit $code)"; cat $work/d; } >> "$out"
  elif [ $code -ne 0 ]; then echo "== $pkg FAILED (exit $code) with no home change" >> "$out"; fi
done
echo done >> "$out"
cat "$out"
