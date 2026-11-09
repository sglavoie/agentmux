import Foundation

/// Where focusing a session goes.
enum WorkspaceTarget: Equatable {
    /// A terminal embedded in the workspace, attached to this runtime session.
    case embedded(runtimeSessionID: String)
}

/// The color a session's line is drawn on, as the daemon publishes it.
struct WorkspaceTabColor: Equatable {
    let red: Double
    let green: Double
    let blue: Double

    /// Reads "#rrggbb"; nil for anything else, which is no color.
    init?(hex: String) {
        let digits = hex.dropFirst()
        guard hex.hasPrefix("#"), digits.utf8.count == 6, let value = UInt32(digits, radix: 16) else { return nil }
        red = Double((value >> 16) & 0xff) / 255
        green = Double((value >> 8) & 0xff) / 255
        blue = Double(value & 0xff) / 255
    }
}

/// One session in the workspace sidebar.
struct WorkspaceRow: Equatable {
    let key: AttentionCycler.Key
    let target: WorkspaceTarget
    /// The project the row is listed under.
    let project: String
    /// What tells the row apart inside its project: the profile it runs as.
    let label: String
    /// `claude · 4m · permission`.
    let detail: String
    /// The state glyph the tab title and the menu use; empty when idle.
    let glyph: String
    let attention: Bool
    let working: Bool
    let muted: Bool
    let toolTip: String
    /// What the line is drawn on while it is not the selected one: the color
    /// picked for the session, else its profile's.
    var tabColor: WorkspaceTabColor?
    /// The name the operator gave the session, shown on a line of its own
    /// above the label; empty for none, and the row then looks as it always
    /// did.
    var name = ""
    /// The session's harness icon, shown ahead of the label: its harness's
    /// own until the icon picker replaces or clears it. Empty for none, and
    /// only an embedded session has one.
    var harnessIcon = ""
    /// The session's harness, which orders rows of one profile.
    var harness = ""
    /// The configured profile, before the display label's harness fallback.
    var profile = ""
    /// Whether the session is pinned (the ★ in its title). Every layout lists
    /// pins first. State-first can promote attention from a collapsed Pinned group.
    var pinned = false
    /// Temporarily listed below pinned sessions and above projects until acknowledged.
    var needsAttention = false
    /// The conversation the session's agent is having, once the daemon knows
    /// it: what Past Sessions is opened on if the agent stops.
    var conversation: ConversationIdentity?
    /// A stable discriminator when several live rows have the same name.
    var qualifier = ""
    var branch = ""
    /// The session's icon state (`stop`, `bolt`, …); empty when idle. What
    /// the line's background under a project says.
    var iconState = ""
    var review = SessionReview()

    var taskMetadata: String {
        var parts = [project, harness.isEmpty ? label : harness]
        if !profile.isEmpty && profile != harness { parts.append(profile) }
        if !branch.isEmpty { parts.append(branch) }
        if !qualifier.isEmpty && qualifier != branch { parts.append(qualifier) }
        return parts.filter { !$0.isEmpty }.joined(separator: " · ")
    }

    var sessionLabel: String { qualified(label) }
    var namedLabel: String { name.isEmpty ? "" : qualified(name) }
    private func qualified(_ value: String) -> String {
        qualifier.isEmpty ? value : "\(value) · \(qualifier)"
    }

    /// What the session is called in titles, alerts, and the inspector:
    /// its name when it was given one, else its project and profile.
    var displayName: String {
        name.isEmpty ? "\(project) — \(sessionLabel)" : namedLabel
    }

    var embeddedRuntimeSessionID: String? {
        if case let .embedded(id) = target { return id }
        return nil
    }
}

/// The state section of the sidebar a session is listed under, by its icon
/// state, most urgent first. `WorkspaceSidebarBand` draws a session's line
/// by the same reading.
enum WorkspaceStateSection: String, CaseIterable, Equatable {
    /// Waiting on the operator: a permission, a question, a warning, an error.
    case waiting = "Waiting"
    /// Its turn ended (✓).
    case done = "Done"
    /// At work (⚡, 🤖, 🌀).
    case working = "Working"
    /// No state glyph.
    case idle = "Idle"

    init(iconState: String) {
        switch iconState {
        case "": self = .idle
        case "stop": self = .done
        case "key", "question", "warning", "error": self = .waiting
        default: self = .working
        }
    }
}

/// The hierarchy used by the workspace sidebar.
enum WorkspaceSidebarGrouping: String, CaseIterable {
    case stateThenProject
    case projectThenState
    case project
    case compact
    case projectThenBranch

    var title: String {
        switch self {
        case .stateThenProject: return "State → Project"
        case .projectThenState: return "Project → State"
        case .project: return "Projects"
        case .compact: return "Compact list"
        case .projectThenBranch: return "Projects → Branches"
        }
    }

    var detail: String {
        switch self {
        case .stateThenProject: return "Groups sessions by state, then project, with pinned and attention sessions above them."
        case .projectThenState: return "Groups sessions by project, then state, below pinned sessions."
        case .project: return "Keeps sessions in a stable order within each project, below pinned sessions."
        case .compact: return "Lists pinned sessions first without project headings. Each row names its project."
        case .projectThenBranch: return "Lists pinned sessions first, then groups projects by branch. Single-branch projects omit the extra heading."
        }
    }
}

enum WorkspaceSidebarSort: String, CaseIterable {
    case manual
    case recentlyVisited

    var title: String { self == .manual ? "Manual order" : "Recently visited" }
}

/// The sessions of one project in one state section, the pinned ones of
/// every project, or the heading of a state section.
struct WorkspaceGroup: Equatable {
    /// The project's name; `WorkspaceSidebar.pinnedSection` for the pinned,
    /// and the section's title for a state section's heading.
    let project: String
    var rows: [WorkspaceRow]
    /// True for the group of pinned sessions, whose rows keep the project
    /// they belong to.
    var pinned = false
    /// True when the operator collapsed it: its header is listed, and still
    /// counts its waiting sessions, but its rows are not.
    var collapsed = false
    /// The temporary attention section is always visible and cannot collapse.
    var needsAttention = false
    /// The state represented by this group; nil for project headings,
    /// Pinned, and Needs attention.
    var stateSection: WorkspaceStateSection?
    /// True for a state heading. In state-first mode its project groups
    /// follow it; a nested state heading owns its rows directly.
    var isSectionHeading = false
    /// How many sessions a state or project heading stands for.
    var sessions = 0
    /// A project heading above its state or branch groups; it owns no rows itself.
    var isProjectHeading = false
    /// A state group nested inside a project, owning that state's rows.
    var isNestedState = false
    /// A branch nested under its project. Empty means no branch was reported.
    var branch: String?
    /// A non-collapsible container in the flat session list.
    var isFlat = false
    /// Waiting count for a project heading whose rows live in child groups.
    var waitingCount = 0
    var reviewLabel = ""
    var projectNote = ""

    var title: String {
        if let branch { return branch.isEmpty ? "No branch" : branch }
        return isNestedState ? stateSection?.rawValue ?? project : project
    }
    var isNested: Bool { isNestedState || branch != nil }
    var canCollapse: Bool { !needsAttention && !isFlat }

    /// How many of the project's sessions wait on a human.
    var attention: Int { waitingCount + rows.filter(\.attention).count }
}
