import AppKit
import SwiftUI

struct WorkspaceActivityView: View {
    @ObservedObject var preferences: WorkspaceReadingPreferences = .shared
    @ObservedObject var model: WorkspaceActivityModel
    var title = "Since you last looked"
    var grouped = false
    var outcomes: [String: WorkspaceRecapModel] = [:]
    var sessionRows: [WorkspaceRow] = []
    var onRefresh: (() -> Void)?
    var onReview: ((AttentionCycler.Key) -> Void)?
    private enum Focus: Hashable { case list, details }
    @FocusState private var focus: Focus?
    @State private var expanded: Set<String> = []
    @State private var expansionGeneration = 0
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Button(action: toggleEvents) { Image(systemName: "sidebar.left") }
                    .help("Show or hide events (⌘⌥S)")
                    .accessibilityLabel(model.layout.collapsed ? "Show events" : "Hide events")
                Text(title).font(.title2)
                Spacer(minLength: 0)
                WorkspaceReviewStatus(reads: [
                    .init(title: "Activity", loading: model.loading, retained: model.report != nil, refreshedAt: model.reportRefreshedAt,
                          feedback: activityFeedback, retry: { refresh() }),
                    .init(title: "Supporting output", loading: model.reading, retained: model.transcript != nil, refreshedAt: model.transcriptRefreshedAt,
                          feedback: model.transcriptError.map { WorkspaceFeedback(target: "Supporting output", message: $0) }, retry: { model.loadTranscript() })
                ])
                WorkspaceReadingMenu(preferences: preferences)
            }
            HStack {
                Toggle("Unread only", isOn: $model.unreadOnly).onChange(of: model.unreadOnly) { _, _ in model.cancelReveal(); model.filterChanged() }
                Picker("Look back", selection: $model.days) {
                    Text("1 day").tag(1); Text("7 days").tag(7); Text("30 days").tag(30); Text("90 days").tag(90)
                }.frame(width: 180).onChange(of: model.days) { _, _ in model.cancelReveal(); refresh() }
                Spacer()
                Button { refresh() } label: { Image(systemName: "arrow.clockwise") }
                    .help("Refresh activity (⌘R)").accessibilityLabel("Refresh activity").disabled(model.loading)
                Menu("Mark read") {
                    Button("Mark selected read") { model.markSelectedRead() }.disabled(!model.canMarkSelectedRead)
                    Button("Mark all \(model.visible.count) results read") { model.markVisibleRead() }
                        .disabled(model.unreadResultCount == 0 || model.loading || model.error != nil)
                }.fixedSize()
                Button("Undo") { model.undoMarkRead() }.disabled(!model.canUndoRead).help("Undo the last activity read acknowledgement")
            }
            HStack {
                Text(model.report.map { "\(model.visible.count) shown · \($0.entries.count) loaded" } ?? "Recorded activity")
                    .font(.caption).foregroundStyle(.secondary)
                Spacer()
                Menu("About this activity") {
                    Text("Opening this view does not mark events read.")
                    Text(model.readState.lastMarkedAt.map { "Last marked read: " + $0.formatted() } ?? "No events marked read yet")
                    Text("Retained history starts " + (model.report.flatMap { WorkspaceActivityEntry.date($0.earliestAvailable) }?.formatted() ?? "at an unknown time"))
                }.menuStyle(.borderlessButton).fixedSize().font(.caption)
            }.frame(height: 20)
            WorkspaceReadFailure(title: "Activity", error: model.error, retained: model.report != nil,
                                 loading: model.loading, retry: { refresh() })
            if model.scopeLoading { ProgressView("Finding project activity…").controlSize(.small) }
            WorkspaceReadFailure(title: "Project activity", error: model.scopeError, retained: !model.visible.isEmpty,
                                 loading: model.scopeLoading, retry: { model.setProject(model.project, rows: sessionRows) })
            if let session = model.flightSession {
                HStack {
                    Text("Session: \(session.title) · \(model.flightKind == "all" ? "all events" : WorkspaceInspector.word(for: model.flightKind))").font(.caption)
                    Spacer()
                    Button("Clear session filter") { model.clearFlightFilter() }
                }
                Text("Only saved events are shown. Older events may no longer be available.").font(.caption).foregroundStyle(.secondary)
            }
            WorkspaceReviewSplit(layout: $model.layout, title: "Events", minimum: 220, detailMinimum: 360) {
                List(selection: Binding(get: { model.selection }, set: { model.select($0, origin: .explicit) })) {
                    if grouped {
                        ForEach(model.groups) { group in
                            Section {
                                if expanded.contains(group.id) {
                                    ForEach(group.entries) { entry in eventRow(entry) }
                                }
                            } header: {
                                VStack(alignment: .leading, spacing: 4) {
                                    HStack {
                                        Button {
                                            if !expanded.insert(group.id).inserted { expanded.remove(group.id) }
                                        } label: {
                                            Image(systemName: expanded.contains(group.id) ? "chevron.down" : "chevron.right")
                                        }
                                        .buttonStyle(.borderless)
                                        .accessibilityLabel(expanded.contains(group.id) ? "Hide events" : "Show events")
                                        .accessibilityIdentifier("activity-disclosure-" + group.id)
                                        Text(groupTitle(group)).font(.headline)
                                        Spacer(minLength: 0)
                                        let unread = group.entries.filter { model.readState.read[$0.id] == nil }.count
                                        if unread > 0 { Text("\(unread) unread").font(.caption).foregroundStyle(.secondary) }
                                    }
                                    Text(group.summary(now: Date())).font(.caption).foregroundStyle(.secondary)
                                    if let recap = outcomes[group.id] { WorkspaceReturnOutcome(model: recap) }
                                }
                            }
                        }
                    } else {
                        ForEach(model.visible) { entry in eventRow(entry) }
                    }
                }.focused($focus, equals: .list)
                    .overlay { if model.visible.isEmpty && !model.loading && !model.scopeLoading && model.error == nil && model.scopeError == nil {
                        WorkspaceEmpty(title: model.unreadOnly ? "All caught up" : "No activity found",
                                       detail: "No \(model.unreadOnly ? "unread" : "recorded") events in this period. Try a longer look-back period.")
                    } }
            } detail: {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        if let entry = model.selected {
                            Text(entry.title).font(.title2)
                            Text("\(relativeAge(entry)) · \(entry.date?.formatted() ?? entry.at)").foregroundStyle(.secondary)
                            if !entry.transition.isEmpty {
                                Text(entry.transition).font(.system(size: preferences.effectiveProseSize)).textSelection(.enabled)
                            }
                            HStack {
                                if entry.conversation != nil || !entry.runtimeSessionID.isEmpty {
                                    Button("Focus live session") { model.onFocus?(entry) }.help("Focus live session (⌘Return)")
                                }
                                if entry.conversation != nil { Button("Open conversation") { model.openConversation() }.help("Open conversation (⌘⇧Return)") }
                            }
                            if let recap = outcomes[entry.groupID] { WorkspaceReturnOutcome(model: recap) }
                            if let row = sessionRows.first(where: { !$0.key.runtimeSessionID.isEmpty && $0.key.runtimeSessionID == entry.runtimeSessionID }), row.iconState == "stop", let onReview {
                                Button("Review changes") { onReview(row.key) }
                            }
                            Divider()
                            Text("Supporting output").font(.headline)
                            WorkspaceReadFailure(title: "Supporting output", error: model.transcriptError, retained: model.transcript != nil,
                                                 loading: model.reading, retry: { model.loadTranscript() })
                            if let excerpt = model.excerpt {
                                Text("Last recorded assistant message at or before this event · \(excerpt.timestamp ?? "")").font(.caption).foregroundStyle(.secondary)
                                WorkspaceTranscriptText(text: excerpt.text.reveal(), terms: [], preferences: preferences)
                            } else if !model.reading && model.transcriptError == nil {
                                Text(entry.conversation == nil ? "No conversation is linked to this event." : "No timestamped assistant output at or before this event is available in the loaded messages.").foregroundStyle(.secondary)
                            }
                            if model.canLoadEarlier { Button("Load earlier messages") { model.loadEarlier() } }
                            Divider()
                            DisclosureGroup("Diagnostics") {
                                VStack(alignment: .leading, spacing: 6) {
                                    Text(entry.detail).textSelection(.enabled)
                                    Text("Recorded event: \(entry.event)").textSelection(.enabled)
                                    if !entry.runID.isEmpty { Text("Run: \(entry.runID)").textSelection(.enabled) }
                                    if !entry.traceID.isEmpty { Text("Trace: \(entry.traceID)").textSelection(.enabled) }
                                }.font(.caption).frame(maxWidth: .infinity, alignment: .leading)
                            }.id(entry.id)
                                .accessibilityIdentifier("activity-diagnostics")
                        }
                    }.padding(12).frame(maxWidth: .infinity, alignment: .leading)
                }.focusable().focused($focus, equals: .details)
                    .overlay {
                        if model.selected == nil && (!model.visible.isEmpty || model.layout.collapsed) {
                            WorkspaceEmpty(title: "Event details", detail: "Select an event to inspect its recorded details and supporting output.",
                                           symbol: "clock.arrow.circlepath")
                        }
                    }
                    .frame(minWidth: 360, maxWidth: .infinity)
            }
        }.padding(12)
            .onAppear { focus = model.layout.collapsed ? .details : .list; revealRequestedEvent() }
            .onChange(of: model.explicitReveal) { _, _ in revealRequestedEvent() }
            .onChange(of: model.visible.map(\.id)) { _, _ in revealRequestedEvent() }
            .onChange(of: model.presentationGeneration) { _, _ in revealRequestedEvent() }
            .background(WorkspaceReviewShortcuts(handle: handleShortcut).frame(width: 0, height: 0))
    }
    private func refresh() { if let onRefresh { onRefresh() } else { model.refresh() } }

    private func eventRow(_ entry: WorkspaceActivityEntry) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                if model.readState.read[entry.id] == nil {
                    Image(systemName: "circle.fill").font(.system(size: 6)).foregroundStyle(WorkspaceReviewColors.unread)
                        .help("Unread").accessibilityLabel("Unread")
                }
                Text(entry.title).font(.headline)
            }
            Text(relativeAge(entry)).font(.caption).foregroundStyle(.secondary)
            if !grouped { Text([entry.harness, entry.profile].filter { !$0.isEmpty }.joined(separator: " · ")).font(.caption) }
            if !entry.transition.isEmpty { Text(entry.transition).font(.caption).lineLimit(2) }
        }.padding(.vertical, 3).tag(entry.id)
            .help(entry.date?.formatted() ?? entry.at)
            .accessibilityIdentifier("activity-event-" + entry.id)
    }
    private func relativeAge(_ entry: WorkspaceActivityEntry) -> String {
        entry.date.map { AgeFormatter.string(since: $0, now: Date(), style: .sentence) } ?? entry.at
    }
    private func revealRequestedEvent() {
        // A close and a new link may arrive in the same SwiftUI update.
        // Reset once, before consuming that presentation's reveal.
        if expansionGeneration != model.presentationGeneration {
            expanded.removeAll()
            expansionGeneration = model.presentationGeneration
        }
        guard let request = model.explicitReveal,
              let entry = model.visible.first(where: { $0.id == request.entryID }) else { return }
        if grouped { expanded.insert(entry.groupID) }
        model.consumeReveal(request)
    }
    private func groupTitle(_ group: WorkspaceActivityModel.SessionGroup) -> String {
        guard let entry = group.entries.first else { return "Session" }
        return sessionRows.first { !$0.key.runtimeSessionID.isEmpty && $0.key.runtimeSessionID == entry.runtimeSessionID }?.displayName
            ?? entry.conversation?.encoded ?? (entry.runtimeSessionID.isEmpty ? entry.title : entry.runtimeSessionID)
    }
    private var activityFeedback: WorkspaceFeedback? {
        if let error = model.error { return WorkspaceFeedback(target: "Activity", message: error,
            nextStep: "Previous events remain visible. Check Settings if amx is unavailable, then refresh.") }
        if let error = model.readError { return WorkspaceFeedback(target: "Read markers", message: error,
            nextStep: "Check that the activity read-marker file is writable, then retry the read acknowledgement or Undo.", recovery: nil) }
        guard let report = model.report else { return nil }
        var warnings: [String] = []
        if report.truncated { warnings.append("Showing the newest 1,000 of \(report.matched) matching events. Older events remain unread.") }
        if report.corruptLines > 0 { warnings.append("\(report.corruptLines) unreadable journal records were skipped; history may be incomplete.") }
        return warnings.isEmpty ? nil : WorkspaceFeedback(target: "Activity history", message: warnings.joined(separator: "\n"), kind: .warning,
            nextStep: "Choose a shorter look-back period to narrow the result, or refresh to read the journal again.")
    }
    private func handleShortcut(_ key: String, _ modifiers: NSEvent.ModifierFlags) -> Bool {
        if modifiers == .command {
            switch key {
            case "r": if !model.loading { refresh() }
            case "\r": if let entry = model.selected, entry.conversation != nil || !entry.runtimeSessionID.isEmpty { model.onFocus?(entry) }
            default: return false
            }
        } else if modifiers == [.command, .shift] {
            switch key {
            case "\r": model.openConversation()
            default: return false
            }
        } else if modifiers == [.command, .option], key == "f" { model.layout.collapsed = false; focus = .list }
        else if modifiers == [.command, .option], key == "s" { toggleEvents() }
        else { return false }
        return true
    }
    private func toggleEvents() {
        model.layout.collapsed.toggle()
        focus = model.layout.collapsed ? .details : .list
    }
}
