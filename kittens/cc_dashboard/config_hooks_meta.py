"""Hook event metadata and examples for dashboard configuration."""

from __future__ import annotations

from cc_dashboard.config_data import load_hook_events as _load_hook_events

HOOK_EVENTS: list[str] = _load_hook_events()

HOOK_EVENT_META = {
    "SessionStart": {
        "abbr": "Strt",
        "desc": "Fires when a session starts or resumes",
        "matcher_desc": "session start type",
        "matcher_examples": "startup|resume|clear|compact",
    },
    "UserPromptSubmit": {
        "abbr": "Pmpt",
        "desc": "Fires when the user submits a prompt",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "PreToolUse": {
        "abbr": "PreT",
        "desc": "Fires before a tool executes",
        "matcher_desc": "tool name",
        "matcher_examples": (
            "Bash|Edit|Write|Read|Glob|Grep|Agent|WebFetch|WebSearch|mcp__.*"
        ),
    },
    "PermissionRequest": {
        "abbr": "Perm",
        "desc": "Fires when a tool requests permission",
        "matcher_desc": "tool name",
        "matcher_examples": "Bash|Edit|Write|Read|Glob|Grep|Agent|WebFetch|WebSearch",
    },
    "PostToolUse": {
        "abbr": "Tool",
        "desc": "Fires after a tool executes successfully",
        "matcher_desc": "tool name",
        "matcher_examples": "Bash|Edit|Write|Read|Glob|Grep|Agent|WebFetch|WebSearch",
    },
    "PostToolUseFailure": {
        "abbr": "Fail",
        "desc": "Fires after a tool execution fails",
        "matcher_desc": "tool name",
        "matcher_examples": "Bash|Edit|Write|Read|Glob|Grep|Agent|WebFetch|WebSearch",
    },
    "Notification": {
        "abbr": "Ntfy",
        "desc": "Fires on permission or idle notifications",
        "matcher_desc": "notification type",
        "matcher_examples": (
            "permission_prompt|idle_prompt|auth_success|elicitation_dialog"
        ),
    },
    "SubagentStart": {
        "abbr": "AgSt",
        "desc": "Fires when a subagent starts",
        "matcher_desc": "agent type",
        "matcher_examples": "Bash|Explore|Plan",
    },
    "SubagentStop": {
        "abbr": "AgSp",
        "desc": "Fires when a subagent stops",
        "matcher_desc": "agent type",
        "matcher_examples": "Bash|Explore|Plan",
    },
    "Stop": {
        "abbr": "Stop",
        "desc": "Fires when Claude stops generating",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "TeammateIdle": {
        "abbr": "TmId",
        "desc": "Fires when a teammate becomes idle",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "TaskCompleted": {
        "abbr": "Task",
        "desc": "Fires when a task is completed",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "ConfigChange": {
        "abbr": "Conf",
        "desc": "Fires when settings change",
        "matcher_desc": "config source",
        "matcher_examples": (
            "user_settings|project_settings|local_settings|policy_settings|skills"
        ),
    },
    "WorktreeCreate": {
        "abbr": "WkCr",
        "desc": "Fires when a worktree is created",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "WorktreeRemove": {
        "abbr": "WkRm",
        "desc": "Fires when a worktree is removed",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "PreCompact": {
        "abbr": "Cmpt",
        "desc": "Fires before context compaction",
        "matcher_desc": "trigger type",
        "matcher_examples": "manual|auto",
    },
    "SessionEnd": {
        "abbr": "End",
        "desc": "Fires when a session ends",
        "matcher_desc": "exit reason",
        "matcher_examples": (
            "clear|logout|prompt_input_exit|bypass_permissions_disabled|other"
        ),
    },
}

HOOK_EVENT_EXAMPLES = {
    "SessionStart": {
        "type": "command",
        "command": "echo 'Session started' >> ~/claude-sessions.log",
        "matcher": "startup",
        "async": True,
    },
    "UserPromptSubmit": {
        "type": "command",
        "command": "date +%s > /tmp/claude-last-prompt",
        "async": True,
    },
    "PreToolUse": {
        "type": "command",
        "command": "echo 'About to run Bash' >> ~/claude-tools.log",
        "matcher": "Bash",
    },
    "PermissionRequest": {
        "type": "command",
        "command": "afplay /System/Library/Sounds/Tink.aiff",
        "matcher": "Write",
        "async": True,
    },
    "PostToolUse": {
        "type": "command",
        "command": 'echo "Tool done: $CLAUDE_TOOL_NAME" >> ~/claude-tools.log',
        "matcher": "Bash",
    },
    "PostToolUseFailure": {
        "type": "command",
        "command": (
            'osascript -e \'display notification "Tool failed" with title "Claude"\''
        ),
        "async": True,
    },
    "Notification": {
        "type": "command",
        "command": "cc-hook-indicator permission",
        "matcher": "permission_prompt",
    },
    "SubagentStart": {
        "type": "command",
        "command": "echo 'Agent started' >> ~/claude-agents.log",
        "async": True,
    },
    "SubagentStop": {
        "type": "command",
        "command": "echo 'Agent stopped' >> ~/claude-agents.log",
        "async": True,
    },
    "Stop": {
        "type": "command",
        "command": "cc-hook-indicator idle",
        "async": True,
    },
    "TeammateIdle": {
        "type": "command",
        "command": (
            'osascript -e \'display notification "Teammate idle" with title "Claude"\''
        ),
        "async": True,
    },
    "TaskCompleted": {
        "type": "command",
        "command": (
            'osascript -e \'display notification "Task done" with title "Claude"\''
        ),
        "async": True,
    },
    "ConfigChange": {
        "type": "command",
        "command": "echo 'Config changed' >> ~/claude-config.log",
        "matcher": "user_settings",
        "async": True,
    },
    "WorktreeCreate": {
        "type": "command",
        "command": "echo 'Worktree created' >> ~/claude-worktrees.log",
        "async": True,
    },
    "WorktreeRemove": {
        "type": "command",
        "command": "echo 'Worktree removed' >> ~/claude-worktrees.log",
        "async": True,
    },
    "PreCompact": {
        "type": "command",
        "command": "cp ~/.claude/context.json ~/.claude/context-backup.json",
        "matcher": "auto",
        "async": True,
    },
    "SessionEnd": {
        "type": "command",
        "command": 'echo "Session ended: $(date)" >> ~/claude-sessions.log',
        "async": True,
    },
}

_FULL_TYPE_EVENTS = {
    "PermissionRequest",
    "PostToolUse",
    "PostToolUseFailure",
    "PreToolUse",
    "Stop",
    "SubagentStop",
    "TaskCompleted",
    "UserPromptSubmit",
}
