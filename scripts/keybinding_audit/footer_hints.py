"""Source parsing for footer hint definitions."""

from __future__ import annotations

import ast

from .constants import DASHBOARD_DIR


def extract_footer_hints() -> dict[str, list[str]]:
    """Parse `_FOOTER_HINTS` from `views_shared.py` using AST."""
    path = DASHBOARD_DIR / "views_shared.py"
    with path.open(encoding="utf-8") as handle:
        tree = ast.parse(handle.read(), str(path))

    for node in ast.walk(tree):
        if (
            isinstance(node, ast.Assign)
            and len(node.targets) == 1
            and isinstance(node.targets[0], ast.Name)
            and node.targets[0].id == "_FOOTER_HINTS"
        ):
            return ast.literal_eval(node.value)

    raise RuntimeError("Could not find _FOOTER_HINTS in views_shared.py")
