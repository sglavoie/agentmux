import Foundation

/// One bounded run of a child process: what it wrote and how it ended.
struct ProcessOutput: Equatable {
    var status: Int32
    var stdout: Data
    var stderr: Data

    /// Runs `executable` with `arguments` and `environment` (nil inherits this
    /// process's), reading both pipes to EOF off the calling thread so a chatty
    /// child cannot fill one. Nil when it fails to start or outlasts `timeout`,
    /// in which case it is terminated.
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
        do {
            try process.run()
        } catch {
            return nil
        }
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
        if group.wait(timeout: .now() + timeout) == .timedOut {
            process.terminate()
            return nil
        }
        process.waitUntilExit()
        return ProcessOutput(status: process.terminationStatus, stdout: box.stdout, stderr: box.stderr)
    }

    private final class Box {
        var stdout = Data()
        var stderr = Data()
    }
}
