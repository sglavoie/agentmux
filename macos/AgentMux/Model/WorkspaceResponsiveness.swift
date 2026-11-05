import os

/// Inspect these intervals with Instruments' Points of Interest. They contain
/// no queries, paths, conversation text, or session identifiers. Synchronous
/// UI intervals measure controller work through focus assignment. Deferred
/// layout and GPU presentation are outside these intervals.
enum WorkspaceResponsiveness {
    static let signposter = OSSignposter(subsystem: "io.github.sglavoie.agentmux", category: "Responsiveness")

    static func begin(_ name: StaticString) -> OSSignpostIntervalState {
        signposter.beginInterval(name, id: signposter.makeSignpostID())
    }

    static func end(_ name: StaticString, _ interval: OSSignpostIntervalState) {
        signposter.endInterval(name, interval)
    }
}
