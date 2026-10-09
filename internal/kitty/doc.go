// Package kitty is the Kitty terminal integration: `kitten @` remote control,
// topology listing and its per-socket read coordination, match expressions,
// and the Kitty environment variables a process inherits.
//
// It is a terminal backend, not a domain service. It reports what Kitty shows
// and performs what it is asked; it never decides what an observation means
// for a session, attention, or a conversation. Match expressions, socket
// addresses, and integer window and tab IDs are Kitty details: Backend adapts
// a KittyClient to internal/backend (a tab is a group, a window is a pane)
// and is the only place a backend.Ref becomes a match expression. The backend
// migration (docs/backend-migration.md) moves the remaining callers behind it
// so AgentMux identity no longer depends on these details;
// TestRemoteControlBaseline pins the client argv and TestBackendIssuesLegacyArgv
// pins the adapter's mapping onto it.
package kitty
