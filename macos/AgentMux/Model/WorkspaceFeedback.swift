import Foundation
import Combine

/// Presentation of an outcome, captured with the target the operation used.
/// Recovery only reads state; it never repeats a launch, landing, or run control.
struct WorkspaceFeedback: Equatable, Codable {
    enum Kind: String, Codable { case information, success, warning, error }
    enum Recovery: String, Codable {
        case refresh, transcript, receipt, readiness, runs
        var title: String {
            switch self {
            case .refresh: return "Refresh"
            case .transcript: return "Reload Transcript"
            case .receipt: return "Retry Receipt"
            case .readiness: return "Check Readiness"
            case .runs: return "Show Runs"
            }
        }
    }
    var target: String
    var message: String
    var kind: Kind = .error
    var nextStep: String = ""
    var recovery: Recovery? = .refresh
}

/// Only read destinations survive a restart. No commands, authorization tokens,
/// receipt operations, or executable closures are serialized.
enum WorkspaceFeedbackDestination: Codable, Equatable {
    case conversation(String)
    case checkout(String)
    case runs(rowID: String?, runID: String?)
    case plan(Plan)

    struct Plan: Codable, Equatable {
        let plan: String
        let project: String
        let profile: String
        let harness: String
        let reviewHarness: String
        let commitMode: String
    }
}

@MainActor
final class WorkspaceFeedbackHistory: ObservableObject {
    struct Entry: Identifiable {
        let id: UUID
        let occurred: Date
        let feedback: WorkspaceFeedback
        let destination: WorkspaceFeedbackDestination?
        let occurrences: Int
        let available: () -> Bool
        let recover: () -> Void
    }
    private struct Saved: Codable {
        let id: UUID
        let occurred: Date
        let feedback: WorkspaceFeedback
        let destination: WorkspaceFeedbackDestination?
        let occurrences: Int
    }
    @Published private(set) var entries: [Entry] = []
    static let limit = 30
    static let storageKey = "amxUnresolvedActionFeedback.v1"
    private let defaults: UserDefaults?
    var canOpen: (WorkspaceFeedbackDestination) -> Bool = { _ in false }
    var open: (WorkspaceFeedbackDestination) -> Void = { _ in }

    init(defaults: UserDefaults? = nil) {
        self.defaults = defaults
        guard let data = defaults?.data(forKey: Self.storageKey),
              let saved = try? JSONDecoder().decode([Saved].self, from: data) else { return }
        entries = saved.filter { Self.unresolved($0.feedback) }.prefix(Self.limit).map { value in
            var feedback = value.feedback
            // A receipt belongs to the old process's exact pending operation.
            // After restart, inspect fresh run state instead of retrying it.
            if case .runs = value.destination { feedback.recovery = .runs }
            return Entry(id: value.id, occurred: value.occurred, feedback: feedback,
                         destination: value.destination, occurrences: value.occurrences,
                         available: { [weak self] in
                             guard let destination = value.destination else { return false }
                             return self?.canOpen(destination) == true
                         }, recover: { [weak self] in
                             guard let self, let destination = value.destination, self.canOpen(destination) else { return }
                             self.open(destination)
                         })
        }
    }

    private static func unresolved(_ feedback: WorkspaceFeedback) -> Bool {
        feedback.kind == .error || feedback.kind == .warning
    }

    func record(_ feedback: WorkspaceFeedback, at: Date = Date(), destination: WorkspaceFeedbackDestination? = nil,
                available: @escaping () -> Bool = { true }, recover: @escaping () -> Void = {}) {
        let duplicate = entries.first { $0.feedback == feedback && $0.destination == destination }
        if let duplicate { entries.removeAll { $0.id == duplicate.id } }
        entries.insert(Entry(id: duplicate?.id ?? UUID(), occurred: at, feedback: feedback, destination: destination,
                             occurrences: (duplicate?.occurrences ?? 0) + 1, available: available, recover: recover), at: 0)
        while entries.count > Self.limit {
            // Routine successes must never displace an unresolved failure.
            let index = entries.lastIndex { !Self.unresolved($0.feedback) } ?? entries.count - 1
            entries.remove(at: index)
        }
        save()
    }

    func dismiss(_ id: UUID) {
        entries.removeAll { $0.id == id }
        save()
    }

    private func save() {
        let saved = entries.filter { Self.unresolved($0.feedback) }.map {
            Saved(id: $0.id, occurred: $0.occurred, feedback: $0.feedback, destination: $0.destination, occurrences: $0.occurrences)
        }
        if let data = try? JSONEncoder().encode(saved) { defaults?.set(data, forKey: Self.storageKey) }
    }
}
