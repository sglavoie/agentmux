import Darwin
import Foundation

/// How one bounded run of a child process ended.
enum ProcessRun: Equatable {
    case cancelled
    /// It exited within its deadline.
    case finished(ProcessOutput)
    /// It never started; `reason` is why, for the log and the operator.
    case launchFailed(reason: String)
    /// It was still running at its deadline and was stopped. Whatever it was
    /// doing may or may not have happened.
    case timedOut

    var output: ProcessOutput? {
        if case let .finished(output) = self { return output }
        return nil
    }
}

/// One bounded run of a child process: what it wrote and how it ended.
final class ProcessCancellation: @unchecked Sendable {
    private let lock = NSLock()
    private var cancelled = false
    var isCancelled: Bool { lock.lock(); defer { lock.unlock() }; return cancelled }
    func cancel() { lock.lock(); cancelled = true; lock.unlock() }
}

struct ProcessOutput: Equatable {
    var status: Int32
    var stdout: Data
    var stderr: Data

    /// `execute`'s output, nil when the child failed to start or timed out.
    static func run(
        _ executable: String,
        arguments: [String],
        environment: [String: String]? = nil,
        timeout: TimeInterval
    ) -> ProcessOutput? {
        execute(executable, arguments: arguments, environment: environment, timeout: timeout).output
    }

    /// Runs `executable` with `arguments` and `environment` (nil inherits this
    /// process's), reading both pipes off the calling thread so a chatty child
    /// cannot fill one. A child that outlasts `timeout` is terminated, along
    /// with everything it started.
    ///
    /// The run ends when the child exits, not when its pipes close: a
    /// grandchild it left in the background (a login shell's agent or
    /// daemon) may hold them open for good. After the exit the readers get
    /// `pipeGrace` to drain what was written, and whatever they have is the
    /// output.
    ///
    /// The child starts in a process group of its own, so a timeout reaches
    /// what it started too: a login shell's profile still running, or the
    /// tmux client amx is waiting on. Signalling the child alone would leave
    /// them running with nobody to wait for them. A child that exits in time
    /// leaves its background helpers be.
    static func execute(
        _ executable: String,
        arguments: [String],
        environment: [String: String]? = nil,
        timeout: TimeInterval,
        cancellation: ProcessCancellation? = nil
    ) -> ProcessRun {
        if cancellation?.isCancelled == true { return .cancelled }
        var stdoutPipe: [Int32] = [-1, -1]
        var stderrPipe: [Int32] = [-1, -1]
        guard pipe(&stdoutPipe) == 0 else { return .launchFailed(reason: String(cString: strerror(errno))) }
        guard pipe(&stderrPipe) == 0 else {
            stdoutPipe.forEach { close($0) }
            return .launchFailed(reason: String(cString: strerror(errno)))
        }
        // Another child started meanwhile must not hold a write end open.
        for fd in stdoutPipe + stderrPipe { _ = fcntl(fd, F_SETFD, FD_CLOEXEC) }

        let spawned = spawn(
            executable, arguments: arguments,
            environment: environment ?? ProcessInfo.processInfo.environment,
            stdout: stdoutPipe[1], stderr: stderrPipe[1]
        )
        close(stdoutPipe[1])
        close(stderrPipe[1])
        let pid: pid_t
        switch spawned {
        case let .success(child):
            pid = child
        case let .failure(failure):
            close(stdoutPipe[0])
            close(stderrPipe[0])
            return .launchFailed(reason: failure.message)
        }

        let readers = [
            PipeReader(FileHandle(fileDescriptor: stdoutPipe[0], closeOnDealloc: true)),
            PipeReader(FileHandle(fileDescriptor: stderrPipe[0], closeOnDealloc: true)),
        ]
        defer { readers.forEach { $0.finish() } }
        let exited = waitForExit(pid)
        let deadline = DispatchTime.now() + timeout
        while exited.wait(timeout: cancellation == nil ? deadline : min(deadline, .now() + 0.025)) == .timedOut {
            if cancellation?.isCancelled == true {
                stop(group: pid, exited: exited)
                return .cancelled
            }
            if DispatchTime.now() >= deadline {
                stop(group: pid, exited: exited)
                return .timedOut
            }
        }
        let status = reap(pid)
        let grace = DispatchTime.now() + pipeGrace
        for reader in readers { reader.waitForEOF(until: grace) }
        return .finished(ProcessOutput(status: status, stdout: readers[0].data, stderr: readers[1].data))
    }

    private struct SpawnFailure: Error {
        let message: String
    }

