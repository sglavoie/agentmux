// Package kitty is the Kitty terminal integration: `kitten @` remote control,
// topology listing and its per-socket read coordination, match expressions,
// and the Kitty environment variables a process inherits.
//
// It is a terminal backend, not a domain service. It reports what Kitty shows
// and performs what it is asked; it never decides what an observation means
// for a session, attention, or a conversation. Match expressions, socket
// addresses, and integer window and tab IDs are Kitty details: the backend
// migration (docs/backend-migration.md) moves them behind a backend adapter
// so AgentMux identity no longer depends on them, and
// TestRemoteControlBaseline pins the argv that adapter must keep issuing.
package kitty
