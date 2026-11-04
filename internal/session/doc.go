// Package session is the session lifecycle: launching, focusing,
// closing, and restarting managed harness sessions, resolving the exact live owner
// of a conversation, and constructing the harness invocation a launch runs.
//
// Conversation identity is the (harness, session_id) pair from
// internal/conversation.
//
// Dedup policy, launch identity, and worktree and handoff compensation are
// AgentMux decisions that stay here. Terminal commands go through
// internal/backend, with tmux as the runtime backend.
// Successful launches also record compact Flight Log events, independently of
// diagnostic logging. A failed or deduplicated launch records no usage.
package session
