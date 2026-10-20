import Foundation

/// Runs blocking work somewhere and hands its result back on the main thread.
/// The panels and review models take one so a test can run the work at once,
/// or hold it.
protocol WorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void)
}

/// The name the answer panels first gave `WorkRunner`.
typealias ASLWorkRunner = WorkRunner

/// Work on a queue, results on main: serial unless the caller gives one that
/// is not.
struct QueueWorkRunner: WorkRunner {
    var queue: DispatchQueue

    init(queue: DispatchQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.asl", qos: .userInitiated)) {
        self.queue = queue
    }

    /// The answer panel's: a snapshot for another run never waits behind a
    /// slow action, since every reply is matched by sequence and target.
    static func concurrent() -> QueueWorkRunner {
        QueueWorkRunner(queue: DispatchQueue(label: "io.github.sglavoie.agentmux.asl.panel", qos: .userInitiated,
                                             attributes: .concurrent))
    }

    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) {
        queue.async {
            let value = work()
            DispatchQueue.main.async { then(value) }
        }
    }

    /// As `run(_:then:)`, for a caller on the main actor: `then` is isolated
    /// to it, so it may touch the caller's state without hopping itself.
    func run<T>(_ work: @escaping () -> T, onMain then: @escaping @MainActor (T) -> Void) {
        queue.async {
            let value = work()
            DispatchQueue.main.async { MainActor.assumeIsolated { then(value) } }
        }
    }
}
