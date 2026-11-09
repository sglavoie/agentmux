import AppKit
import SwiftUI
import UniformTypeIdentifiers

@MainActor
final class WorkspaceFlightLogWindow: ReviewWindowController {
    let model: WorkspaceFlightLogModel
    private var timer: Timer?
    init(model: WorkspaceFlightLogModel) {
        self.model = model
        super.init(title: "Flight Log", autosaveName: "WorkspaceFlightLog", size: NSSize(width: 1060, height: 760))
    }
    func show() { present() }
    override func makeContentView() -> NSView { Self.host(WorkspaceFlightLogView(model: model)) }
    override func willPresent() { model.refresh() }
    override func applicationDidBecomeActive() { if !model.loading { model.refresh() } }
    override var canClose: Bool { !model.acting }
    override func readingLiveChanged(_ live: Bool) {
        timer?.invalidate(); timer = nil
        if live {
            timer = Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in
                MainActor.assumeIsolated {
                    guard let self, !self.model.loading, !self.model.acting else { return }
                    self.model.refresh()
                }
            }
        }
    }
    override func didClose() { timer?.invalidate(); timer = nil; model.close() }
}

struct WorkspaceFlightLogView: View {
    @ObservedObject var model: WorkspaceFlightLogModel
    @State private var tab = "overview"
    @State private var confirmingReset = false
    @State private var missionTitle = ""
    @State private var missionProject = ""
    @State private var missionReference = ""
    init(model: WorkspaceFlightLogModel, initialTab: String = "overview") {
        self.model = model; _tab = State(initialValue: initialTab)
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Image(systemName: "paperplane").font(.title2).foregroundStyle(.tint)
                Text("Flight Log").font(.title2)
                Spacer()
                if model.loading { ProgressView().controlSize(.small).accessibilityLabel("Refreshing Flight Log") }
                Button { model.refresh() } label: { Image(systemName: "arrow.clockwise") }
                    .help("Refresh Flight Log (⌘R)").accessibilityLabel("Refresh Flight Log").disabled(model.loading || model.acting)
                Menu("Manage") {
                    Toggle("Record Activity", isOn: Binding(get: { model.report?.settings.enabled ?? true }, set: { model.settings(enabled: $0) }))
                    Toggle("Milestone Celebrations", isOn: Binding(get: { model.report?.settings.celebrations ?? true }, set: { model.settings(celebrations: $0) }))
                    Divider()
                    Button("Export History…", action: export)
                    Button("Import Retained History") { model.importHistory() }
                    Divider()
                    Button("Reset History…", role: .destructive) { confirmingReset = true }
                }.fixedSize().disabled(model.acting)
            }
            HStack {
                Picker("Period", selection: $model.period) {
                    Text("Today").tag("today"); Text("Week").tag("week"); Text("All time").tag("all")
                }.pickerStyle(.segmented).labelsHidden().accessibilityLabel("Period").frame(width: 230).onChange(of: model.period) { _, _ in model.refresh() }
                filter("Project", value: $model.project, choices: model.report?.projects ?? [])
                filter("Harness", value: $model.harness, choices: model.report?.harnesses ?? [])
                filter("Profile", value: $model.profile, choices: model.report?.profiles ?? [])
            }.disabled(model.acting)
            if let report = model.report {
                HStack {
                    Text(report.trackingDate.map { "Tracking since " + $0.formatted(date: .abbreviated, time: .shortened) } ?? "Tracking has not started")
                    if !report.settings.enabled { Label("Tracking paused", systemImage: "pause.circle") }
                    Spacer()
                    Text("Updated " + (WorkspaceActivityEntry.date(report.generatedAt)?.formatted(date: .omitted, time: .shortened) ?? "—"))
                }.font(.caption).foregroundStyle(.secondary)
            }
            WorkspaceReadFailure(title: "Flight Log", error: model.error, retained: model.report != nil, loading: model.loading, retry: { model.refresh() })
            if let message = model.actionMessage { Text(message).font(.callout).textSelection(.enabled) }
            if let celebration = model.celebration {
                HStack {
                    Label(celebration, systemImage: "sparkles").foregroundStyle(.tint)
                    Spacer()
                    Button("Dismiss") { model.dismissCelebration() }
                }.padding(8).background(.tint.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
                    .accessibilityElement(children: .combine)
            }
            Picker("Flight Log view", selection: $tab) {
                Text("Missions").tag("missions"); Text("Overview").tag("overview"); Text("Sessions").tag("sessions"); Text("Milestones").tag("milestones")
            }.pickerStyle(.segmented).labelsHidden().frame(maxWidth: 540)
            Group {
                switch tab { case "missions": missions; case "sessions": sessions; case "milestones": milestones; default: overview }
            }.frame(maxWidth: .infinity, maxHeight: .infinity)
        }.padding(14)
            .confirmationDialog("Reset Flight Log history?", isPresented: $confirmingReset) {
                Button("Reset History", role: .destructive) { model.reset() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("This permanently deletes session statistics, missions, patches, milestones, and imported Flight Log history. Export first if you want a copy. A new tracking period will start.")
            }
            .background(WorkspaceReviewShortcuts { key, modifiers in
                guard key == "r", modifiers == .command else { return false }
                if !model.loading && !model.acting { model.refresh() }; return true
            }.frame(width: 0, height: 0))
    }
    private func filter(_ title: String, value: Binding<String>, choices: [String]) -> some View {
        Picker(title, selection: value) {
            Text(title == "Harness" ? "All harnesses" : "All \(title.lowercased())s").tag("")
            ForEach(choices, id: \.self) { Text($0).tag($0) }
        }.labelsHidden().help("Filter by \(title.lowercased())").accessibilityLabel(title)
            .onChange(of: value.wrappedValue) { _, _ in model.refresh() }
    }
    private var missions: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Text("Choose an outcome. Ship it. Keep the patch.").font(.title3)
                Text("Missions span all time and follow the project filter. Harness, profile, and period filters apply to session statistics.")
                    .font(.caption).foregroundStyle(.secondary)
                GroupBox("Brief a mission") {
                    VStack(alignment: .leading, spacing: 8) {
                        TextField("Outcome, e.g. Ship authentication", text: $missionTitle)
                        TextField("Project", text: $missionProject)
                        TextField("Optional conversation ID, worktree path, or ASL run ID", text: $missionReference)
                        Button("Create mission") {
                            model.saveMission(title: missionTitle, project: missionProject, status: "briefed",
                                              references: missionReference.isEmpty ? [] : [missionReference]) {
                                missionTitle = ""; missionReference = ""
                            }
                        }.disabled(missionTitle.trimmingCharacters(in: .whitespaces).isEmpty || missionProject.trimmingCharacters(in: .whitespaces).isEmpty)
                    }.padding(6)
                }
                if (model.report?.missions ?? []).isEmpty {
                    Text("Your first mission starts with one concrete outcome.").foregroundStyle(.secondary)
                }
                ForEach(model.report?.missions ?? []) { mission in
                    GroupBox {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Image(systemName: mission.shippedAt == nil ? "paperplane.circle" : "seal.fill")
                                    .font(.largeTitle).foregroundStyle(.tint)
                                VStack(alignment: .leading) {
                                    Text(mission.title).font(.headline)
                                    Text(mission.project + " · " + mission.statusLabel).foregroundStyle(.secondary)
                                }
                                Spacer()
                                if mission.shippedAt != nil { Label("Patch earned", systemImage: "checkmark.seal") }
                            }
                            ForEach(mission.references, id: \.self) { Text($0).font(.caption).textSelection(.enabled) }
                            HStack {
                                ForEach(["briefed", "in_progress", "review", "shipped"], id: \.self) { status in
                                    Button(missionStatusLabel(status)) {
                                        model.saveMission(id: mission.id, title: mission.title, project: mission.project,
                                                          status: status, references: mission.references)
                                    }.disabled(status == mission.status)
                                }
                            }
                            Text("Shipped is your confirmation that the outcome is complete. Earned patches remain if you reopen a mission.")
                                .font(.caption).foregroundStyle(.secondary)
                        }.padding(6)
                    }
                }
            }.padding(12)
        }.disabled(model.acting || model.report?.settings.enabled == false)
    }
    private func missionStatusLabel(_ status: String) -> String {
        switch status {
        case "in_progress": return "In progress"
        case "review": return "Ready for review"
        case "shipped": return "Mark shipped"
        default: return "Briefed"
        }
    }
    private var overview: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let report = model.report {
                    GroupBox("Project missions") {
                        HStack {
                            Text("\((report.missions ?? []).filter { $0.status != "shipped" }.count) active · \((report.missions ?? []).filter { $0.shippedAt != nil }.count) patches earned")
                            Spacer()
                            Button("Open missions") { tab = "missions" }
                        }.padding(6)
                    }
                    metrics(report.totals)
                    GroupBox {
                        VStack(alignment: .leading, spacing: 10) {
                            HStack {
                                Text("Activity calendar").font(.headline)
                                Spacer()
                                Picker("Measure", selection: $model.heatMetric) {
                                    ForEach(FlightHeatMetric.allCases) { Text($0.rawValue).tag($0) }
                                }.frame(width: 210)
                            }
                            FlightCalendar(days: report.days, metric: model.heatMetric, trackingSince: model.dateKey(report.trackingDate) ?? "9999-12-31") { day in
                                model.selectDay(day); tab = "sessions"
                            }
                            Text("Select a day to explore its sessions. Calendar uses \(report.timezone); weeks start Monday.")
                                .font(.caption).foregroundStyle(.secondary)
                        }.padding(6)
                    }
                    GroupBox("Captain’s log · this week") {
                        VStack(alignment: .leading, spacing: 8) {
                            ForEach(report.recap, id: \.self) { Text($0) }
                            Button("Explore this week") { model.selectedDay = nil; model.period = "week"; model.inspect("all"); tab = "sessions"; model.refresh() }
                        }.frame(maxWidth: .infinity, alignment: .leading).padding(6)
                    }
                    if let next = report.achievements.first(where: { $0.unlockedAt == nil }) {
                        GroupBox("Next milestone · all time") {
                            VStack(alignment: .leading, spacing: 6) {
                                HStack { Text(next.title).font(.headline); Spacer(); Text("\(next.progress) / \(next.target)").monospacedDigit() }
                                Text(next.description).foregroundStyle(.secondary)
                                ProgressView(value: Double(min(next.progress, next.target)), total: Double(next.target))
                                Button("View collection") { tab = "milestones" }
                            }.padding(6)
                        }
                    }
                    Text("All time: \(report.lifetime.launches) launches · \(report.lifetime.stateChanges) state changes · \(report.lifetime.turnsFinished) turns finished")
                        .font(.callout).foregroundStyle(.secondary)
                    ForEach(report.warnings, id: \.self) { Text($0).font(.caption).foregroundStyle(.secondary).textSelection(.enabled) }
                } else if !model.loading && model.error == nil {
                    WorkspaceEmpty(title: "Your flight history starts here", detail: "Launch a session to start collecting statistics and milestones.", symbol: "paperplane")
                }
            }.padding(12)
        }
    }
    private func metrics(_ m: FlightMetrics) -> some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 170), alignment: .leading)], alignment: .leading, spacing: 12) {
            metric("Launches", value: "\(m.launches)", detail: "\(m.fresh) fresh · \(m.resumes) resumed · \(m.forks) forked · \(m.restarts) restarted", kind: "all")
            metric("State changes", value: "\(m.stateChanges)", detail: "Effective icon or loop changes", kind: "all")
            metric("Turns finished", value: "\(m.turnsFinished)", detail: "Agent responses finished", kind: "stop")
            metric("Observed working time", value: flightDuration(m.workingSeconds), detail: "Summed across agents", kind: "all")
            metric("Waiting for you", value: flightDuration(m.waitingSeconds), detail: "\(m.questions) questions · \(m.permissions) permissions", kind: "all")
            metric("Conversations", value: "\(m.conversations)", detail: "Across \(m.projects) projects", kind: "all")
            metric("Questions", value: "\(m.questions)", detail: "Explore recorded questions", kind: "question")
            metric("Permissions", value: "\(m.permissions)", detail: "Explore permission requests", kind: "key")
            metric("ASL runs finished", value: "\(m.runsFinished)", detail: "Peak working sessions: \(m.peakWorking)", kind: "all")
        }
    }
    private func metric(_ label: String, value: String, detail: String, kind: String) -> some View {
        Button { model.inspect(kind); tab = "sessions" } label: {
            VStack(alignment: .leading, spacing: 5) {
                Text(label).font(.caption).foregroundStyle(.secondary)
                Text(value).font(.title2).monospacedDigit()
                Text(detail).font(.caption2).foregroundStyle(.secondary).lineLimit(2)
            }.frame(maxWidth: .infinity, minHeight: 78, alignment: .leading).padding(10)
                .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 8))
        }.buttonStyle(.plain).help("Explore \(label.lowercased())")
    }
    private var sessions: some View {
        VStack(alignment: .leading, spacing: 8) {
            Picker("Show", selection: $model.eventKind) {
                Text("All events").tag("all"); Text("Questions").tag("question"); Text("Permissions").tag("key"); Text("Turns finished").tag("stop"); Text("Errors").tag("error")
            }.frame(maxWidth: 300).padding(.horizontal, 10).onChange(of: model.eventKind) { _, _ in model.normalizeSelection() }
            if let day = model.selectedDay {
                HStack { Text("Activity on \(day)"); Button("Clear day filter") { model.selectedDay = nil } }.padding(.horizontal, 10)
            }
            HSplitView {
                List(selection: $model.selection) {
                    ForEach(model.visibleSessions) { session in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(session.title).font(.headline)
                            Text([session.harness, session.profile, session.operation].filter { !$0.isEmpty }.joined(separator: " · ")).font(.caption)
                            Text("\(session.metrics.stateChanges) changes · \(session.metrics.turnsFinished) turns").font(.caption).foregroundStyle(.secondary)
                        }.padding(.vertical, 4).tag(session.key)
                    }
                }.frame(minWidth: 210, idealWidth: 260)
                    .overlay { if model.visibleSessions.isEmpty && !model.loading { WorkspaceEmpty(title: "No sessions", detail: "Try another period or clear the filters.", symbol: "paperplane") } }
                ScrollView {
                    if let session = model.selected { scorecard(session).padding(14) }
                    else { WorkspaceEmpty(title: "Session scorecard", detail: "Select a recorded session to explore its timeline and events.", symbol: "chart.bar") }
                }.frame(minWidth: 390)
            }
        }.padding(.top, 10)
    }
    private func scorecard(_ session: FlightSession) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(session.title).font(.title2)
            Text([session.harness, session.profile, session.operation.isEmpty ? "Launch type unknown" : session.operation.capitalized].filter { !$0.isEmpty }.joined(separator: " · ")).foregroundStyle(.secondary)
            Text(session.key).font(.caption).textSelection(.enabled)
            Text("First recorded " + (WorkspaceActivityEntry.date(session.firstAt)?.formatted() ?? session.firstAt) + " · Last recorded " + (WorkspaceActivityEntry.date(session.lastAt)?.formatted() ?? session.lastAt))
                .font(.caption).foregroundStyle(.secondary)
            if session.partial { Text("Partial history: some launch, identity, or observation data is unavailable.").font(.caption).foregroundStyle(.secondary) }
            HStack {
                Button("Open Conversation") { model.openConversation() }.disabled(session.conversation == nil)
                Button("Open Retained Activity") { model.onActivity?(session, model.eventKind) }
            }
            HStack(spacing: 20) {
                Text("\(session.metrics.stateChanges) state changes")
                Text("\(session.metrics.turnsFinished) turns finished")
            }.font(.headline)
            Text("Observed work: \(flightDuration(session.metrics.workingSeconds)) · Waiting for you: \(flightDuration(session.metrics.waitingSeconds))")
            HStack {
                Button("\(session.metrics.questions) questions") { model.inspect("question") }
                Button("\(session.metrics.permissions) permissions") { model.inspect("key") }
                Button("\(session.metrics.errors) errors") { model.inspect("error") }
            }
            Divider()
            Text("State timeline").font(.headline)
            FlightTimeline(segments: session.timeline)
            Text("Gray intervals are unknown. Time after the last observation is unknown. Working time includes compacting context and subagent activity.")
                .font(.caption).foregroundStyle(.secondary)
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 140), alignment: .leading)], alignment: .leading) {
                ForEach(session.stateCounts.keys.sorted(), id: \.self) { icon in
                    Text("\(FlightEvent.stateName(icon)): \(session.stateCounts[icon] ?? 0)").font(.caption)
                }
            }
            Divider()
            LazyVStack(alignment: .leading, spacing: 10) {
                ForEach(model.visibleEvents.reversed()) { event in
                    VStack(alignment: .leading, spacing: 4) {
                        Text(event.label).font(.headline)
                        Text(event.date?.formatted() ?? event.at).font(.caption).foregroundStyle(.secondary)
                        if event.looping != event.beforeLooping, event.kind == "state" {
                            Text("Loop \(event.beforeLooping == true ? "on" : "off") → \(event.looping == true ? "on" : "off")").font(.caption)
                        }
                        if event.conversation != nil { Button("Conversation at this event") { model.openConversation(at: event.at) }.font(.caption) }
                    }.frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            if model.visibleEvents.isEmpty { Text("No matching events in this session and period.").foregroundStyle(.secondary) }
        }.frame(maxWidth: .infinity, alignment: .leading)
    }
    private var milestones: some View {
        ScrollView {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 270))], spacing: 14) {
                ForEach(model.report?.achievements ?? []) { achievement in
                    VStack(alignment: .leading, spacing: 10) {
                        Label(achievement.title, systemImage: achievement.unlockedAt == nil ? "circle.dashed" : "medal.fill").font(.headline)
                        Text(achievement.description).foregroundStyle(.secondary)
                        if let at = achievement.unlockedAt {
                            Text("Unlocked " + (WorkspaceActivityEntry.date(at)?.formatted(date: .abbreviated, time: .omitted) ?? at)).font(.caption)
                            if achievement.id.hasPrefix("mission:") {
                                Button("Revisit mission") { model.project = ""; model.harness = ""; model.profile = ""; model.refresh(); tab = "missions" }
                            } else {
                                Button("Revisit session") { model.revealAchievement(achievement); tab = "sessions" }.disabled(achievement.sessionKey.isEmpty)
                            }
                        } else {
                            ProgressView(value: Double(min(achievement.progress, achievement.target)), total: Double(achievement.target))
                            Text("\(achievement.progress) / \(achievement.target)").font(.caption).monospacedDigit()
                        }
                    }.padding(14).frame(maxWidth: .infinity, minHeight: 140, alignment: .topLeading)
                        .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 10))
                }
            }.padding(14)
            Text("Milestones use all recorded history. There are no daily streak requirements.").font(.caption).foregroundStyle(.secondary)
        }
    }
    private func export() {
        let panel = NSSavePanel(); panel.nameFieldStringValue = "flight-log.json"; panel.allowedContentTypes = [.json]
        guard panel.runModal() == .OK, let url = panel.url else { return }
        model.export(to: url)
    }
}

