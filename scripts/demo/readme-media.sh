#!/usr/bin/env sh
# Export the offline dashboard tour and render its frames as a CSS-animated SVG.
set -eu

case "${1:-}" in
    ''|--check) ;;
    *) echo 'Usage: sh scripts/demo/readme-media.sh [--check]' >&2; exit 2 ;;
esac

cd "$(dirname "$0")/../.."
image=docs/media/demo.svg
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

AMX_TUI_THEME=dark AMX_README_DEMO_DIR="$work" \
    go test -tags tuitest ./tui -run '^TestReadmeDemo$' -count=1
uv run -q scripts/demo/render_svg.py --animate "$work/demo.json" "$work/demo.svg"

if [ "${1:-}" = "--check" ]; then
    if ! cmp -s "$work/demo.svg" "$image"; then
        echo "$image is stale: run 'just readme-media' and commit the result." >&2
        exit 1
    fi
    echo "$image is current."
else
    cp "$work/demo.svg" "$image"
    echo "Wrote $image."
fi
