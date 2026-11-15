import AppKit

/// One app-wide sound budget for live workspace state transitions. Suppressed
/// transitions are dropped, never queued to play after focus or snooze changes.
final class WorkspaceSound {
    static let needsYouSound = "Glass"
    static let finishedSound = "Pop"
    static let minimumInterval: TimeInterval = 2

    enum Kind: Equatable {
        case needsYou, finished

        var name: String {
            self == .needsYou ? WorkspaceSound.needsYouSound : WorkspaceSound.finishedSound
        }

        var volume: Float { self == .needsYou ? 0.7 : 0.35 }

        init?(iconState: String) {
            switch iconState {
            case "key", "question", "error": self = .needsYou
            case "stop": self = .finished
            default: return nil
            }
        }
    }

    private let clock: () -> TimeInterval
    private let player: (Kind) -> Void
    private var lastPlayed: TimeInterval?

    init(clock: @escaping () -> TimeInterval = { ProcessInfo.processInfo.systemUptime },
         player: @escaping (Kind) -> Void = WorkspaceSound.play) {
        self.clock = clock
        self.player = player
    }

    func transition(from previous: [WorkspaceRow], to rows: [WorkspaceRow],
                    wasCurrent: Bool, current: Bool, enabled: Bool,
                    snoozed: Bool, onScreen: Set<AttentionCycler.Key>) {
        guard enabled, wasCurrent, current, !snoozed else { return }
        let before = Dictionary(previous.map { ($0.key, $0.iconState) }, uniquingKeysWith: { first, _ in first })
        var due: Kind?
        for row in rows where !row.muted && !onScreen.contains(row.key) {
            guard let old = before[row.key], let kind = Kind(iconState: row.iconState),
                  Kind(iconState: old) != kind else { continue }
            if kind == .needsYou { due = kind; break }
            due = kind
        }
        guard let due else { return }
        let time = clock()
        if let lastPlayed, time - lastPlayed < Self.minimumInterval { return }
        lastPlayed = time
        player(due)
    }

    private static func play(_ kind: Kind) {
        guard let sound = NSSound(named: NSSound.Name(kind.name)) else { return }
        sound.volume = kind.volume
        sound.play()
    }
}
