import Foundation

/// Presentation preferences only: no transcript, diff, command, or authority is
/// saved. Tests opt into an isolated defaults suite; production supplies standard.
final class WorkspaceReviewState {
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
    private let defaults: UserDefaults?
    init(defaults: UserDefaults? = nil) { self.defaults = defaults }

    var history: History? {
        get { read("history") }
        set { write(newValue, key: "history") }
    }
    func comparison(_ target: [String]) -> Comparison? {
        let values: [String: Comparison] = read("comparisons") ?? [:]
        return values[target.joined(separator: "\u{0}")]
    }
    func saveComparison(_ value: Comparison, target: [String]) {
        saveEntry(value, id: target.joined(separator: "\u{0}"), key: "comparisons")
    }
    func position(_ id: String) -> NSPoint {
        let values: [String: Position] = read("positions") ?? [:]
        guard let value = values[id], value.x.isFinite, value.y.isFinite else { return .zero }
        return NSPoint(x: max(0, value.x), y: max(0, value.y))
    }
    func savePosition(_ point: NSPoint, id: String) {
        guard point.x.isFinite, point.y.isFinite else { return }
        saveEntry(Position(x: point.x, y: point.y), id: id, key: "positions")
    }
    private func saveEntry<T: Codable>(_ value: T, id: String, key: String) {
        var values: [String: T] = read(key) ?? [:]
        if values[id] == nil, values.count >= 100, let oldest = values.keys.sorted().first { values.removeValue(forKey: oldest) }
        values[id] = value
        write(values, key: key)
    }
    private func read<T: Decodable>(_ key: String) -> T? {
        guard let data = defaults?.data(forKey: "amxReview." + key) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }
    private func write<T: Encodable>(_ value: T, key: String) {
        guard let defaults, let data = try? JSONEncoder().encode(value) else { return }
        defaults.set(data, forKey: "amxReview." + key)
    }
}