private struct FlightCalendar: View {
    let days: [FlightDay]
    let metric: FlightHeatMetric
    let trackingSince: String
    let select: (String) -> Void
    private var peak: Double { max(1, days.map { metric.value($0.metrics) }.max() ?? 1) }
    var body: some View {
        ScrollView(.horizontal) {
            LazyHGrid(rows: Array(repeating: GridItem(.fixed(13), spacing: 3), count: 7), spacing: 3) {
                // Align the first column to Monday using calendar arithmetic,
                // without treating missing days as observed zero activity.
                ForEach(0..<leading, id: \.self) { _ in Color.clear.frame(width: 13, height: 13).accessibilityHidden(true) }
                ForEach(days) { day in
                    Button { select(day.date) } label: {
                        RoundedRectangle(cornerRadius: 2).fill(metric.value(day.metrics) == 0 ? Color.secondary.opacity(0.13) : Color.accentColor.opacity(0.25 + 0.75 * sqrt(metric.value(day.metrics) / peak)))
                            .frame(width: 13, height: 13)
                            .overlay { if day.date < String(trackingSince.prefix(10)) && metric.value(day.metrics) == 0 { RoundedRectangle(cornerRadius: 2).strokeBorder(.secondary.opacity(0.25), style: StrokeStyle(lineWidth: 1, dash: [2])) } }
                    }.buttonStyle(.plain).help("\(day.date) · \(metric.label(day.metrics))\(day.date < String(trackingSince.prefix(10)) ? " · before tracking; history may be partial" : "")")
                        .accessibilityLabel("\(day.date), \(metric.label(day.metrics))")
                }
            }.padding(.vertical, 3)
        }
    }
    private var leading: Int {
        guard let first = days.first else { return 0 }
        let formatter = DateFormatter(); formatter.dateFormat = "yyyy-MM-dd"
        guard let date = formatter.date(from: first.date) else { return 0 }
        return (Calendar.current.component(.weekday, from: date) + 5) % 7
    }
}

