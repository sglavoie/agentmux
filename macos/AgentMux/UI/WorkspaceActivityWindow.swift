import AppKit
import SwiftUI

@MainActor
final class WorkspaceActivityWindow: ReviewWindowController {
    let model: WorkspaceActivityModel
    init(model: WorkspaceActivityModel) {
        self.model = model
        super.init(title: "Since Last Looked", autosaveName: "WorkspaceActivity", size: NSSize(width: 1000, height: 650))
    }
    func show() { present() }
    override func makeContentView() -> NSView { Self.host(WorkspaceActivityView(model: model)) }
    override func willPresent() { model.refresh() }
    override func didClose() { model.close() }
}

struct WorkspaceActivityView: View {
    @ObservedObject var preferences: WorkspaceReadingPreferences = .shared
    @ObservedObject var model: WorkspaceActivityModel
    private enum Focus: Hashable { case list, details }
    @FocusState private var focus: Focus?
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Button(action: toggleEvents) { Image(systemName: "sidebar.left") }
                    .help("Show or hide events (⌘⌥S)")
                    .accessibilityLabel(model.layout.collapsed ? "Show events" : "Hide events")
                Text("Since Last Looked").font(.title2).fixedSize()
                Spacer(minLength: 0)
                WorkspaceReviewStatus(reads: [
                    .init(title: "Activity", loading: model.loading, retained: model.report != nil, refreshedAt: model.reportRefreshedAt,
                          feedback: activityFeedback, retry: { model.refresh() }),
                    .init(title: "Supporting output", loading: model.reading, retained: model.transcript != nil, refreshedAt: model.transcriptRefreshedAt,
                          feedback: model.transcriptError.map { WorkspaceFeedback(target: "Supporting output", message: $0) }, retry: { model.loadTranscript() })
                ])
                WorkspaceReadingMenu(preferences: preferences)
            }
            HStack {
                Toggle("Unread only", isOn: $model.unreadOnly).onChange(of: model.unreadOnly) { _, _ in model.filterChanged() }
                Picker("Look back", selection: $model.days) {
                    Text("1 day").tag(1); Text("7 days").tag(7); Text("30 days").tag(30); Text("90 days").tag(90)
                }.frame(width: 180).onChange(of: model.days) { _, _ in model.refresh() }
                Spacer()
                Button { model.refresh() } label: { Image(systemName: "arrow.clockwise") }
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
                                 loading: model.loading, retry: { model.refresh() })
            WorkspaceReviewSplit(layout: $model.layout, title: "Events", minimum: 220, detailMinimum: 360) {
                List(selection: Binding(get: { model.selection }, set: { model.select($0) })) {
                    ForEach(model.visible) { entry in
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                if model.readState.read[entry.id] == nil {
                                    Image(systemName: "circle.fill").font(.system(size: 6)).foregroundStyle(WorkspaceReviewColors.unread)
                                        .accessibilityLabel("Unread")
                                }
                                Text(entry.title).font(.headline)
                            }
                            Text(entry.date?.formatted() ?? entry.at).font(.caption).foregroundStyle(.secondary)
                            Text([entry.harness, entry.profile].filter { !$0.isEmpty }.joined(separator: " · ")).font(.caption)
                            Text(entry.detail).font(.caption).lineLimit(2)
                        }.padding(.vertical, 3).tag(entry.id)
                    }
                }.focused($focus, equals: .list)
                    .overlay { if model.visible.isEmpty && !model.loading && model.error == nil {
                        WorkspaceEmpty(title: model.unreadOnly ? "All caught up" : "No activity found",
                                       detail: "No \(model.unreadOnly ? "unread" : "recorded") events in this period. Try a longer look-back period.")
                    } }
            } detail: {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        if let entry = model.selected {
                            Text(entry.title).font(.title2)
                            Text(entry.date?.formatted() ?? entry.at).foregroundStyle(.secondary)
                            Text(entry.detail).font(.system(size: preferences.effectiveProseSize)).textSelection(.enabled)
                            HStack {
                                if entry.conversation != nil || !entry.runtimeSessionID.isEmpty {
                                    Button("Focus live session") { model.onFocus?(entry) }.help("Focus live session (⌘Return)")
                                }
                                if entry.conversation != nil { Button("Open conversation") { model.openConversation() }.help("Open conversation (⌘⇧Return)") }
                            }
                            Divider()
                            Text("Supporting output").font(.headline)
                            WorkspaceReadFailure(title: "Supporting output", error: model.transcriptError, retained: model.transcript != nil,
                                                 loading: model.reading, retry: { model.loadTranscript() })
                            if let excerpt = model.excerpt {
                                Text("Last recorded assistant message at or before this event · \(excerpt.timestamp ?? "")").font(.caption).foregroundStyle(.secondary)
                                WorkspaceTranscriptText(text: excerpt.text.reveal(), terms: [], preferences: preferences)
                            } else if !model.reading && model.transcriptError == nil {
                                Text(entry.conversation == nil ? "This event has no recorded conversation identity." : "No timestamped assistant output at or before this event is available in the loaded messages.").foregroundStyle(.secondary)
                            }
                            if model.canLoadEarlier { Button("Load earlier messages") { model.loadEarlier() } }
                            Divider()
                            Text("Recorded event: \(entry.event)").font(.caption).textSelection(.enabled)
                            if !entry.runID.isEmpty { Text("Run: \(entry.runID)").font(.caption).textSelection(.enabled) }
                            if !entry.traceID.isEmpty { Text("Trace: \(entry.traceID)").font(.caption).textSelection(.enabled) }
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
            .onAppear { focus = model.layout.collapsed ? .details : .list }
            .background(WorkspaceReviewShortcuts(handle: handleShortcut).frame(width: 0, height: 0))
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
            case "r": if !model.loading { model.refresh() }
            case "\r": if let entry = model.selected, entry.conversation != nil || !entry.runtimeSessionID.isEmpty { model.onFocus?(entry) }
            default: return false
            }
        } else if modifiers == [.command, .shift] {
            switch key {
            case "f": model.layout.collapsed = false; focus = .list
            case "\r": model.openConversation()
            default: return false
            }
        } else if modifiers == [.command, .option], key == "s" { toggleEvents() }
        else { return false }
        return true
    }
    private func toggleEvents() {
        model.layout.collapsed.toggle()
        focus = model.layout.collapsed ? .details : .list
    }
}
