import Foundation

/// What the workspace's right sidebar is turned to.
enum WorkspaceInspectorTab: String, CaseIterable, Equatable {
    /// Every session waiting on the operator, whichever is shown.
    case waiting
    /// The session the pane is on.
    case session

    var title: String {
        switch self {
        case .waiting: return "Waiting"
        case .session: return "Session"
        }
    }
}

/// What Answer… on a waiting session opens: one of the two panels the menu
/// and the banners already open. Neither is decided here to be sendable;
/// each panel asks before it offers anything.
enum WorkspaceWaitingAnswer: Equatable {
    /// The supervisor question of the agent-session-loop run bound to the
    /// session, whichever terminal it runs in.
    case asl(ASLAnswerTarget)
    /// The prompt an embedded session is showing.
    case prompt(runtimeSessionID: String)
}

/// One session waiting on the operator, as the right sidebar lists it.
struct WorkspaceWaitingRow: Equatable {
    let key: AttentionCycler.Key
    /// The name the operator gave the session, else its project and profile.
    let title: String
    /// `needs permission · 4m`.
    let detail: String
    let glyph: String
    let toolTip: String
    /// What can be answered from the row without going to the session; nil
    /// when there is nothing to answer here, and the row only goes there.
    var answer: WorkspaceWaitingAnswer?
}

/// What the right sidebar says about the session the pane is on.
struct WorkspaceInspection: Equatable {
    struct Field: Equatable {
        let label: String
        let value: String
        /// Whether a click copies the value: an identifier worth pasting.
        var copies = false
    }

    struct Section: Equatable {
        let title: String
        let fields: [Field]
    }

    let key: AttentionCycler.Key
    let title: String
    let glyph: String
    let sections: [Section]
    /// Whether the workspace embeds the session. A Kitty session is named,
    /// colored, and given its icon in Kitty.
    let embedded: Bool
    let muted: Bool
}

/// What a button of the right sidebar asks for the session the pane is on:
/// what ⌘R and the session function keys do.
enum WorkspaceInspectorAction: CaseIterable, Equatable {
    case name, color, icon, mute, clearState, markActive

    /// Whether the action is one only an embedded session has here.
    var needsEmbedded: Bool {
        switch self {
        case .name, .color, .icon: return true
        case .mute, .clearState, .markActive: return false
        }
    }

    func title(muted: Bool) -> String {
        switch self {
        case .name: return "Name…"
        case .color: return "Color…"
        case .icon: return "Icon…"
        case .mute: return muted ? "Unmute" : "Mute"
        case .clearState: return "Clear State"
        case .markActive: return "Mark Active"
        }
    }

    /// The key that does the same.
    var shortcut: String {
        switch self {
        case .name: return "⌘R"
        case .color: return "F6"
        case .icon: return "F5"
        case .mute: return "F10"
        case .clearState: return "F8"
        case .markActive: return "F11"
        }
    }
}

/// The right sidebar as data, built from a snapshot so it can be tested
/// without AppKit.
///
/// It lists what the left sidebar does not: only the sessions waiting on the
/// operator, in the order the next-attention shortcut visits them, which is
/// the order the left sidebar has them in. Like a utility
/// view it is not a session and adds nothing to what counts as waiting.
enum WorkspaceInspector {
    /// A state in words, for the states the palette draws as glyphs.
    static let stateWords = [
        "bolt": "working", "stop": "stopped", "key": "needs permission", "warning": "warning",
        "error": "error", "subagent": "running a subagent", "compact": "compacting", "question": "asked a question",
    ]
    static let idle = "idle"

    static func word(for iconState: String) -> String {
        iconState.isEmpty ? idle : stateWords[iconState] ?? iconState
    }

    /// The states in which a session shows a prompt a key can answer.
    static let promptStates: Set<String> = ["key", "question"]