private struct FlightTimeline: View {
    let segments: [FlightSegment]
    var body: some View {
        GeometryReader { geometry in
            let total = max(1, segments.reduce(0) { $0 + $1.seconds })
            HStack(spacing: 0) {
                ForEach(Array(segments.enumerated()), id: \.offset) { _, segment in
                    Rectangle().fill(color(segment)).frame(width: geometry.size.width * segment.seconds / total)
                        .help("\(segment.observed ? FlightEvent.stateName(segment.icon) : "Unknown") · \(flightDuration(segment.seconds))")
                }
            }.clipShape(RoundedRectangle(cornerRadius: 4))
        }.frame(height: 24).accessibilityElement(children: .ignore)
            .accessibilityLabel(segments.isEmpty ? "No bracketed observations yet" : segments.map { "\($0.observed ? FlightEvent.stateName($0.icon) : "Unknown"), \(flightDuration($0.seconds))" }.joined(separator: "; "))
            .overlay { if segments.isEmpty { Text("No bracketed observations yet").font(.caption).foregroundStyle(.secondary) } }
    }
    private func color(_ segment: FlightSegment) -> Color {
        guard segment.observed else { return .gray.opacity(0.25) }
        switch segment.icon {
        case "bolt", "subagent", "compact": return .blue
        case "key", "question": return .orange
        case "error", "warning": return .red
        case "stop": return .green
        default: return .secondary.opacity(0.5)
        }
    }
}
