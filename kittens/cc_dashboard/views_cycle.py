"""Cycling helpers for dashboard form fields."""

from cc_dashboard import config as _cfg

_PW_OPTIONS = ["none", "stack", "vsplit", "hsplit", "split"]


def _cycle_account(current):
    """Return the next account name in the loaded list, wrapping around."""
    if not _cfg._account_names:
        return current
    try:
        idx = _cfg._account_names.index(current)
    except ValueError:
        idx = -1
    return _cfg._account_names[(idx + 1) % len(_cfg._account_names)]


def _cycle_pw(current):
    """Return the next project window option, wrapping around."""
    try:
        idx = _PW_OPTIONS.index(current)
    except ValueError:
        idx = -1
    return _PW_OPTIONS[(idx + 1) % len(_PW_OPTIONS)]


def _cycle_instance(current, instance_labels):
    """Return the next instance label from the list, wrapping around."""
    if not instance_labels:
        return current
    try:
        idx = instance_labels.index(current)
    except ValueError:
        idx = -1
    return instance_labels[(idx + 1) % len(instance_labels)]
