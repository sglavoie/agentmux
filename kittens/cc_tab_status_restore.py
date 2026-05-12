import json
import shutil
import subprocess
from typing import Any

from cc_dashboard.config_accounts import _palette_tab_colors
from cc_tab_status_state import SessionInfo


def schedule_color_restore(
    socket: str,
    tab_id: int,
    cc_color: str,
    cc_account: str,
    cc_config_dir: str,
) -> None:
    """Spawn a detached Python process to re-apply tab colors after overlay closes."""
    script = (
        "import json, os, subprocess, sys, time, tomllib\n"
        "time.sleep(0.5)\n"
        f"tab_id = {tab_id!r}\n"
        f"socket = {socket!r}\n"
        f"cc_color = {cc_color!r}\n"
        f"cc_account = {cc_account!r}\n"
        f"cc_config_dir = {cc_config_dir!r}\n"
        "to_args = ['--to', socket] if socket else []\n"
        "palette = None\n"
        "for d in [\n"
        "    os.path.join(os.path.dirname(os.path.realpath(\n"
        "        sys.argv[0] if sys.argv else __file__)),\n"
        "        '..', '..', 'data'),\n"
        "    os.path.expanduser('~/.local/lib/cc-manager'),\n"
        "]:\n"
        "    p = os.path.join(d, 'palette.json')\n"
        "    if os.path.isfile(p):\n"
        "        with open(p) as f: palette = json.load(f)\n"
        "        break\n"
        "if not palette: sys.exit(1)\n"
        "color_name = 'gray'\n"
        "colors = palette.get('colors', {})\n"
        "if cc_color and cc_color in colors:\n"
        "    color_name = cc_color\n"
        "elif cc_account:\n"
        "    for ap in [\n"
        "        os.environ.get('CC_MANAGER_ACCOUNTS', ''),\n"
        "        os.path.join(os.environ.get(\n"
        "            'XDG_CONFIG_HOME',\n"
        "            os.path.expanduser('~/.config')),\n"
        "            'cc-manager', 'accounts.toml'),\n"
        "    ]:\n"
        "        if ap and os.path.isfile(ap):\n"
        "            with open(ap, 'rb') as f: accts = tomllib.load(f)\n"
        "            for e in accts.get('accounts', []):\n"
        "                if e.get('name') == cc_account:\n"
        "                    color_name = e.get('color', 'gray')\n"
        "                    break\n"
        "            break\n"
        "elif cc_config_dir:\n"
        "    for ap in [\n"
        "        os.environ.get('CC_MANAGER_ACCOUNTS', ''),\n"
        "        os.path.join(os.environ.get(\n"
        "            'XDG_CONFIG_HOME',\n"
        "            os.path.expanduser('~/.config')),\n"
        "            'cc-manager', 'accounts.toml'),\n"
        "    ]:\n"
        "        if ap and os.path.isfile(ap):\n"
        "            with open(ap, 'rb') as f: accts = tomllib.load(f)\n"
        "            for e in accts.get('accounts', []):\n"
        "                d = os.path.expanduser(e.get('config_dir', ''))\n"
        "                if d == cc_config_dir:\n"
        "                    color_name = e.get('color', 'gray')\n"
        "                    break\n"
        "            break\n"
        "c = colors.get(color_name, colors.get('gray', {}))\n"
        "afg = palette.get('active_fg', '#ffffff')\n"
        "ifg = palette.get('inactive_fg', '#aaaaaa')\n"
        "subprocess.run(['kitten', '@'] + to_args + [\n"
        "    'set-tab-color', '--match', f'id:{tab_id}',\n"
        "    f'active_bg={c[\"active_bg\"]}', f'active_fg={afg}',\n"
        "    f'inactive_bg={c[\"inactive_bg\"]}', f'inactive_fg={ifg}',\n"
        "])\n"
    )
    python = shutil.which("python3") or "python3"
    subprocess.Popen(
        [python, "-c", script],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )


def build_restore_payload(session: SessionInfo) -> str:
    """Build the backup color-restore payload returned from main()."""
    color_args = _palette_tab_colors(
        session.get("account", ""),
        color_override=session.get("cc_color", ""),
        config_dir_override=session.get("cc_config_dir", ""),
    )
    return json.dumps({"tab_id": session["tab_id"], "color_args": color_args})


def handle_restore_result(answer: str, boss: Any) -> None:
    """Backup: re-apply tab colors after overlay closes via the boss object."""
    if not answer:
        return
    try:
        data = json.loads(answer)
    except (json.JSONDecodeError, TypeError):
        return

    tab_id = data.get("tab_id")
    color_args = data.get("color_args", [])
    if tab_id and color_args:
        boss.call_remote_control(
            None,
            ("set-tab-color", "--match", f"id:{tab_id}", *color_args),
        )
