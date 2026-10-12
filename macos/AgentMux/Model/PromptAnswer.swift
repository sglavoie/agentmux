import Foundation

// Answering the prompt a session is showing, from a banner's Answer… action.
//
// The app decides nothing about whether a session may be written to. It asks
// `amx send`, which reads the session's screen before every key and refuses
// one that would land on something other than a prompt; this file holds what
// the panel shows and the two invocations it makes. The first is always
// `amx send --look`: the operator sees the prompt before any key can be
// pressed, which is the condition a permission banner's action is offered on.

/// The payload of `amx send RUNTIME_SESSION_ID --look --json`.
struct PromptLook: Decodable, Equatable {
    var project = ""
    /// What the screen says: blocked, working, idle, or unknown.
    var screen = ""
    /// The last rows of the session's screen.
    var lines: [String] = []

    enum CodingKeys: String, CodingKey { case project, screen, lines }

    init(project: String = "", screen: String = "", lines: [String] = []) {
        self.project = project
        self.screen = screen
        self.lines = lines
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        project = try c.decodeIfPresent(String.self, forKey: .project) ?? ""
        screen = try c.decodeIfPresent(String.self, forKey: .screen) ?? ""
        lines = try c.decodeIfPresent([String].self, forKey: .lines) ?? []
    }

    /// The screen is asking something, so a key has a prompt to answer.
    var asking: Bool { screen == "blocked" }

    static func arguments(runtimeSessionID: String) -> [String] {
        ["send", runtimeSessionID, "--look"]
    }
}

/// The payload of `amx send RUNTIME_SESSION_ID --key … --json`.
struct PromptSent: Decodable, Equatable {
    /// accepted, consumed, or replayed.
    var delivery = ""

    enum CodingKeys: String, CodingKey { case delivery }

    init(delivery: String = "") { self.delivery = delivery }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        delivery = try c.decodeIfPresent(String.self, forKey: .delivery) ?? ""
    }
}

/// One key the panel can press, by the name `amx send --key` takes.
struct PromptKey: Equatable, Identifiable {
    /// The name `amx send --key` takes.
    let name: String
    /// What the button shows.
    let label: String
    /// What the button's tooltip says.
    let help: String

    var id: String { name }

    /// The choices of a numbered prompt.
    static let digits: [PromptKey] = (1...9).map { PromptKey(name: String($0), label: String($0), help: "Choose option \($0)") }

    /// Moving through a prompt's options and taking the highlighted one.
    static let navigation: [PromptKey] = [
        PromptKey(name: "up", label: "↑", help: "Move up"),
        PromptKey(name: "down", label: "↓", help: "Move down"),
        PromptKey(name: "enter", label: "↩", help: "Take the highlighted option"),
    ]

    /// Yes, no, and backing out of the prompt.
    static let answers: [PromptKey] = [
        PromptKey(name: "y", label: "y", help: "Press y"),
        PromptKey(name: "n", label: "n", help: "Press n"),
        PromptKey(name: "escape", label: "esc", help: "Press Escape in the session"),
    ]

    static let all: [PromptKey] = digits + navigation + answers

    /// The key a character typed in the panel presses; nil for one the panel
    /// does not forward. Escape is not here: in the panel it closes the panel,
    /// and the session's Escape is the button.
    static func typed(_ character: Character) -> PromptKey? {
        switch character {
        case "\r", "\n": return all.first { $0.name == "enter" }
        default: return (digits + answers.prefix(2)).first { $0.name == String(character) }
        }
    }
}

/// The session a banner's Answer… was chosen for.
struct PromptAnswerTarget: Equatable {
    let runtimeSessionID: String
}

/// What the panel asks `amx` for next.
enum PromptAnswerEffect: Equatable {
    /// `settle` gives a session that was just written to time to redraw.
    case look(seq: Int, settle: Bool)
    case press(seq: Int, arguments: [String])
}

/// Everything the panel shows, and the rules for what a click may do.
///
/// A key is pressed only once a look has shown a screen that is asking
/// something, and never while another key is on its way: the screen the
/// operator chose a key for is about to change.
struct PromptAnswerState: Equatable {
    enum Phase: Equatable {
        /// The first look has not answered.
        case loading
        case open
        /// The session cannot be looked at; the sentence says why.
        case refused(String)
    }