    /// The sessions waiting on the operator, in the order `AttentionCycler`
    /// steps through: the sidebar's. `asl` is the snapshot's waiting runs: a
    /// session one is bound to is answered through its run's mailbox, never
    /// through its pane.
    static func waiting(
        _ sessions: [CompanionSession],
        asl: [CompanionASLWaiting] = [],
        snapshotSocket: String = "",
        now: Date = Date(),
        glyphs: Glyphs = .bundled,
        order: WorkspaceOrder = WorkspaceOrder()
    ) -> [WorkspaceWaitingRow] {
        let runs = MenuBuilder.waitingBySession(asl, sessions: sessions, snapshotSocket: snapshotSocket).bySession
        return AttentionCycler.ordered(sessions, snapshotSocket: snapshotSocket, order: order).map { session in
            let row = WorkspaceSidebar.row(session, snapshotSocket: snapshotSocket, now: now, glyphs: glyphs)
            var answer: WorkspaceWaitingAnswer?
            if let run = runs[row.key] {
                answer = .asl(MenuBuilder.target(run))
            } else if let id = row.embeddedRuntimeSessionID, promptStates.contains(session.iconState) {
                answer = .prompt(runtimeSessionID: id)
            }
            var detail = [session.reason.isEmpty ? word(for: session.iconState) : session.reason]
            if let changed = session.changedAt { detail.append(MenuBuilder.age(since: changed, now: now)) }
            if row.embeddedRuntimeSessionID == nil { detail.append(WorkspaceSidebar.kittyBadge) }
            return WorkspaceWaitingRow(
                key: row.key,
                title: title(of: row),
                detail: detail.joined(separator: " · "),
                glyph: row.glyph,
                toolTip: row.name.isEmpty ? row.toolTip : "\(row.project) — \(row.label) · \(row.toolTip)",
                answer: answer
            )
        }
    }

    /// What there is to say about one session. Empty values are left out,
    /// and a section with nothing in it is too.
    static func inspection(
        _ session: CompanionSession,
        snapshotSocket: String = "",
        now: Date = Date(),
        glyphs: Glyphs = .bundled
    ) -> WorkspaceInspection {
        typealias Field = WorkspaceInspection.Field
        let row = WorkspaceSidebar.row(session, snapshotSocket: snapshotSocket, now: now, glyphs: glyphs)

        var status = [Field(label: "State", value: word(for: session.iconState))]
        status.append(Field(label: "Reason", value: session.reason))
        if let changed = session.changedAt {
            status.append(Field(label: "Since", value: MenuBuilder.age(since: changed, now: now)))
        }
        if session.isWorking, session.busySinceAt > 0 {
            status.append(Field(label: "Working for", value: age(session.busySinceAt, now: now)))
        }
        if session.lastEventAt > 0 {
            status.append(Field(label: "Last event", value: age(session.lastEventAt, now: now)))
        }
        var flags: [String] = []
        if session.attention { flags.append("waiting on you") }
        if session.unseen { flags.append("unseen") }
        if session.muted { flags.append("muted") }
        if session.locked { flags.append("locked") }
        if session.looping { flags.append("looping") }
        status.append(Field(label: "Flags", value: flags.joined(separator: ", ")))

        let identity = [
            Field(label: "Name", value: session.name),
            Field(label: "Project", value: row.project),
            Field(label: "Profile", value: session.profile),
            Field(label: "Harness", value: session.harness),
            Field(label: "Conversation", value: session.conversation?.sessionID ?? "", copies: true),
            Field(label: "Runtime", value: session.runtime?.sessionID ?? "", copies: true),
            Field(label: "Terminal", value: MenuBuilder.place(of: session)),
            Field(label: "ASL run", value: session.aslRunID, copies: true),
        ]

        var git: [Field] = []
        if !session.branch.isEmpty {
            git = [
                Field(label: "Branch", value: session.branch, copies: true),
                Field(label: "Changes", value: session.dirty ? "uncommitted" : "clean"),
            ]
        }

        let all = [("Status", status), ("Identity", identity), ("Git", git)]
        let sections = all.compactMap { title, fields -> WorkspaceInspection.Section? in
            let filled = fields.filter { !$0.value.isEmpty }
            return filled.isEmpty ? nil : WorkspaceInspection.Section(title: title, fields: filled)
        }
        return WorkspaceInspection(
            key: row.key,
            title: title(of: row),
            glyph: row.glyph,
            sections: sections,
            embedded: row.embeddedRuntimeSessionID != nil,
            muted: session.muted
        )
    }

    private static func title(of row: WorkspaceRow) -> String {
        row.name.isEmpty ? "\(row.project) — \(row.label)" : row.name
    }

    private static func age(_ seconds: Int64, now: Date) -> String {
        MenuBuilder.age(since: Date(timeIntervalSince1970: TimeInterval(seconds)), now: now)
    }
}
