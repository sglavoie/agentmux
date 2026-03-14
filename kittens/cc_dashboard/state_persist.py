"""Persistent UI state for the dashboard (sort, filters, collapsed groups, etc.)."""

import contextlib
import json
import os
import sys
import tempfile


def _state_file_path():
    """Return the path to the dashboard state cache file."""
    cache = os.environ.get(
        "XDG_CACHE_HOME", os.path.join(os.path.expanduser("~"), ".cache")
    )
    return os.path.join(cache, "cc-manager", "dashboard_state.json")


_DEFAULTS: dict[str, str | list[str] | bool | None] = {
    "sort_mode": "default",
    "account_filter": None,
    "group_filter": None,
    "collapsed_groups": [],
    "show_detail": False,
    "show_all": False,
}


def load_ui_state():
    """Load persisted UI state from disk.

    Returns a dict with all expected keys, falling back to defaults for
    any missing or corrupt values.
    """
    path = _state_file_path()
    if not os.path.isfile(path):
        return dict(_DEFAULTS)
    try:
        with open(path) as f:
            data = json.load(f)
        if not isinstance(data, dict):
            return dict(_DEFAULTS)
    except (json.JSONDecodeError, OSError):
        return dict(_DEFAULTS)

    result: dict[str, str | list[str] | bool | None] = {}
    for key, default in _DEFAULTS.items():
        val = data.get(key, default)
        if isinstance(default, bool):
            result[key] = val if isinstance(val, bool) else default
        elif isinstance(default, list):
            result[key] = val if isinstance(val, list) else default
        elif default is None:
            # account_filter / group_filter: None or list of strings
            if val is None or isinstance(val, list):
                result[key] = val
            else:
                result[key] = default
        else:
            result[key] = val if isinstance(val, str) else default
    return result


def save_ui_state(state_dict):
    """Write UI state to the cache file atomically, creating the directory if needed."""
    path = _state_file_path()
    dir_ = os.path.dirname(path)
    os.makedirs(dir_, exist_ok=True)
    tmp_path = None
    try:
        with tempfile.NamedTemporaryFile(
            "w", dir=dir_, delete=False, suffix=".tmp"
        ) as tmp:
            json.dump(state_dict, tmp, indent=2)
            tmp.write("\n")
            tmp_path = tmp.name
        os.replace(tmp_path, path)
        tmp_path = None
    except OSError as exc:
        print(f"Warning: could not save UI state: {exc}", file=sys.stderr)
        if tmp_path:
            with contextlib.suppress(OSError):
                os.remove(tmp_path)


def prune_stale_entries(persisted, valid_accounts, valid_groups):
    """Remove references to deleted accounts or groups from persisted state.

    Modifies *persisted* in place.
    """
    af = persisted.get("account_filter")
    if isinstance(af, list):
        pruned = [a for a in af if a in valid_accounts]
        persisted["account_filter"] = pruned if pruned else None

    gf = persisted.get("group_filter")
    if isinstance(gf, list):
        pruned = [g for g in gf if g in valid_groups]
        persisted["group_filter"] = pruned if pruned else None

    cg = persisted.get("collapsed_groups")
    if isinstance(cg, list):
        persisted["collapsed_groups"] = [g for g in cg if g in valid_groups]
