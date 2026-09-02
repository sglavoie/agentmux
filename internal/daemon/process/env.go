package process

// The launch handshake a detaching parent hands to the daemon it spawns.
// SpawnDetached is the only writer; the child reads them back when it builds
// its operational logger and its diagnostic run identity, and `amx daemon
// start` reads DetachedEnv to decide whether it is running in the foreground.
// They live here rather than beside the logger because they are part of the
// launch contract, not part of logging.
const (
	DetachedEnv  = "AMX_DAEMON_DETACHED"
	LaunchIDEnv  = "AMX_DAEMON_LAUNCH_ID"
	BootstrapEnv = "AMX_DAEMON_BOOTSTRAP_LOG"
)
