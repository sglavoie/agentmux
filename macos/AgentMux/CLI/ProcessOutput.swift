import Foundation

/// One bounded run of a child process: what it wrote and how it ended.
struct ProcessOutput: Equatable {
    var status: Int32
    var stdout: Data
    var stderr: Data

    /// Runs `executable` with `arguments` and `environment` (nil inherits this
    /// process's), reading both pipes to EOF off the calling thread so a chatty
    /// child cannot fill one. Nil when it fails to start or outlasts `timeout`,
    /// in which case it is terminated. The one deadline covers both pipes and
    /// the exit itself, so a child that closes its pipes and keeps running
    /// still times out.
    static func run(
        _ executable: String,
        arguments: [String],
        environment: [String: String]? = nil,
        timeout: TimeInterval
    ) -> ProcessOutput? {
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
            return nil
        }
        let deadline = DispatchTime.now() + timeout
        let box = Box()
        let group = DispatchGroup()
        group.enter()
        DispatchQueue.global().async {
            box.stdout = stdout.fileHandleForReading.readDataToEndOfFile()
            group.leave()
        }
        group.enter()
        DispatchQueue.global().async {
            box.stderr = stderr.fileHandleForReading.readDataToEndOfFile()
            group.leave()
        }
        if group.wait(timeout: deadline) == .timedOut || exited.wait(timeout: deadline) == .timedOut {
            stop(process, exited: exited)
            return nil
        }
        return ProcessOutput(status: process.terminationStatus, stdout: box.stdout, stderr: box.stderr)
    }

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

    private final class Box {
        var stdout = Data()
        var stderr = Data()
    }
}
