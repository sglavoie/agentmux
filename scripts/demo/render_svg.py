# /// script
# requires-python = ">=3.10"
# dependencies = ["rich==14.2.0"]
# ///
"""Render the README's exported ANSI frames as SVG, preserving terminal styles.

Usage:
  uv run scripts/demo/render_svg.py INPUT.ansi OUTPUT.svg
  uv run scripts/demo/render_svg.py --animate INPUT.json OUTPUT.svg

The animated form reads the frames TestReadmeDemo exports, each an ANSI screen
with a hold time and a caption, and stacks them in one SVG whose CSS shows one
frame at a time, so the README plays it without a GIF or a script.
"""

# Adapted from Terminal Tasks (tt), Copyright (c) 2026 Sébastien Lavoie.
# Distributed under the MIT license in the repository root.

import html
import io
import json
import re
import sys
from pathlib import Path

from rich._export_format import CONSOLE_SVG_FORMAT
from rich.console import Console
from rich.terminal_theme import TerminalTheme
from rich.text import Text

TITLE = "Agent Mux"

# Match Agent Mux's dark palette; explicit ANSI colors retain their original values.
PALETTE = TerminalTheme(
    (21, 27, 34), (242, 244, 247),
    [(26, 27, 38), (247, 118, 142), (158, 206, 106), (224, 175, 104),
     (122, 162, 247), (187, 154, 247), (125, 207, 255), (192, 202, 245)],
)

# The terminal body of rich's template: everything one animation frame replaces.
TERMINAL_BODY = """    <g transform="translate({terminal_x}, {terminal_y})" clip-path="url(#{unique_id}-clip-terminal)">
    {backgrounds}
    <g class="{unique_id}-matrix">
    {matrix}
    </g>
    </g>"""

# One animation frame: its own styles and line clips, then the terminal body.
# Each frame keeps its own unique_id, since rich numbers style classes per export.
FRAME_FORMAT = """<style>
.{unique_id}-matrix {{ font-family: ui-monospace, Menlo, Consolas, monospace; font-size: {char_height}px; line-height: {line_height}px; font-variant-east-asian: full-width; }}
{styles}
</style>
<defs>{lines}</defs>
<g class="amx-frame amx-f@INDEX@">
<text class="amx-caption" text-anchor="end" x="@CAPTION_X@" y="27">@CAPTION@</text>
""" + TERMINAL_BODY.replace("{unique_id}-clip-terminal", "amx-clip-terminal") + "\n</g>"


def export(ansi, code_format=CONSOLE_SVG_FORMAT, unique_id=None):
    console = Console(
        file=io.StringIO(), record=True, width=100, color_system="truecolor"
    )
    console.print(Text.from_ansi(ansi), end="", soft_wrap=True)
    svg = console.export_svg(
        title=TITLE, theme=PALETTE, code_format=code_format, unique_id=unique_id
    )
    svg = svg.replace("Fira Code, monospace", "ui-monospace, Menlo, Consolas, monospace")
    # Keep the checked-in image self-contained; use the viewer's monospace font.
    return re.sub(r"@font-face\s*\{[^}]*\}", "", svg)


def animate(frames):
    total = sum(frame["hold_ms"] for frame in frames)
    width = int(re.search(r'viewBox="0 0 ([\d.]+)', export(frames[0]["ansi"], unique_id="amx")).group(1))
    rules = [
        f".amx-frame {{ visibility: hidden; animation: {total}ms step-end infinite; }}",
        ".amx-caption { font: bold 16px ui-monospace, Menlo, monospace; fill: #4ec9b0; }",
    ]
    bodies = []
    start = 0
    for index, frame in enumerate(frames):
        end = start + frame["hold_ms"]
        rules.append(
            f".amx-f{index} {{ animation-name: amx-k{index}; }} "
            f"@keyframes amx-k{index} {{ 0% {{ visibility: hidden; }} "
            f"{100 * start / total:.3f}% {{ visibility: visible; }} "
            f"{100 * end / total:.3f}% {{ visibility: {'visible' if end == total else 'hidden'}; }} }}"
        )
        body = export(frame["ansi"], FRAME_FORMAT, unique_id=f"amx{index}")
        bodies.append(
            body.replace("@INDEX@", str(index))
            .replace("@CAPTION_X@", str(width - 24))
            .replace("@CAPTION@", html.escape(frame["caption"]).replace(" ", "&#160;"))
        )
        start = end
    # Readers who asked for less motion get the opening frame, held.
    rules.append(
        "@media (prefers-reduced-motion: reduce) { .amx-frame { animation: none; } .amx-f0 { visibility: visible; } }"
    )
    outer = CONSOLE_SVG_FORMAT.replace(TERMINAL_BODY, "@FRAMES@").replace(
        "    </style>", "    @RULES@\n    </style>"
    )
    svg = export(frames[0]["ansi"], outer, unique_id="amx")
    svg = svg.replace('<svg ', '<svg role="img" aria-labelledby="demo-title demo-description" ', 1)
    svg = svg.replace('    <!-- Generated', '<title id="demo-title">Agent Mux dashboard walkthrough</title>\n'
                      '<desc id="demo-description">Three scripted agent sessions. Inspect their output, '
                      'jump to the session needing permission, filter by harness, switch projects, '
                      'and find commands.</desc>\n    <!-- Generated', 1)
    return svg.replace("@RULES@", "\n".join(rules)).replace("@FRAMES@", "\n".join(bodies))


def main():
    args = sys.argv[1:]
    if args[0] == "--animate":
        source, destination = map(Path, args[1:])
        svg = animate(json.loads(source.read_text()))
    else:
        source, destination = map(Path, args)
        svg = export(source.read_text())
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(svg)


if __name__ == "__main__":
    main()
