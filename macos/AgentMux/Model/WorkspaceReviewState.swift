import Foundation
import os

/// Presentation preferences only: no transcript, diff, command, or authority is
/// saved. Tests opt into an isolated defaults suite; production supplies standard.
final class WorkspaceReviewState {
    private static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "review")
    /// How many per-target entries (comparisons, scroll positions) are kept.
    static let entryLimit = 100
    struct History: Codable {
        var query: String
        var scope: String
        var automated: Bool
        var project: String
        var conversation: String?
        var selection: String?
        var message: Int?
        var resultsQuery: String? = nil
        var loadedResults: Int? = nil
    }
    struct HistoryLayout: Codable {
        var resultsWidth: Double? = nil
        var collapsed = false
        func width(in available: Double) -> Double {
            let preferred = resultsWidth.flatMap { $0.isFinite ? $0 : nil } ?? min(260, available * 0.30)
            return min(max(200, preferred), max(200, available - 380))
        }
    }
    var historyLayout: HistoryLayout {
        get { read("historyLayout") ?? .init() }
        set { write(newValue, key: "historyLayout") }
    }
    struct SidebarLayout: Codable {
        var preferredWidth: Double? = nil
        var collapsed = false
        func width(in available: Double, minimum: Double, detailMinimum: Double) -> Double {
            let preferred = preferredWidth.flatMap { $0.isFinite ? $0 : nil } ?? min(240, available * 0.27)
            return min(max(minimum, preferred), max(minimum, available - detailMinimum - 6))
        }
    }
    var changesLayout: SidebarLayout {
        get { read("changesLayout") ?? .init() }
        set { write(newValue, key: "changesLayout") }
    }
    var activityLayout: SidebarLayout {
        get { read("activityLayout") ?? .init() }
        set { write(newValue, key: "activityLayout") }
    }
    var runsLayout: SidebarLayout {
        get { read("runsLayout") ?? .init() }
        set { write(newValue, key: "runsLayout") }
    }
    struct Comparison: Codable { var base: String; var file: String? }
    struct DiffAppearance: Codable {
        var fontSize: Double = 12
        var wrapLines: Bool = false
    }
    var diffAppearance: DiffAppearance {
        get {
            var value: DiffAppearance = read("diffAppearance") ?? .init()
            value.fontSize = value.fontSize.isFinite ? min(24, max(10, value.fontSize)) : 12
            return value
        }
        set { write(newValue, key: "diffAppearance") }
    }
    struct Position: Codable { var x: Double; var y: Double }
    /// A per-target entry with when it was last saved, so the store keeps
    /// the targets used most recently rather than the alphabetically last.
    struct Stamped<Value: Codable>: Codable {
        var value: Value
        var used: Double
    }
    private let defaults: UserDefaults?
    private let now: () -> Date
    init(defaults: UserDefaults? = nil, now: @escaping () -> Date = Date.init) {
        self.defaults = defaults
        self.now = now
    }

    var history: History? {
        get { read("history") }
        set { write(newValue, key: "history") }
    }
    func comparison(_ target: [String]) -> Comparison? {
        let values: [String: Stamped<Comparison>] = entries("comparisons")
        return values[target.joined(separator: "\u{0}")]?.value
    }
    func saveComparison(_ value: Comparison, target: [String]) {
        saveEntry(value, id: target.joined(separator: "\u{0}"), key: "comparisons")
    }
    func position(_ id: String) -> NSPoint {
        let values: [String: Stamped<Position>] = entries("positions")
        guard let value = values[id]?.value, value.x.isFinite, value.y.isFinite else { return .zero }
        return NSPoint(x: max(0, value.x), y: max(0, value.y))
    }
    func savePosition(_ point: NSPoint, id: String) {
        guard point.x.isFinite, point.y.isFinite else { return }
        saveEntry(Position(x: point.x, y: point.y), id: id, key: "positions")
    }
    /// Saving an entry is its use: it is stamped now and, once the store is
    /// full, the entry saved longest ago makes room for a new one.
    private func saveEntry<T: Codable>(_ value: T, id: String, key: String) {
        var values: [String: Stamped<T>] = entries(key)
        if values[id] == nil, values.count >= Self.entryLimit {
            let evicted = values.sorted { ($0.value.used, $0.key) < ($1.value.used, $1.key) }
                .prefix(values.count - Self.entryLimit + 1)
            for (oldest, _) in evicted { values.removeValue(forKey: oldest) }
        }
        values[id] = Stamped(value: value, used: now().timeIntervalSince1970)
        write(values, key: key)
    }
    /// Entries in the stamped format, or in the earlier unstamped
    /// `[id: value]` one, which reads as never used so it is evicted first.
    private func entries<T: Codable>(_ key: String) -> [String: Stamped<T>] {
        guard let data = defaults?.data(forKey: "amxReview." + key) else { return [:] }
        if let stamped = try? JSONDecoder().decode([String: Stamped<T>].self, from: data) { return stamped }
        do {
            let legacy = try JSONDecoder().decode([String: T].self, from: data)
            return legacy.mapValues { Stamped(value: $0, used: 0) }
        } catch {
            Self.logger.error("review state \(key, privacy: .public) unreadable, starting over: \(String(describing: error), privacy: .public)")
            return [:]
        }
    }
    private func read<T: Decodable>(_ key: String) -> T? {
        guard let data = defaults?.data(forKey: "amxReview." + key) else { return nil }
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            Self.logger.error("review state \(key, privacy: .public) unreadable, using defaults: \(String(describing: error), privacy: .public)")
            return nil
        }
    }
    private func write<T: Encodable>(_ value: T, key: String) {
        guard let defaults else { return }
        do {
            defaults.set(try JSONEncoder().encode(value), forKey: "amxReview." + key)
        } catch {
            Self.logger.error("review state \(key, privacy: .public) not saved: \(String(describing: error), privacy: .public)")
        }
    }
}
