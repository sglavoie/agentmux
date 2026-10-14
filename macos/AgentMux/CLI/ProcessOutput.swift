import Darwin
import Foundation

/// How one bounded run of a child process ended.
enum ProcessRun: Equatable {
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
    /// cannot fill one. A child that outlasts `timeout` is terminated.
    ///
    /// The run ends when the child exits, not when its pipes close: a
    /// grandchild it left in the background (a login shell's agent or
    /// daemon) may hold them open for good. After the exit the readers get
    /// `pipeGrace` to drain what was written, and whatever they have is the
    /// output.
    static func execute(
        _ executable: String,
        arguments: [String],
        environment: [String: String]? = nil,
        timeout: TimeInterval
    ) -> ProcessRun {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments
        if let environment {
            process.environment = environment
        }
        let stdout = Pipe()
        let stderr = Pipe()
        process.standardOutput = stdout
        process.standardError = stderr
        process.standardInput = FileHandle.nullDevice
        let exited = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in exited.signal() }
        do {
            try process.run()
        } catch {
            return .launchFailed(reason: error.localizedDescription)
        }
        let readers = [PipeReader(stdout.fileHandleForReading), PipeReader(stderr.fileHandleForReading)]
        defer { readers.forEach { $0.finish() } }
        if exited.wait(timeout: .now() + timeout) == .timedOut {
            stop(process, exited: exited)
            return .timedOut
        }
        let grace = DispatchTime.now() + pipeGrace
        for reader in readers { reader.waitForEOF(until: grace) }
        return .finished(ProcessOutput(status: process.terminationStatus, stdout: readers[0].data, stderr: readers[1].data))
    }

    /// How long the pipes may stay open after the child exits before the
    /// output is taken as it stands.
    static let pipeGrace: TimeInterval = 0.2

    /// How long a terminated child gets to exit before it is killed.
    static let terminationGrace: TimeInterval = 0.5

    /// Ends a child that outlasted its deadline: SIGTERM, then SIGKILL if it
    /// is still running after the grace period. Bounded either way.
    private static func stop(_ process: Process, exited: DispatchSemaphore) {
        process.terminate()
        if exited.wait(timeout: .now() + terminationGrace) == .timedOut, process.isRunning {
            kill(process.processIdentifier, SIGKILL)
            _ = exited.wait(timeout: .now() + terminationGrace)
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
