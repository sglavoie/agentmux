import Foundation
import Combine

struct FlightMetrics: Decodable {
    let launches, fresh, resumes, forks, restarts, conversations, projects: Int
    let stateChanges, turnsFinished, questions, permissions, errors, runsFinished, peakWorking: Int
    let workingSeconds, waitingSeconds, observedSeconds: Double
    enum CodingKeys: String, CodingKey {
        case launches, fresh, resumes, forks, restarts, conversations, projects, questions, permissions, errors
        case stateChanges = "state_changes", turnsFinished = "turns_finished", runsFinished = "runs_finished", peakWorking = "peak_working"
        case workingSeconds = "working_seconds", waitingSeconds = "waiting_seconds", observedSeconds = "observed_seconds"
    }
}

struct FlightEvent: Decodable, Identifiable {
    let id, at, kind: String
    let icon, beforeIcon, name, traceID, conversationID: String?
    let looping, beforeLooping: Bool?
    enum CodingKeys: String, CodingKey {
        case id, at, kind, icon, name, looping
        case beforeIcon = "before_icon", beforeLooping = "before_looping", traceID = "trace_id"
        case conversationID = "conversation_id"
    }
    var date: Date? { WorkspaceActivityEntry.date(at) }
    var conversation: ConversationIdentity? { conversationID.flatMap(ConversationIdentity.init(encoded:)) }
    var label: String {
        switch kind {
        case "state": return Self.stateName(beforeIcon ?? "") + " → " + Self.stateName(icon ?? "")
        case "launch": return "Session launched"
        case "run": return name ?? "ASL event"
        default: return name ?? kind.capitalized
        }
    }
    static func stateName(_ icon: String) -> String {
        switch icon {
        case "", "idle": return "Idle"
        case "bolt": return "Working"
        case "subagent": return "Subagent working"
        case "compact": return "Compacting context"
        case "stop": return "Turn finished"
        case "key": return "Permission requested"
        case "question": return "Question"
        case "error": return "Error"
        case "warning": return "Warning"
        default: return icon.capitalized
        }
    }
}

struct FlightSegment: Decodable {
    let from, to, icon: String
    let looping, observed: Bool
    var start: Date? { WorkspaceActivityEntry.date(from) }
    var end: Date? { WorkspaceActivityEntry.date(to) }
    var seconds: Double { max(0, (end ?? .distantPast).timeIntervalSince(start ?? .distantPast)) }
}

struct FlightSession: Decodable, Identifiable {
    let key, runtimeSessionID, launchID, conversationID, harness, profile, project, operation, firstAt, lastAt: String
    let metrics: FlightMetrics
    let stateCounts: [String: Int]
    let timeline: [FlightSegment]
    let events: [FlightEvent]
    let partial: Bool
    var id: String { key }
    var conversation: ConversationIdentity? { ConversationIdentity(encoded: conversationID) }
    var title: String { project.isEmpty ? key : project }
    enum CodingKeys: String, CodingKey {
        case key, harness, profile, project, operation, metrics, timeline, events, partial
        case runtimeSessionID = "runtime_session_id", launchID = "launch_id", conversationID = "conversation_id"
        case firstAt = "first_at", lastAt = "last_at", stateCounts = "state_counts"
    }
}

struct FlightDay: Decodable, Identifiable {
    let date: String
    let metrics: FlightMetrics
    var id: String { date }
}

struct FlightAchievement: Decodable, Identifiable {
    let id, title, description, sessionKey: String
    let progress, target: Int
    let unlockedAt: String?
    enum CodingKeys: String, CodingKey {
        case id, title, description, progress, target
        case sessionKey = "session_key", unlockedAt = "unlocked_at"
    }
}

struct FlightSettings: Decodable {
    let enabled, celebrations: Bool
}

struct FlightMission: Decodable, Identifiable {
    let id, title, project, status: String
    let references: [String]
    let shippedAt: String?
    enum CodingKeys: String, CodingKey {
        case id, title, project, status, references
        case shippedAt = "shipped_at"
    }
    var statusLabel: String {
        switch status {
        case "in_progress": return "In progress"
        case "review": return "Ready for review"
        case "shipped": return "Shipped"
        default: return "Briefed"
        }
    }
}

struct FlightReport: Decodable {
    let trackingSince, generatedAt, timezone: String
    let settings: FlightSettings
    let totals, lifetime: FlightMetrics
    let days: [FlightDay]
    let sessions: [FlightSession]
    let achievements: [FlightAchievement]
    let missions: [FlightMission]?
    let recap, warnings, projects, harnesses, profiles: [String]
    var trackingDate: Date? { trackingSince.hasPrefix("0001-") ? nil : WorkspaceActivityEntry.date(trackingSince) }
    enum CodingKeys: String, CodingKey {
        case timezone, settings, totals, lifetime, days, sessions, achievements, missions, recap, warnings, projects, harnesses, profiles
        case trackingSince = "tracking_since", generatedAt = "generated_at"
    }
}

