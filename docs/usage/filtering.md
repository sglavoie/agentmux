# Filtering and annotations

[Back to usage](../usage.md)

## Filter DSL

The list and launcher share the same filter DSL:

| Token | Meaning |
| --- | --- |
| `word` | case-insensitive substring against title, directory, or profile |
| `@profile` | exact profile match |
| `%harness` | exact harness match, e.g. `%claude`, `%codex`, `%copilot` |
| `!state` | state match: one whole urgency group (`attention`, `active`, `idle`) or one icon (`bolt`, `key`, `stop`, `warning`, `error`, `subagent`, `compact`, `question`) |
| `#project` | exact case-insensitive project-label match |
| `#"project name"` | project label containing whitespace; inside quotes use `\"` for `"` and `\\` for `\` |
| `\@foo`, `\!x`, `\%y`, `\#z` | literal leading `@`, `!`, `%`, or `#` |

`!attention`, `!active`, and `!idle` select exactly the three groups the
dashboard already sorts rows into, so `!attention` always matches the rows under
the **Needs you** heading and nothing else. They are groups, not shorthand
for a set of icons: an unseen `🛑` is in the attention group and a `🛑` you have
already looked at is not, so the two rows carry the same icon and answer
`!attention` differently.

Project completion is contextual: type `#` in the filter editor and the popup
lists the case-insensitive canonical labels remaining after every other active
token is applied. Labels that need quoting are inserted in their escaped quoted
form.

Tokens are whitespace-separated and combine with AND. Lone `@`, `!`, `%`, and `#`
markers and unrecognized `!state` names are ignored rather than rejected — a
typo like `!attn` leaves the list unfiltered instead of emptying it. An inline
hint explains that the state was not applied and suggests `!attention`. Unclosed
project quotes also remain unapplied and explain how to finish them.

### Accounted-for sessions and notes

Press `Ctrl+g`, or use `:` → **Mark Session Accounted For**, after checking a session.
Pressing `Ctrl+g` again clears the acknowledgement. Its live
Working, Idle, or Waiting state stays visible; a separate checkmark records your
review. **Mark Project Accounted For** applies to the sessions captured when you
opened the palette, including folded members of a selected project summary.
Sessions added afterward remain unacknowledged. A mixed repository family can be
acknowledged together, but a shared note requires a single configured project.

A completed turn, new permission request or question, warning, error, or unexpected
agent exit makes the affected session need review again. Ordinary working,
subagent, and compaction changes preserve the acknowledgement. Unresolved questions
and errors remain actionable even when acknowledged; acknowledged completed turns
leave the Needs you queue. Reading a session does not mark it accounted for.

**Edit Session Note…** and **Edit Project Note…** save a one-line note of up to
500 characters. Enter saves, Escape cancels, and an empty note removes it. Notes
remain after new activity and appear in the inspector. Acknowledgements and notes
are shared with the macOS app and survive daemon restarts. **Undo Annotation**
reverts your last successful edit without hiding newer activity; it refuses to
replace annotations changed since that edit. Undo receipts expire on daemon
restart or after 32 newer edits.

The CLI offers the same session actions:

```sh
amx session review --runtime-session <id> --accounted
amx session review --runtime-session <id> --note "Waiting for implementation"
amx session review --runtime-session <id> --clear
amx session review --undo <receipt>
```

Inside a managed session, `--runtime-session` defaults to
`AMX_RUNTIME_SESSION_ID`. `--json` returns the edit count and Undo receipt.
Annotations are saved privately in `session-reviews.json` beside the runtime
registry, independently of agent state. An unreadable annotation file is preserved
and edits fail visibly instead of replacing existing notes.
