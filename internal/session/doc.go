// Package session is the Kitty-backed session lifecycle: launching, focusing,
// closing, and restarting managed harness tabs, resolving the exact live owner
// of a conversation, and constructing the harness invocation a launch runs.
//
// Live instance identity is the freshly validated Kitty window and title
// owner, never a remembered one; conversation identity is the
// (harness, session_id) pair from internal/conversation.
//
// Dedup policy, launch identity, and worktree and handoff compensation are
// AgentMux decisions that stay here when launching becomes backend-neutral.
// Every terminal command this package issues (launch, close, focus, title,
// colors, metadata, reorder) goes through internal/backend; it still reads
// Kitty's window tree to project RunningSession values, and RunningSession's
// GroupRef/PaneRef are where a Kitty tab or window ID becomes a backend.Ref.
// docs/backend-migration.md records that split and the workflows that depend
// on it.
package session
