import Darwin
import Foundation

extension AMXCommand {
    /// Starts `amx <arguments>` in a session of its own with its standard
    /// streams on /dev/null, and does not wait for it: the way the app runs
    /// `amx daemon start`, which blocks for the daemon's whole life and must
    /// outlive the app. Returns the child's pid, or nil when it did not start.
    func spawnDetached(_ arguments: [String], kittyListenOn: String? = nil) -> pid_t? {
        var attributes: posix_spawnattr_t?
        guard posix_spawnattr_init(&attributes) == 0 else { return nil }
        defer { posix_spawnattr_destroy(&attributes) }
        posix_spawnattr_setflags(&attributes, Int16(POSIX_SPAWN_SETSID | POSIX_SPAWN_CLOEXEC_DEFAULT))

        var actions: posix_spawn_file_actions_t?
        guard posix_spawn_file_actions_init(&actions) == 0 else { return nil }
        defer { posix_spawn_file_actions_destroy(&actions) }
        for fd: Int32 in [0, 1, 2] {
            posix_spawn_file_actions_addopen(&actions, fd, "/dev/null", fd == 0 ? O_RDONLY : O_WRONLY, 0)
        }

        let argv = [binary.path] + arguments
        let envp = environment(kittyListenOn: kittyListenOn).map { "\($0.key)=\($0.value)" }
        var cArgv: [UnsafeMutablePointer<CChar>?] = argv.map { strdup($0) } + [nil]
        var cEnvp: [UnsafeMutablePointer<CChar>?] = envp.map { strdup($0) } + [nil]
        defer {
            cArgv.forEach { free($0) }
            cEnvp.forEach { free($0) }
        }
        var pid: pid_t = 0
        let result = posix_spawn(&pid, binary.path, &actions, &attributes, &cArgv, &cEnvp)
        guard result == 0 else { return nil }
        Self.reapWhenExited(pid)
        return pid
    }

    /// Reaps `pid` whenever it exits, so a daemon that fails at once does not
    /// linger as a zombie of this app. A process source costs no thread while
    /// the child runs, which for the daemon is the app's whole life.
    static func reapWhenExited(_ pid: pid_t) {
        let source = DispatchSource.makeProcessSource(identifier: pid, eventMask: .exit, queue: .global(qos: .utility))
        source.setEventHandler {
            var status: Int32 = 0
            _ = waitpid(pid, &status, WNOHANG)
            // The handler holds the source until it is cancelled here.
            source.cancel()
        }
        source.activate()
        // A child that exited before the source was armed raises no event.
        var status: Int32 = 0
        if waitpid(pid, &status, WNOHANG) == pid { source.cancel() }
    }
}
