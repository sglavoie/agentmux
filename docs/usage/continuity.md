# Dashboard views and continuity

[Back to usage](../usage.md)

The default TUI opens on the live-session dashboard and selects the session
it was opened from when possible: the tmux session a popup is over. It runs in
any terminal ([The tmux backend](launching.md#the-tmux-backend)). `/` filters the live-session
list; `:` opens the command palette, from which **Search All Past Sessions**
opens Past Sessions and **Search Plans** opens Plans.

Beside it, `amx overview` (bound to `F2`) opens the
[live session overview](sessions.md#live-session-overview): a separate program that shows
every managed session at once as a grid of live pane previews.

The same workflows are scriptable. Machine-readable commands use one
`{"schema_version":1,"data":...}` success envelope; failures use a structured
`error` envelope and ambiguous selectors return candidates instead of guessing.
An invocation the CLI cannot parse fails the same way in every command, and
under `--json` as that envelope too: a name that is no command of its group
(`amx session lst`) is `unknown_command`, with the commands it may have meant
as candidates, and a flag that does not parse or an argument a command does
not take is `invalid_arguments`. A bare group (`amx worktree`) prints its help.
`amx version` prints what `amx --version` does.

```bash
amx session list              # a table with relative ages, message counts (prompts and replies with text, not tool calls or the background-task notices Claude Code writes as prompts), short session IDs any command accepts, and each conversation's title (its opening prompt when the harness named it nothing) followed by `→` and the opening of the agent's last reply, so runs of the same prompt read apart, in a terminal, the 25 most recent unless -n says otherwise; every session as tab-separated RFC 3339 rows, with full IDs followed by the opening prompt, the title (empty when there is none), the message count, a LIVE field (`here`, `live`, empty when closed, `?` when the daemon or tmux could not be asked), and that last reply (empty when there was none) when piped
amx session list --json
amx session list --since 7d   # only sessions active in the last week (or 2w, 12h, 90m, …)
amx session list --here -n 5  # the five latest sessions started in this directory or below it
amx session list --harness codex --profile codex-work   # both repeatable
amx session list --project amx   # one project's sessions; "amx" reaches "🐱amx" as project launch reads it, and a directory-named project works too (repeatable)
amx session list --live       # only conversations open in a tmux session; refused, not empty, when the daemon or tmux cannot say
amx session list --all        # also automated sessions (ASL runs, subagents, exec), hidden by default as in Past Sessions
amx session continue "$(amx session list | fzf | cut -f5)"   # pick by opening prompt
amx session search race daemon            # conversations mentioning both words, newest first, with match counts and a matched line (one you or the agent wrote before tool output); the conversation you search from comes last, and a terminal says how many automated sessions also match
amx session search '"race condition"' --here --since 7d   # a quoted phrase; takes every list selector
amx session search --source user flaky    # only your own prompts (or --source harness for the agent's output, including Claude's background-task notices)
amx session show 09467ec1                 # one conversation: title, directory, ages, whether it is live, message count, transcript path, how it ended, and the commands that continue or fork it
cd "$(amx session show 09467ec1 --json | jq -r .data.session.cwd)"
amx session show 09467ec1 --last 4       # the same, then its last 4 user and assistant messages in full, oldest first
amx session show 09467ec1 --find '"race condition"' --match 2 --count 50  # prose around the second matching message
amx session show 09467ec1 --from 100 --count 50 --json  # a bounded message window, with zero-based message ordinals
amx session show 09467ec1 --at 2026-10-17T12:00:00Z --count 50 --json # supporting assistant output at or before an event
amx session continue codex <session-id>
amx session continue --here   # the most recently active conversation started in this directory or below it, named on stderr before acting
amx session fork claude <session-id>
amx session fork-worktree codex <session-id> --yes
amx session handoff claude:<session-id> --profile codex-work
```

A handoff can also be reviewed by a native or external caller before launch:

```sh
amx session handoff claude:<session-id> --preview --json
amx session handoff claude:<session-id> --preview > context.md
# Edit context.md, then submit the reviewed text:
amx session handoff claude:<session-id> --profile codex-work \
  --content-file context.md --instruction 'Review this for concurrency bugs'
```

Preview is read-only and requires no target profile or terminal backend. Its JSON
uses the standard action envelope, with `changed: false` and `data.content`,
`data.directory`, `data.source_harness`, and `data.source_session_id`.
`--content-file` accepts non-empty UTF-8 up to 1 MiB; `--instruction` accepts up
to 16 KiB and appends a **Next instruction** section. Submitted context is used
verbatim, even when the original transcript has advanced. `--preview` cannot be
combined with editing, submitted content, instructions, or `--no-launch`.
`--edit` and `--content-file` are mutually exclusive. Launch results additionally
report `data.runtime_session_id` on tmux, so clients open the exact receiving
session. Packs remain saved if launch fails.

Conversation threads retain the source and receiving conversation of handoffs
launched from either the app or CLI, including after both agents exit:

```sh
amx session thread claude:<session-id>        # follow the entire connected chain
amx session thread codex:<receiving-id> --json
amx session threads --json                   # all recorded continuations
```

`thread` accepts the same qualified, bare, and unique-prefix selectors as
`show`. An exact qualified identity already present in the link history works
even when its transcript is no longer indexed. JSON uses the standard schema-v1
envelope and `data.thread_links`: each link has its source identity, optional
target identity, stable receiving launch key, handoff artifact path, kind, and
time. Until the receiving agent reports its first interactive conversation,
the target is absent and the link is labeled as awaiting its conversation.
Branches are preserved; matching a bare session ID never joins different
harnesses. Native resumes of the same conversation retain that identity.

`session list --json` and `session show --json` also include `thread_links`.
List responses add `related_conversations` for indexed members outside the
returned page; pagination totals still count the requested list. A
`thread_warning` reports unreadable link history while leaving usable
conversation results visible. The dedicated thread commands fail visibly if
the history cannot be read.

Links live in `$XDG_STATE_HOME/agent-mux/conversation-links.json`, falling back
to `~/.local/state/agent-mux/conversation-links.json`. The private file stores
identities, relationships, artifact paths and timestamps, without transcript
contents. It is durable state, independent of the transcript cache and Flight
Log. Missing files start empty; damaged files are preserved, never rebuilt.
Recording begins with the updated launcher and daemon; restart the daemon
after updating. `--preview`, `--no-launch`, and failed launches create no
continuation. A later `/new` in the receiving terminal does not reassign an
existing handoff to the new chat.

`amx session activity` reads what the daemon recorded in its journal: sessions
changing state, agents exiting, and agent-session-loop runs starting and
finishing. It is a record of what happened, never rebuilt from the sessions
running now, so it reaches back only as far as the journal is retained.

```bash
amx session activity                     # the last 7 days, at most 500 records
amx session activity --days 1 --limit 50 # --days is 1–90, --limit 1–1000
amx session activity --json              # {entries, since, earliest_available, truncated, corrupt_lines, matched}
```

Each line is the time, the harness, a title, and a detail. A journal the
command cannot open is `activity_unavailable`.

`amx session changes` reviews a Git checkout against `--base` (`HEAD` unless
given): staged and unstaged changes to tracked files, and untracked paths.
Name the checkout with exactly one of `--directory`, or `--runtime-session`
for the directory a live tmux session's agent is working in. It writes nothing, and runs no Git hook,
external diff driver, or textconv.

```bash
amx session changes --directory .
amx session changes --directory ~/projects/api --base main
amx session changes --runtime-session rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --file README.md
amx session changes --directory . --json   # {root, base, files, patch}
```

Each changed file is listed as `+added -deleted` and its path; `--file` adds
that one file's patch. A runtime session that is not live, or whose pane has
no working directory, is `session_unavailable`; a checkout Git cannot read is
`review_unavailable`.

`session show --at` opens a bounded window around the latest timestamped assistant
message at or before the given RFC3339 event time, including old messages outside
the recent tail. It returns `focus_message` when one exists; otherwise it returns
recent messages without attributing them to the event. It cannot be combined with
`--last`, `--find`, or `--from`. Activity uses this same lookup when opening History,
briefly highlights the located message, and returns focus to Activity when History
closes. Opening a conversation does not mark the event read.

`session show --find` uses the same terms and quoted phrases as search. It
navigates user/assistant prose only; a match confined to tool output or hidden
reasoning is not exposed. `--match` is one-based; `--from` and the JSON
`messages_start`, `match_messages`, and `focus_message` ordinals are zero-based.
Window size defaults to 50 and is limited to 500; `--last` remains a separate
way to request the tail. JSON includes `match_terms` for highlighting.

`continue`, `fork`, `fork-worktree`, `handoff`, and `show` read their
conversation with one parser, so each accepts `<harness> <session-id>`, the
single `<harness>:<session-id>` selector (`amx session continue
codex:<session-id>`), and a bare `<session-id>`, and a conversation that is
not indexed is reported in the same words by all of them. A bare
`<session-id>`: it resolves when exactly one indexed conversation carries
that ID, and an ID shared across harnesses is refused with the
harness-qualified candidates rather than guessed. Any of these forms may give
just the start of the ID, at least four characters, the way a short Git hash
works: `amx session continue 7f67dc81`. An exact ID always wins over a longer
one it begins, and a prefix that several conversations share is refused with
every candidate.

`session continue` attaches to the exact live tmux session when one exists
and otherwise resumes the closed conversation. `session list` and `session search`
in a terminal mark those conversations with `●` in a LIVE column, and the one
open in the window the command runs in with `here`; `session show` identifies their live backend target, and all three put the tab under `live` in `--json`; when the daemon or tmux cannot be asked, the column and line are left out rather than reporting
nothing open. Native forks are used for Claude
and Codex. Copilot forks use a portable handoff because Copilot does not expose
native fork semantics. Worktree forks create a tracked isolated Git worktree
from the source commit. They refuse a dirty source unless `--allow-dirty` is
explicitly confirmed; uncommitted changes are never copied, even with that flag.