    private(set) var target: PromptAnswerTarget
    private(set) var phase = Phase.loading
    private(set) var look = PromptLook()
    /// A key or a look is on its way.
    private(set) var inFlight = true
    /// The operator was told the session runs an agent-session-loop run; the
    /// next key says so.
    private(set) var confirmed = false
    /// The key held back for that confirmation.
    private(set) var awaitingConfirmation: PromptKey?
    private(set) var notice = ""
    private(set) var seq = 0

    /// Opens on `target` and asks for the first look.
    static func open(_ target: PromptAnswerTarget, after seq: Int = 0) -> (PromptAnswerState, PromptAnswerEffect) {
        var state = PromptAnswerState(target: target)
        state.seq = seq + 1
        return (state, .look(seq: state.seq, settle: false))
    }

    private init(target: PromptAnswerTarget) { self.target = target }

    /// How the panel names the session.
    var title: String { look.project.isEmpty ? "Session" : look.project }

    /// Whether a key may be pressed now.
    var canPress: Bool { phase == .open && !inFlight && look.asking }

    /// One line under the screen saying what it is.
    var caption: String {
        switch look.screen {
        case "blocked": return "It is asking something. Press the key that answers it."
        case "working": return "It is working; there is nothing to answer."
        case "idle": return "It is at rest; there is nothing to answer."
        default: return "Nothing here is recognized as a prompt."
        }
    }

    /// Asks for another look, when none and no key is on its way.
    mutating func refresh() -> PromptAnswerEffect? {
        guard phase == .open, !inFlight else { return nil }
        seq += 1
        inFlight = true
        return .look(seq: seq, settle: false)
    }

    /// Presses `key`. Returns nil when no key may be pressed now.
    mutating func press(_ key: PromptKey) -> PromptAnswerEffect? {
        guard canPress else { return nil }
        if awaitingConfirmation != nil, awaitingConfirmation != key {
            // A different key is a different decision: ask again for it.
            confirmed = false
        }
        awaitingConfirmation = nil
        seq += 1
        inFlight = true
        notice = ""
        var arguments = ["send", target.runtimeSessionID, "--key", key.name]
        if confirmed { arguments.append("--yes") }
        pending = key
        return .press(seq: seq, arguments: arguments)
    }

    /// The key the press in flight carries.
    private var pending: PromptKey?

    mutating func looked(seq answered: Int, _ result: AMXCommandResult<PromptLook>) {
        guard answered == seq else { return }
        inFlight = false
        switch result {
        case let .success(value), let .partial(value, _):
            look = value
            phase = .open
        case let .failure(error):
            // A look that fails after the panel opened keeps the last screen
            // on show, with the reason; a first look that fails has none.
            if phase == .loading {
                phase = .refused(Self.text(error))
            } else {
                notice = Self.text(error)
            }
        }
    }

    /// A press answered. A key that was written is followed by a look, so the
    /// panel shows what it led to.
    mutating func pressed(seq answered: Int, _ result: AMXCommandResult<PromptSent>) -> PromptAnswerEffect? {
        guard answered == seq else { return nil }
        let key = pending
        pending = nil
        switch result {
        case .success, .partial:
            confirmed = false
            notice = "Sent."
        case let .failure(.command(error, _)) where error.code == "confirmation_required":
            inFlight = false
            confirmed = true
            awaitingConfirmation = key
            notice = "This session runs an agent-session-loop run, and what is sent reaches its supervisor. Press the key again to confirm."
            return nil
        case let .failure(error):
            notice = Self.text(error)
        }
        seq += 1
        return .look(seq: seq, settle: true)
    }

    /// What a refusal means in the panel. `amx send`'s own sentences name its
    /// flags; the refusals the operator can act on here are worded for here.
    static func text(_ error: AMXCommandError) -> String {
        guard case let .command(json, _) = error else { return error.message }
        switch json.code {
        case "no_prompt_showing":
            return "The session is not asking anything now, so the key was not pressed."
        case "screen_unreadable":
            return "The session's screen could not be read, so the key was not pressed. Open the session to answer it there."
        case "asl_question_open":
            return "This session has an agent-session-loop question open. Answer it from the run's own Answer…."
        case "unsupported_backend":
            return "Only a session in the workspace can be answered here; this one is a Kitty tab."
        case "not_found", "runtime_session_exited", "runtime_session_lost", "runtime_session_ended":
            return "This session's agent is no longer running."
        default:
            return json.message.isEmpty ? json.code : json.message
        }
    }
}
