"""Minimal pure-Python TOML serializer for cc-manager config files.

Handles the subset of TOML needed by accounts.toml, sessions.toml, and
defaults.toml: strings, integers, floats, booleans, arrays of strings,
[table], and [[array_of_tables]].  No third-party dependencies.
"""


def _format_value(value, *, key="<unknown>"):
    """Format a single Python value as a TOML value string."""
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float):
        return str(value)
    if isinstance(value, str):
        escaped = (
            value.replace("\\", "\\\\")
            .replace('"', '\\"')
            .replace("\b", "\\b")
            .replace("\f", "\\f")
            .replace("\n", "\\n")
            .replace("\t", "\\t")
            .replace("\r", "\\r")
        )
        # Escape remaining control characters that TOML does not allow as
        # literal characters in basic strings.  The named escapes above
        # already handled \b, \t, \n, \f, \r so only the rarer controls
        # (NUL, BEL, VT, etc.) reach this point.  U+007F (DEL) is also
        # forbidden by the TOML spec.
        escaped = "".join(
            f"\\u{ord(c):04X}" if ord(c) <= 0x1F or ord(c) == 0x7F else c
            for c in escaped
        )
        return f'"{escaped}"'
    if isinstance(value, list):
        items = ", ".join(_format_value(v, key=key) for v in value)
        return f"[{items}]"
    msg = f"unsupported TOML value type for key '{key}': {type(value).__name__} (value: {value!r})"
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
                    parts.append(f"{key} = {_format_value(val, key=key)}")
                parts.append("")
        elif isinstance(section_value, dict):
            # [table]
            parts.append(f"[{section_name}]")
            for key, val in section_value.items():
                parts.append(f"{key} = {_format_value(val, key=key)}")
            parts.append("")
        else:
            msg = (
                f"top-level value for '{section_name}' must be a dict or list of dicts"
            )
            raise TypeError(msg)

    return "\n".join(parts)
