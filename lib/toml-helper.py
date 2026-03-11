#!/usr/bin/env python3
"""CLI bridge between Bash scripts and TOML config files.

Commands:
    read-accounts <file>   Output TSV: name, config_dir, color
    read-sessions <file>   Output TSV: title, dir, account, flags, pw, env, group
    read-defaults <file>   Output TSV: key, value
    write-sessions <file>  Read JSON from stdin, write sessions.toml
"""

import json
import os
import sys

# The lib/ directory containing this script also contains the modules.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import toml_reader
import toml_writer


def cmd_read_accounts(path):
    for acct in toml_reader.read_accounts(path):
        print(f"{acct['name']}\t{acct['config_dir']}\t{acct['color']}")


def cmd_read_sessions(path):
    for s in toml_reader.read_sessions(path):
        print(
            f"{s['title']}\t{s['dir']}\t{s['account']}"
            f"\t{s['flags']}\t{s['pw']}\t{s['env']}\t{s['group']}"
        )


def cmd_read_defaults(path):
    defaults, notifications = toml_reader.read_defaults(path)
    for key, val in defaults.items():
        print(f"{key}\t{val}")
    # Map notification keys back to the notify_* naming Bash expects.
    _notify_key_map = {
        "error": "notify_error",
        "permission": "notify_permission",
        "stop": "notify_stop",
        "idle": "notify_idle",
        "idle_alert_minutes": "idle_alert_minutes",
        "min_secs": "notify_min_secs",
        "ignore_patterns": "notify_ignore_patterns",
    }
    for key, val in notifications.items():
        bash_key = _notify_key_map.get(key, key)
        if isinstance(val, bool):
            print(f"{bash_key}\t{'on' if val else ''}")
        elif isinstance(val, list):
            print(f"{bash_key}\t{'|'.join(str(v) for v in val)}")
        else:
            print(f"{bash_key}\t{val}")


def cmd_write_sessions(path):
    """Read JSON array from stdin and write sessions.toml."""
    try:
        raw = json.load(sys.stdin)
    except json.JSONDecodeError as e:
        print(f"Error: invalid JSON on stdin: {e}", file=sys.stderr)
        sys.exit(1)
    header = ""
    if os.path.isfile(path):
        header = toml_reader.read_header_comments(path)

    sessions = []
    for entry in raw:
        d = {"title": entry.get("title", ""), "directory": entry.get("dir", "")}
        if entry.get("account"):
            d["account"] = entry["account"]
        if entry.get("flags"):
            d["flags"] = entry["flags"]
        if entry.get("pw"):
            d["project_window"] = entry["pw"]
        if entry.get("env"):
            d["env"] = entry["env"]
        if entry.get("group"):
            d["group"] = entry["group"]
        sessions.append(d)

    try:
        with open(path, "w") as f:
            f.write(toml_writer.dumps({"sessions": sessions}, header=header))
    except OSError as e:
        print(f"Error: failed to write {path}: {e}", file=sys.stderr)
        sys.exit(1)


def main():
    if len(sys.argv) < 2:
        print(__doc__, file=sys.stderr)
        sys.exit(1)

    cmd = sys.argv[1]
    if cmd == "read-accounts" and len(sys.argv) == 3:
        cmd_read_accounts(sys.argv[2])
    elif cmd == "read-sessions" and len(sys.argv) == 3:
        cmd_read_sessions(sys.argv[2])
    elif cmd == "read-defaults" and len(sys.argv) == 3:
        cmd_read_defaults(sys.argv[2])
    elif cmd == "write-sessions" and len(sys.argv) == 3:
        cmd_write_sessions(sys.argv[2])
    else:
        print(__doc__, file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
