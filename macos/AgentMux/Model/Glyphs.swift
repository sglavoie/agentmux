import Foundation

/// The state glyphs Agent Mux paints into Kitty tab titles, read from the
/// `internal/palette/icons.json` bundled with the app, so the menu bar and the
/// tabs draw a state with the same character.
struct Glyphs: Equatable {
    /// icon state → glyph (`"key"` → `"🔑"`).
    let states: [String: String]
    /// The order states are listed in, as the palette declares it.
    let stateOrder: [String]
    let mute: String
    let lock: String
    let loop: String

    /// The palette as of this app's build, for a bundle without the resource.
    static let fallback = Glyphs(
        states: [
            "bolt": "⚡", "stop": "🛑", "key": "🔑", "warning": "⚠️", "error": "❗",
            "subagent": "🤖", "compact": "🌀", "question": "❓",
        ],
        stateOrder: ["bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"],
        mute: "🔇",
        lock: "🔒",
        loop: "🔁"
    )

    /// The bundled palette, or `fallback` when it is missing or unreadable.
    static let bundled: Glyphs = {
        guard let url = Bundle.main.url(forResource: "icons", withExtension: "json"),
              let data = try? Data(contentsOf: url),
              let glyphs = Glyphs(json: data) else {
            return .fallback
        }
        return glyphs
    }()

    init(states: [String: String], stateOrder: [String], mute: String, lock: String, loop: String) {
        self.states = states
        self.stateOrder = stateOrder
        self.mute = mute
        self.lock = lock
        self.loop = loop
    }

    init?(json: Data) {
        struct Palette: Decodable {
            let states: [String: String]?
            let stateOrder: [String]?
            let mute: String?
            let lock: String?
            let loop: String?
            enum CodingKeys: String, CodingKey {
                case states, mute, lock, loop
                case stateOrder = "state_order"
            }
        }
        guard let palette = try? JSONDecoder().decode(Palette.self, from: json),
              let states = palette.states, !states.isEmpty else {
            return nil
        }
        let fallback = Glyphs.fallback
        self.states = states
        stateOrder = palette.stateOrder ?? fallback.stateOrder
        mute = palette.mute ?? fallback.mute
        lock = palette.lock ?? fallback.lock
        loop = palette.loop ?? fallback.loop
    }

    /// The glyph for `state`; empty for an idle session or an unknown state.
    func glyph(for state: String) -> String {
        // A completed turn is a successful outcome, not a stopped process.
        state == "stop" ? "✓" : states[state] ?? ""
    }
}