    /// Starts the child as the leader of a new process group, with standard
    /// input on /dev/null, its output on the given pipe ends, no other
    /// descriptor of this process, and default signal handling, as `Process`
    /// would. The group is set by the spawn itself, so there is no moment in
    /// which the child runs, or starts anything, outside it.
    private static func spawn(
        _ executable: String,
        arguments: [String],
        environment: [String: String],
        stdout: Int32,
        stderr: Int32
    ) -> Result<pid_t, SpawnFailure> {
        var attributes: posix_spawnattr_t?
        guard posix_spawnattr_init(&attributes) == 0 else { return .failure(SpawnFailure(message: "could not prepare the process")) }
        defer { posix_spawnattr_destroy(&attributes) }
        let flags = POSIX_SPAWN_SETPGROUP | POSIX_SPAWN_CLOEXEC_DEFAULT | POSIX_SPAWN_SETSIGDEF | POSIX_SPAWN_SETSIGMASK
        posix_spawnattr_setflags(&attributes, Int16(flags))
        // Group 0: a new group whose ID is the child's own pid.
        posix_spawnattr_setpgroup(&attributes, 0)
        var everySignal = sigset_t()
        sigfillset(&everySignal)
        posix_spawnattr_setsigdefault(&attributes, &everySignal)
        var noSignals = sigset_t()
        sigemptyset(&noSignals)
        posix_spawnattr_setsigmask(&attributes, &noSignals)

        var actions: posix_spawn_file_actions_t?
        guard posix_spawn_file_actions_init(&actions) == 0 else { return .failure(SpawnFailure(message: "could not prepare the process")) }
        defer { posix_spawn_file_actions_destroy(&actions) }
        posix_spawn_file_actions_addopen(&actions, 0, "/dev/null", O_RDONLY, 0)
        posix_spawn_file_actions_adddup2(&actions, stdout, 1)
        posix_spawn_file_actions_adddup2(&actions, stderr, 2)

        let argv = [executable] + arguments
        let envp = environment.map { "\($0.key)=\($0.value)" }
        var cArgv: [UnsafeMutablePointer<CChar>?] = argv.map { strdup($0) } + [nil]
        var cEnvp: [UnsafeMutablePointer<CChar>?] = envp.map { strdup($0) } + [nil]
        defer {
            cArgv.forEach { free($0) }
            cEnvp.forEach { free($0) }
        }
        var pid: pid_t = 0
        let result = posix_spawn(&pid, executable, &actions, &attributes, &cArgv, &cEnvp)
        guard result == 0 else { return .failure(SpawnFailure(message: String(cString: strerror(result)))) }
        return .success(pid)
    }

    /// Signalled once `pid` has exited. The child is left unreaped, so its pid
    /// (and with it its group's ID) cannot go to another process while a
    /// timeout may still signal the group.
    private static func waitForExit(_ pid: pid_t) -> DispatchSemaphore {
        let exited = DispatchSemaphore(value: 0)
        let thread = Thread {
            var info = siginfo_t()
            while waitid(P_PID, id_t(pid), &info, WEXITED | WNOWAIT) != 0, errno == EINTR {}
            exited.signal()
        }
        thread.qualityOfService = Thread.current.qualityOfService
        thread.start()
        return exited
    }

    /// Collects an exited child's status as `Process` reports it: the exit
    /// code, or the signal that ended it.
    private static func reap(_ pid: pid_t) -> Int32 {
        var status: Int32 = 0
        while waitpid(pid, &status, 0) < 0, errno == EINTR {}
        let signal = status & 0x7f
        return signal == 0 ? (status >> 8) & 0xff : signal
    }

    /// How long the pipes may stay open after the child exits before the
    /// output is taken as it stands.
    static let pipeGrace: TimeInterval = 0.2

    /// How long a terminated child gets to exit before it is killed.
    static let terminationGrace: TimeInterval = 0.5

    /// Ends a child that outlasted its deadline, and its group with it:
    /// SIGTERM, then SIGKILL if the child is still running after the grace
    /// period, then SIGKILL for anything in the group that outlived it.
    /// Bounded either way; a child that will not die is reaped whenever it
    /// does.
    private static func stop(group pid: pid_t, exited: DispatchSemaphore) {
        kill(-pid, SIGTERM)
        var ended = exited.wait(timeout: .now() + terminationGrace) == .success
        if !ended {
            kill(-pid, SIGKILL)
            ended = exited.wait(timeout: .now() + terminationGrace) == .success
        }
        // The leader is not reaped yet, so the group's ID is still its own.
        kill(-pid, SIGKILL)
        if ended {
            _ = reap(pid)
        } else {
            AMXCommand.reapWhenExited(pid)
        }
    }

    /// Reads one pipe to EOF on a thread of its own, in slices short enough
    /// that `finish()` ends it promptly when the pipe never reaches EOF. The
    /// thread holds the handle, so its descriptor closes (when the handle is
    /// released) only after the last read, never under one.
    private final class PipeReader {
        private let lock = NSLock()
        private var collected = Data()
        private var stopped = false
        private let done = DispatchSemaphore(value: 0)
        private static let slice: Int32 = 50

        init(_ handle: FileHandle) {
            let fd = handle.fileDescriptor
            let thread = Thread { [self] in
                defer {
                    withExtendedLifetime(handle) {}
                    done.signal()
                }
                var chunk = [UInt8](repeating: 0, count: 64 * 1024)
                while !isStopped {
                    var descriptor = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
                    let ready = poll(&descriptor, 1, Self.slice)
                    if ready < 0 {
                        if errno == EINTR { continue }
                        return
                    }
                    if ready == 0 { continue }
                    let count = chunk.withUnsafeMutableBytes { Darwin.read(fd, $0.baseAddress, $0.count) }
                    if count < 0 {
                        if errno == EINTR || errno == EAGAIN { continue }
                        return
                    }
                    if count == 0 { return }
                    lock.lock(); collected.append(contentsOf: chunk[0..<count]); lock.unlock()
                }
            }
            // The caller waits on it, so it runs at the caller's priority.
            thread.qualityOfService = Thread.current.qualityOfService
            thread.start()
        }

        private var isStopped: Bool {
            lock.lock(); defer { lock.unlock() }
            return stopped
        }

        var data: Data {
            lock.lock(); defer { lock.unlock() }
            return collected
        }

        /// Waits until EOF or `deadline`, whichever comes first.
        func waitForEOF(until deadline: DispatchTime) {
            if done.wait(timeout: deadline) == .success { done.signal() }
        }

        /// Stops reading within one slice; does not wait for it.
        func finish() {
            lock.lock(); stopped = true; lock.unlock()
        }
    }
}
