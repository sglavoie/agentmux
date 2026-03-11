"""Minimal pure-Python TOML serializer for cc-manager config files.

Handles the subset of TOML needed by accounts.toml, sessions.toml, and
defaults.toml: strings, integers, booleans, arrays of strings, [table],
and [[array_of_tables]].  No third-party dependencies.
"""


def _format_value(value):
    """Format a single Python value as a TOML value string."""
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, str):
        escaped = (
            value.replace("\\", "\\\\")
            .replace('"', '\\"')
            .replace("\n", "\\n")
            .replace("\t", "\\t")
            .replace("\r", "\\r")
        )
        return f'"{escaped}"'
    if isinstance(value, list):
        items = ", ".join(_format_value(v) for v in value)
        return f"[{items}]"
    msg = f"unsupported TOML value type: {type(value).__name__}"
    raise TypeError(msg)


def dumps(data, header=""):
    """Serialize *data* to a TOML string.

    *data* is a dict whose values are either:
      - plain dicts   → rendered as ``[table]`` sections
      - lists of dicts → rendered as ``[[array_of_tables]]`` sections

    *header* is an optional comment block (with ``#`` prefixes) prepended
    verbatim before the TOML body.
    """
    parts = []
    if header:
        text = header.rstrip("\n")
        parts.append(text)
        parts.append("")

    for section_name, section_value in data.items():
        if isinstance(section_value, list):
            # [[array_of_tables]]
            for entry in section_value:
                parts.append(f"[[{section_name}]]")
                for key, val in entry.items():
                    parts.append(f"{key} = {_format_value(val)}")
                parts.append("")
        elif isinstance(section_value, dict):
            # [table]
            parts.append(f"[{section_name}]")
            for key, val in section_value.items():
                parts.append(f"{key} = {_format_value(val)}")
            parts.append("")
        else:
            msg = (
                f"top-level value for '{section_name}' must be a dict or list of dicts"
            )
            raise TypeError(msg)

    return "\n".join(parts)
