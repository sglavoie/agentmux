#!/usr/bin/env python3
"""CLI bridge between Bash scripts and TOML config files.

Commands:
    read-accounts <file>   Output TSV: name, config_dir, color
    read-sessions <file>   Output TSV: title, dir, account, flags, pw, env, groups, mute, cmds
    read-defaults <file>   Output TSV: key, value
    write-sessions <file>  Read JSON from stdin, write sessions.toml
"""

import contextlib
import json
import os
import sys

# The lib/ directory containing this script also contains the modules.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import toml_reader
import toml_writer


def _normalize_commands(raw_commands):
    """Return a sanitized list of command strings for CLI transport."""
    if raw_commands is None:
        return []
    if not isinstance(raw_commands, list):
        return []
    result = []
    for command in raw_commands:
        if (
            isinstance(command, str)
            and "\t" not in command
            and "\n" not in command
            and "\r" not in command
        ):
            result.append(command)
    return result


def cmd_read_accounts(path):
    for acct in toml_reader.read_accounts(path):
        print(f"{acct['name']}\t{acct['config_dir']}\t{acct['color']}")


def cmd_read_sessions(path):
    for s in toml_reader.read_sessions(path):
        cmds = "\x1f".join(_normalize_commands(s.get("commands")))
        groups = "\x1f".join(s.get("groups", []))
        print(
            f"{s['title']}\t{s['dir']}\t{s['account']}"
            f"\t{s['flags']}\t{s['pw']}\t{s['env']}\t{groups}"
            f"\t{s['mute']}\t{cmds}"
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
        "dim_after_minutes": "dim_after_minutes",
        "idle_poll_interval": "idle_poll_interval",
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
        groups_val = entry.get("groups", [])
        if isinstance(groups_val, str):
            groups_val = [g for g in groups_val.split("\x1f") if g]
        if groups_val:
            d["groups"] = groups_val
        if entry.get("mute") == "true":
            d["mute"] = True
        commands = _normalize_commands(entry.get("commands"))
        if commands:
            d["commands"] = commands
        sessions.append(d)

    tmp_path = path + ".tmp"
    try:
        with open(tmp_path, "w") as f:
            f.write(toml_writer.dumps({"sessions": sessions}, header=header))
        os.replace(tmp_path, path)
    except OSError as e:
        # Clean up temp file on failure
        with contextlib.suppress(OSError):
            os.unlink(tmp_path)
        print(f"Error: failed to write {path}: {e}", file=sys.stderr)
        sys.exit(1)


def main():
    if len(sys.argv) < 2:
        print(__doc__, file=sys.stderr)
        sys.exit(1)

    cmd = sys.argv[1]
    _COMMANDS = {
        "read-accounts": cmd_read_accounts,
        "read-sessions": cmd_read_sessions,
        "read-defaults": cmd_read_defaults,
        "write-sessions": cmd_write_sessions,
    }

    if cmd not in _COMMANDS:
        print(f"Error: unknown command '{cmd}'.", file=sys.stderr)
        print(f"Available commands: {', '.join(_COMMANDS)}", file=sys.stderr)
        sys.exit(1)

    if len(sys.argv) != 3:
        print(f"Error: '{cmd}' requires exactly one FILE argument.", file=sys.stderr)
        print(f"Usage: toml-helper.py {cmd} <file>", file=sys.stderr)
        sys.exit(1)

    _COMMANDS[cmd](sys.argv[2])


if __name__ == "__main__":
    main()
