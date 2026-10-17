import Foundation
import Combine

/// History, Activity and Waiting share prose sizing. Code, terminal previews and
/// checkout diffs use an independent monospace size, preserving saved diff choices.
@MainActor
final class WorkspaceReadingPreferences: ObservableObject {
    static let shared = WorkspaceReadingPreferences(defaults: .standard)
    private let state: WorkspaceReviewState
    private let defaults: UserDefaults?
    @Published var proseSize: Double { didSet { defaults?.set(effectiveProseSize, forKey: "amxReadingProseSize") } }
    @Published var fontSize: Double { didSet { save() } }
    @Published var wrapLines: Bool { didSet { save() } }

    init(defaults: UserDefaults? = nil) {
        self.defaults = defaults
        proseSize = defaults?.object(forKey: "amxReadingProseSize") as? Double ?? 13
        state = WorkspaceReviewState(defaults: defaults)
        let saved = state.diffAppearance
        fontSize = saved.fontSize
        wrapLines = saved.wrapLines
    }
    var effectiveFontSize: Double { fontSize.isFinite ? min(24, max(10, fontSize)) : 12 }
    var effectiveProseSize: Double { proseSize.isFinite ? min(24, max(11, proseSize)) : 13 }
    private func save() {
        state.diffAppearance = .init(fontSize: effectiveFontSize, wrapLines: wrapLines)
    }
}