enum FlightHeatMetric: String, CaseIterable, Identifiable {
    case launches = "Launches", turns = "Turns finished", work = "Working time"
    var id: String { rawValue }
    func value(_ metrics: FlightMetrics) -> Double {
        switch self { case .launches: return Double(metrics.launches); case .turns: return Double(metrics.turnsFinished); case .work: return metrics.workingSeconds }
    }
    func label(_ metrics: FlightMetrics) -> String {
        switch self { case .launches: return "\(metrics.launches) launches"; case .turns: return "\(metrics.turnsFinished) turns finished"; case .work: return flightDuration(metrics.workingSeconds) + " observed work" }
    }
}

func flightDuration(_ seconds: Double) -> String {
    let total = Int(max(0, seconds))
    if total >= 3600 { return "\(total / 3600)h \((total % 3600) / 60)m" }
    if total >= 60 { return "\(total / 60)m \(total % 60)s" }
    return "\(total)s"
}

@MainActor
final class WorkspaceFlightLogModel: ObservableObject {
    @Published var period = "week"
    @Published var project = ""
    @Published var harness = ""
    @Published var profile = ""
    @Published var heatMetric = FlightHeatMetric.launches
    @Published var selectedDay: String?
    @Published var selection: String?
    @Published var eventKind = "all"
    @Published private(set) var report: FlightReport?
    @Published private(set) var loading = false
    @Published private(set) var acting = false
    @Published private(set) var error: String?
    @Published private(set) var actionMessage: String?
    @Published private(set) var celebration: String?
    var onConversation: ((ConversationIdentity, String?) -> Void)?
    var onActivity: ((FlightSession, String) -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var generation = 0
    private var seenUnlocks: Set<String>?
    private var trackingPeriod: String?

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.command = command; self.runner = runner
    }
    var selected: FlightSession? { report?.sessions.first { $0.key == selection } }
    var visibleSessions: [FlightSession] {
        return (report?.sessions ?? []).filter { session in
            let kindMatches = eventKind == "all" || session.events.contains { $0.kind == "state" && ($0.icon ?? "") == eventKind && ($0.beforeIcon ?? "") != ($0.icon ?? "") && (selectedDay == nil || dateKey($0.date) == selectedDay) }
            guard kindMatches else { return false }
            guard let day = selectedDay else { return true }
            return session.events.contains { dateKey($0.date) == day } || session.timeline.contains { segment in
                guard segment.observed, let start = dateKey(segment.start), let end = segment.end,
                      let lastDay = dateKey(end.addingTimeInterval(-0.001)) else { return false }
                return start <= day && lastDay >= day
            }
        }
    }
    var visibleEvents: [FlightEvent] {
        (selected?.events ?? []).filter {
            (selectedDay == nil || dateKey($0.date) == selectedDay) && (eventKind == "all" || $0.kind == "state" && ($0.icon ?? "") == eventKind && ($0.beforeIcon ?? "") != ($0.icon ?? ""))
        }
    }
    func dateKey(_ date: Date?) -> String? {
        guard let date else { return nil }
        let formatter = DateFormatter(); formatter.dateFormat = "yyyy-MM-dd"
        if let zone = report?.timezone { formatter.timeZone = TimeZone(identifier: zone) ?? .current }
        return formatter.string(from: date)
    }
    func refresh() {
        generation += 1; let request = generation
        guard let command = command() else { loading = false; error = AMXWording.current; return }
        loading = true; error = nil
        var args = ["session", "flight-log", "--period", period]
        for (flag, value) in [("project", project), ("harness", harness), ("profile", profile)] where !value.isEmpty { args += ["--" + flag, value] }
        runner.run({ command.json(FlightReport.self, args, timeout: 30) }) { [weak self] result in
            guard let self, self.generation == request else { return }
            self.loading = false
            switch result {
            case let .failure(error): self.error = error.message
            case let .success(report), let .partial(report, _):
                let unlocked = Set(report.achievements.filter { $0.unlockedAt != nil }.map(\.id))
                if self.trackingPeriod == report.trackingSince, let previous = self.seenUnlocks, report.settings.celebrations {
                    let titles = report.achievements.filter { unlocked.subtracting(previous).contains($0.id) }.map(\.title)
                    if !titles.isEmpty { self.celebration = "Milestone unlocked: " + titles.joined(separator: ", ") }
                }
                self.seenUnlocks = unlocked; self.trackingPeriod = report.trackingSince; self.report = report
                if !self.visibleSessions.contains(where: { $0.key == self.selection }) { self.selection = self.visibleSessions.first?.key }
            }
        }
    }
    func selectDay(_ date: String) {
        selectedDay = selectedDay == date ? nil : date
        // The heatmap spans a year; a day outside this week's filter must
        // open its actual sessions instead of an empty weekly subset.
        if selectedDay != nil, period != "all" { period = "all"; refresh() }
        if !visibleSessions.contains(where: { $0.key == selection }) { selection = visibleSessions.first?.key }
    }
    func revealAchievement(_ achievement: FlightAchievement) {
        project = ""; harness = ""; profile = ""; period = "all"; selectedDay = nil
        selection = achievement.sessionKey; eventKind = "all"; refresh()
    }
    func openConversation(at: String? = nil) {
        let identity: ConversationIdentity?
        if let at { identity = selected?.events.first { $0.at == at }?.conversation }
        else { identity = selected?.conversation }
        guard let identity else { return }
        onConversation?(identity, at)
    }
    func inspect(_ kind: String) { eventKind = kind; normalizeSelection() }
    func normalizeSelection() {
        if !visibleSessions.contains(where: { $0.key == selection }) { selection = visibleSessions.first?.key }
    }
    func saveMission(id: String? = nil, title: String, project: String, status: String, references: [String], onSaved: (() -> Void)? = nil) {
        guard !acting, let command = command() else { return }
        var args = ["session", "flight-log", "mission", id == nil ? "create" : "update", "--status", status]
        if let id {
            args += ["--id", id]
        } else {
            args += ["--title", title, "--project", project]
            for reference in references { args += ["--reference", reference] }
        }
        acting = true; actionMessage = nil
        runner.run({ command.json(FlightMission.self, args, mutates: true) }) { [weak self] result in
            guard let self else { return }
            self.acting = false
            switch result {
            case let .failure(error): self.actionMessage = error.message
            case .success, .partial: self.actionMessage = "Mission saved."; onSaved?(); self.refresh()
            }
        }
    }
    func settings(enabled: Bool? = nil, celebrations: Bool? = nil) {
        var args = ["session", "flight-log", "settings"]
        if let enabled { args.append("--enabled=\(enabled)") }
        if let celebrations { args.append("--celebrations=\(celebrations)") }
        mutate(args, success: "Flight Log preferences saved.")
    }
    func reset() {
        selection = nil; selectedDay = nil; seenUnlocks = nil; celebration = nil
        mutate(["session", "flight-log", "reset", "--yes"], success: "History reset. A new tracking period has started.")
    }
    private func mutate(_ args: [String], success: String) {
        guard !acting else { return }
        guard let command = command() else { actionMessage = AMXWording.current; return }
        acting = true; actionMessage = nil
        runner.run({ command.json(FlightSettings.self, args, mutates: true) }) { [weak self] result in
            guard let self else { return }; self.acting = false
            switch result {
            case let .failure(error): self.actionMessage = error.message
            case .success, .partial: self.actionMessage = success; self.refresh()
            }
        }
    }
    func importHistory() {
        guard !acting else { return }
        guard let command = command() else { actionMessage = AMXWording.current; return }
        struct Imported: Decodable { let imported: Int }
        acting = true
        runner.run({ command.json(Imported.self, ["session", "flight-log", "import"], timeout: 30, mutates: true) }) { [weak self] result in
            guard let self else { return }; self.acting = false
            switch result {
            case let .failure(error): self.actionMessage = error.message
            case let .success(value), let .partial(value, _): self.actionMessage = "Imported \(value.imported) partial historical events."; self.refresh()
            }
        }
    }
    func export(to url: URL) {
        guard !acting else { return }
        guard let command = command() else { actionMessage = AMXWording.current; return }
        acting = true
        runner.run({ () -> String? in
            switch command.run(["session", "flight-log", "export", "--json"], timeout: 30) {
            case let .failure(error): return error.message
            case let .success(output):
                guard output.status == 0 else { return "Flight Log export failed. History was preserved." }
                do { try output.stdout.write(to: url, options: .atomic); return nil }
                catch { return "Could not save the export: \(error.localizedDescription)" }
            }
        }) { [weak self] error in
            self?.acting = false; self?.actionMessage = error ?? "Flight Log exported."
        }
    }
    func dismissCelebration() { celebration = nil }
    func close() { generation += 1; loading = false; seenUnlocks = nil; trackingPeriod = nil; celebration = nil }
}
