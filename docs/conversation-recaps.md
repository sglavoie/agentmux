# Conversation recaps

The macOS workspace's **Session** inspector tab and **Conversation History**
automatically show a short recap for the selected conversation:

- **Goal:** what the user is trying to accomplish.
- **Latest:** the most recent meaningful outcome, attributed to the agent when
  it is a reported result.
- **Outstanding:** explicitly unfinished work or unanswered decisions. This
  stays absent when the source messages give no evidence.

You do not need to write or maintain notes. Each field's **Supporting messages**
expands a list of citations; select a message to open it in Conversation History.
A recap's timestamp and profile/model are visible; excerpts and stale saved
results are labeled.
History's **Recap** button also opens it without moving your reading position.

The app's **Project Briefing…** brings together saved recaps for one project's
live conversations and ten most recent conversations, plus older indexed
members of their recorded handoff threads. Each thread groups related
conversations while preserving separate recaps and source links. Opening or refreshing
the briefing only reads the cache; use **Generate Recap** or **Regenerate** on a
conversation to request generation. Source links, stale labels, and the same
shared request locks apply there too.

## Generation and configuration

Recaps use one configured **Claude** profile for source conversations from
Claude, Codex, and Copilot. The default selects the first Claude profile in
configuration and the `haiku` model alias. No additional API key is required
when that profile already has working Claude Code authentication.

To choose another profile/model, or turn generation off, edit the existing
configuration with `amx config edit`:

```toml
[defaults.recaps]
enabled = true
profile = "personal-claude"
model = "haiku"
```

`profile` must name an existing Claude profile. Omitting the entire block uses
the defaults above. Without a Claude profile the view explains what is missing
and makes no model requests. Set `enabled = false` to stop generation; saved
recaps can still be displayed. The next selection or refresh reads new settings.

Generation makes a separate model request and uses the selected profile's
service/quota. The bounded source text is sent through that profile's provider;
storing the result locally does not make generation offline.

The worker starts a fresh, noninteractive Claude process in a temporary
directory. It disables tools, MCP, hooks, skills, customizations and session
persistence, and removes Agent Mux/session identity from its environment. It
does not inherit project/profile launch arguments, send a prompt into a working
session, or change the original transcript. The CLI must support `--safe-mode`
and the isolation flags; an older version gets an update message. These flags
are described in the [Claude CLI reference](https://code.claude.com/docs/en/cli-reference).

## When requests run

Opening a conversation first reads its saved recap. An unchanged conversation
reuses it. A missing or stale recap starts generation after a short selection
debounce, only while that view is open. A selected live session waits until
its agent finishes its turn. Opening History does not summarize the archive.

The app and CLI share the cache and worker locks. Only one model request runs
at once, and duplicate requests for the same conversation do not start another
model. A failed request retains an older recap when available and records a
one-minute automatic-retry cooldown; the refresh button explicitly retries.
A model request has a 90-second deadline. Switching away discards its UI reply;
an already running request may finish and populate the cache.

## Source and storage

Input contains the opening two and latest twelve visible user/assistant prose
messages, with long messages excerpted. Its JSON is capped at 32 KiB. Raw tool
payloads and hidden reasoning are excluded using the transcript readers' handoff
rules. Omitted content is labeled. The worker validates that each nonempty
field cites messages actually supplied to the model. Message links identify
supporting source text; they do not independently verify an agent's claims.

The cache fingerprint covers all visible conversation prose, the summarizer
profile/home, model, and recap format version. Transcript edits, truncation,
new messages, or a summarizer change invalidate it. A result arriving after
its source changes is discarded. Supporting-message links are disabled for
stale recaps until they are refreshed. The citation list still expands and explains
why its links are unavailable.
A late transcript write gets one automatic retry while the view remains selected.

Private, disposable recap files live under `$XDG_CACHE_HOME/agent-mux/recaps`,
falling back to `~/.cache/agent-mux/recaps`, with directory mode 0700 and files
0600. These files contain generated text and evidence indices; they are
separate from Flight Log and do not create sessions, milestones or activity
notifications. Regenerating a lost cache requires another model request.

## CLI

```sh
amx session recap codex:<conversation-id>
amx session recap codex:<conversation-id> --cached --json
amx session recap codex:<conversation-id> --refresh
```

Selectors resolve exactly as `amx session show` does. `--cached` never starts a
model; `--refresh` forces regeneration or retries a failed request. They cannot
be combined. JSON uses the standard schema-v1 envelope and includes availability
status, any saved recap, warnings and retry time. Each field carries zero-based
visible message indices, which `amx session show --from` can open.
