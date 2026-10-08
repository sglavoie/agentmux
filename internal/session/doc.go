// Package session is the Kitty-backed session lifecycle: launching, focusing,
// closing, and restarting managed harness tabs, resolving the exact live owner
// of a conversation, and constructing the harness invocation a launch runs.
//
// Live instance identity is the freshly validated Kitty window and title
// owner, never a remembered one; conversation identity is the
// (harness, session_id) pair from internal/conversation.
package session
