import Darwin
import Foundation

extension AMXCommand {
    /// Starts `amx <arguments>` in a session of its own with its standard
    /// streams on /dev/null, and does not wait for it: the way the app runs
    /// `amx daemon start`, which blocks for the daemon's whole life and must
    /// outlive the app. Returns the child's pid, or nil when it did not start.
    ///
    /// `stderrPath` names a file standard error is written to instead,
    /// truncated first: a file, never a pipe, since the child outlives the
    /// app. `onExit` is called with the child's pid and exit code
    /// (`exitCode(waitStatus:)`) once it exits, on a background queue.
    func spawnDetached(
        _ arguments: [String],
        kittyListenOn: String? = nil,
        extraEnvironment: [String: String] = [:],
        stderrPath: String? = nil,
        onExit: (@Sendable (pid_t, Int32) -> Void)? = nil
    ) -> pid_t? {
        var attributes: posix_spawnattr_t?
        guard posix_spawnattr_init(&attributes) == 0 else { return nil }
        defer { posix_spawnattr_destroy(&attributes) }
        posix_spawnattr_setflags(&attributes, Int16(POSIX_SPAWN_SETSID | POSIX_SPAWN_CLOEXEC_DEFAULT))

        var actions: posix_spawn_file_actions_t?
        guard posix_spawn_file_actions_init(&actions) == 0 else { return nil }
        defer { posix_spawn_file_actions_destroy(&actions) }
        for fd: Int32 in [0, 1, 2] {
            if fd == 2, let stderrPath {
                posix_spawn_file_actions_addopen(&actions, fd, stderrPath, O_WRONLY | O_CREAT | O_TRUNC, 0o600)
            } else {
                posix_spawn_file_actions_addopen(&actions, fd, "/dev/null", fd == 0 ? O_RDONLY : O_WRONLY, 0)
            }
        }

        let argv = [binary.path] + arguments
        let envp = environment(kittyListenOn: kittyListenOn)
            .merging(extraEnvironment) { _, extra in extra }
            .map { "\($0.key)=\($0.value)" }
        var cArgv: [UnsafeMutablePointer<CChar>?] = argv.map { strdup($0) } + [nil]
        var cEnvp: [UnsafeMutablePointer<CChar>?] = envp.map { strdup($0) } + [nil]
        defer {
            cArgv.forEach { free($0) }
            cEnvp.forEach { free($0) }
        }
        var pid: pid_t = 0
        let result = posix_spawn(&pid, binary.path, &actions, &attributes, &cArgv, &cEnvp)
        guard result == 0 else { return nil }
        Self.reapWhenExited(pid, onExit: onExit)
        return pid
    }

    /// Reaps `pid` whenever it exits, so a daemon that fails at once does not
    /// linger as a zombie of this app, and hands its exit code to `onExit`.
    /// A process source costs no thread while the child runs, which for the
    /// daemon is the app's whole life.
    static func reapWhenExited(_ pid: pid_t, onExit: (@Sendable (pid_t, Int32) -> Void)? = nil) {
        let source = DispatchSource.makeProcessSource(identifier: pid, eventMask: .exit, queue: .global(qos: .utility))
        source.setEventHandler {
            var status: Int32 = 0
            if waitpid(pid, &status, WNOHANG) == pid { onExit?(pid, exitCode(waitStatus: status)) }
            // The handler holds the source until it is cancelled here.
            source.cancel()
        }
        source.activate()
        // A child that exited before the source was armed raises no event.
        // Only one of the two waits reaps it, so `onExit` runs once.
        var status: Int32 = 0
        if waitpid(pid, &status, WNOHANG) == pid {
            source.cancel()
            onExit?(pid, exitCode(waitStatus: status))
        }
    }

    /// The exit code a `waitpid` status carries: a normal exit's own code,
    /// or 128 plus the signal that ended the process, as a shell reports it.
    nonisolated static func exitCode(waitStatus: Int32) -> Int32 {
        let signal = waitStatus & 0x7f
        return signal == 0 ? (waitStatus >> 8) & 0xff : 128 + signal
    }
}
