import AppKit
import SwiftUI

@MainActor
final class WorkspaceHistoryWindow: ReviewWindowController {
    let model: WorkspaceHistoryModel
    private var opening: (project: String, conversation: ConversationIdentity?, eventTime: String?)?

    init(model: WorkspaceHistoryModel) {
        self.model = model
        super.init(title: "Conversation History", autosaveName: "WorkspaceHistory", size: NSSize(width: 1000, height: 680))
        model.revealFeedback = { [weak self] in self?.reveal() }
    }

    func show(project: String, conversation: ConversationIdentity?, eventTime: String? = nil) {
        opening = (project, conversation, eventTime)
        present()
    }

    override func makeContentView() -> NSView { Self.host(WorkspaceHistoryView(model: model)) }
    override func willPresent() {
        guard let opening else { return }
        self.opening = nil
        model.open(project: opening.project, conversation: opening.conversation, eventTime: opening.eventTime)
    }
    override func didClose() { model.close() }
}

struct WorkspaceHistoryView: View {
    @ObservedObject var preferences: WorkspaceReadingPreferences = .shared
    @ObservedObject var model: WorkspaceHistoryModel
    @State private var visibleMessage: Int?
    private enum Focus: Hashable { case search, results, transcript }
    @FocusState private var focus: Focus?
    @State private var dragStart: Double?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let note = model.searchEntryNote {
                Text(note).font(.caption).foregroundStyle(.secondary).lineLimit(2).help(note)
            }
            HStack {
                Button(action: toggleResults) { Image(systemName: "sidebar.left") }
                    .help("Show or hide conversation results (⌘⌥S)")
                    .accessibilityLabel(model.layout.collapsed ? "Show conversation results" : "Hide conversation results")
                TextField("Search full conversations", text: $model.query)
                    .focused($focus, equals: .search)
                    .onKeyPress(.downArrow) { model.layout.collapsed = false; focus = .results; return .handled }
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { model.search() }
                    .onChange(of: model.query) { _, _ in model.scheduleSearch() }
                Button("Search") { model.search() }
                    .help("Search conversations. ⌘F focuses the search field.")
                WorkspaceReviewStatus(reads: [
                    .init(title: "Search", loading: model.loading || model.loadingMore, retained: !model.entries.isEmpty, refreshedAt: model.resultsRefreshedAt,
                          feedback: model.error.map { WorkspaceFeedback(target: "Conversation search", message: $0) }, retry: { model.search() }),
                    .init(title: "Transcript", loading: model.reading, retained: model.transcript != nil, refreshedAt: model.transcriptRefreshedAt,
                          feedback: model.transcriptError.map { WorkspaceFeedback(target: "Transcript", message: $0) }, retry: { model.refreshTranscript() })
                ])
                WorkspaceStatus(loading: model.acting, title: "Opening conversation…", feedback: model.actionFeedback,
                                recover: { model.recoverActionFeedback() }, compactFeedback: true).frame(width: 24)
                WorkspaceFeedbackHistoryButton(history: model.feedbackHistory)
                WorkspaceReadingMenu(preferences: preferences)
            }
            HStack {
                Picker("Search in", selection: $model.scope) {
                    ForEach(WorkspaceHistoryScope.allCases, id: \.self) { scope in
                        Text(scope.rawValue).tag(scope)
                            .disabled(scope == .project && model.project.isEmpty || scope == .conversation && model.currentConversation == nil)
                    }
                }.frame(maxWidth: 270)
                    .onChange(of: model.scope) { _, _ in model.search() }
                    .help(model.scope == .project ? model.project : model.scope == .conversation ? (model.currentConversation?.encoded ?? "") : "All recorded conversations")
                Toggle("Include automated sessions", isOn: $model.includeAutomated)
                    .onChange(of: model.includeAutomated) { _, _ in model.search() }
                Spacer()

                Text("\(model.entries.count) of \(model.totalResults) \(model.totalResults == 1 ? "conversation" : "conversations")").foregroundStyle(.secondary)
            }
            GeometryReader { geometry in
            VStack(alignment: .leading, spacing: 6) {
            WorkspaceReadFailure(title: "Search results", error: model.error, retained: !model.entries.isEmpty,
                                 loading: model.loading, retry: { model.search() })
            HStack(spacing: 0) {
                if !model.layout.collapsed {
                VStack(spacing: 6) {
                List(selection: Binding(get: { model.selection }, set: { model.select($0) })) {
                    ForEach(model.entries) { entry in
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                Text(entry.conversation.displayName).font(.headline)
                                if model.liveConversations.contains(entry.id) { Text("Live").font(.caption).foregroundStyle(WorkspaceReviewColors.live) }
                            }
                            HStack {
                                Text("\(entry.conversation.harness) · \(entry.conversation.profile)").lineLimit(1)
                                Spacer(minLength: 4)
                                if let lastAt = entry.conversation.lastAt {
                                    TimelineView(.periodic(from: .now, by: 30)) { _ in
                                        Text(lastAt.formatted(.relative(presentation: .named, unitsStyle: .abbreviated)))
                                            .help(lastAt.formatted())
                                    }
                                }
                            }.font(.caption).foregroundStyle(.secondary)
                            if model.scope == .all && !entry.conversation.cwd.isEmpty {
                                WorkspacePath(path: entry.conversation.cwd).font(.caption).foregroundStyle(.secondary)
                            }
                            Text(WorkspaceHighlight.text(entry.preview.reveal(), terms: model.resultTerms)).font(.caption).lineLimit(3)
                            if let matches = entry.matches { Text("\(matches) \(matches == 1 ? "match" : "matches")").font(.caption).foregroundStyle(.secondary) }
                        }.padding(.vertical, 3).tag(entry.id)
                    }
                }.focused($focus, equals: .results)
                    .frame(maxWidth: .infinity)
                    .overlay {
                        if model.entries.isEmpty && !model.loading && model.error == nil {
                            WorkspaceEmpty(title: "No conversations found", detail: emptyResultsDetail, symbol: "magnifyingglass") {
                                VStack(spacing: 6) {
                                    if !model.resultsQuery.isEmpty { Button("Clear Search") { model.query = "" } }
                                    if model.scope != .all { Button("Search All History") { model.scope = .all } }
                                    if !model.includeAutomated { Button("Include Automated Sessions") { model.includeAutomated = true } }
                                }.padding(.top, 2)
                            }
                        }
                    }
                    if model.hasMoreResults {
                        Button(model.loadingMore ? "Loading…" : "Load more conversations") { model.loadMoreResults() }
                            .disabled(model.loading || model.loadingMore)
                    }
                    if !model.entries.isEmpty && model.hiddenAutomated > 0 {
                        Button("Include \(model.hiddenAutomated) automated matches") { model.includeAutomated = true }.font(.caption)
                    }
                }.frame(width: model.layout.width(in: geometry.size.width))
                    .accessibilityIdentifier("history-results")
                Rectangle().fill(Color.secondary.opacity(0.25)).frame(width: 1).frame(width: 6)
                    .background(WorkspaceHistoryResizeCursor())
                    .contentShape(Rectangle())
                    .gesture(DragGesture().onChanged { value in
                        if dragStart == nil { dragStart = model.layout.width(in: geometry.size.width) }
                        model.layout.resultsWidth = min(max(200, (dragStart ?? 200) + value.translation.width), max(200, geometry.size.width - 380))
                    }.onEnded { _ in dragStart = nil })
                    .accessibilityLabel("Conversation results width")
                    .accessibilityValue("\(Int(model.layout.width(in: geometry.size.width))) points")
                    .accessibilityAdjustableAction { direction in
                        model.layout.resultsWidth = model.layout.width(in: geometry.size.width) + (direction == .increment ? 20 : -20)
                    }
                }
                VStack(alignment: .leading, spacing: 4) {
                    if let selected = model.selected {
                        HStack {
                            Text(selected.conversation.displayName).font(.headline).lineLimit(1).help(selected.conversation.displayName + "\n" + selected.conversation.cwd)
                                .frame(maxWidth: .infinity, alignment: .leading)
                            Button(model.liveConversations.contains(selected.id) ? "Focus Live Session" : "Resume") { model.act(fork: false) }
                                .buttonStyle(.borderedProminent).fixedSize()
                                .help("Resume or focus the selected session (⌘Return)").disabled(!model.canAct)
                            Button("Fork") { model.act(fork: true) }.fixedSize().help("Fork the selected conversation (⌘⇧Return)").disabled(!model.canAct)
                            Button("Handoff / More…") { model.openAdvancedActions() }
                                .help("Open terminal conversation actions with this conversation and search. ⌘Y returns here.")
                                .disabled(!model.canAct || model.onAdvanced == nil)
                            Button { model.refreshTranscript() } label: { Image(systemName: "arrow.clockwise") }
                                .help("Refresh conversation without moving your place (⌘R)")
                                .accessibilityLabel("Refresh conversation").disabled(!model.canRefreshTranscript)
                        }
                        if let notice = model.locationNotice {
                            Text(notice).font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        }
                        WorkspaceReadFailure(title: "Transcript", error: model.transcriptError, retained: model.transcript != nil,
                                             loading: model.reading, retry: { model.refreshTranscript() })
                        if let transcript = model.transcript {
                            HStack(spacing: 4) {
                                Menu {
                                    Text(transcript.total == 0 ? "No messages" : "Messages \(transcript.start + 1)–\(transcript.end) of \(transcript.total)")
                                    Button("Load Earlier Messages") { model.loadEarlier(anchor: visibleMessage) }.disabled(!model.canLoadEarlier)
                                    Button("Load Later Messages") { model.loadLater(anchor: visibleMessage) }.disabled(!model.canLoadLater)
                                } label: { Image(systemName: "ellipsis") }
                                    .menuStyle(.borderlessButton).fixedSize().help("Message navigation").accessibilityLabel("Message navigation")
                                if model.newMessages > 0 {
                                    Button("\(model.newMessages) new \(model.newMessages == 1 ? "message" : "messages")") { model.showNewMessages() }.disabled(model.reading)
                                } else {
                                    Text(transcript.total == 0 ? "No messages" : "\(transcript.total) \(transcript.total == 1 ? "message" : "messages")")
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer()
                                if !model.resultsQuery.isEmpty {
                                    Text(model.matchingMessages.isEmpty ? "No matches in conversation prose" : "Match \(model.matchIndex + 1) of \(model.matchingMessages.count)")
                                        .font(.caption).foregroundStyle(.secondary)
                                    Button { model.moveMatch(-1) } label: { Image(systemName: "chevron.up") }
                                        .help("Previous matching message (⌘⇧G)").accessibilityLabel("Previous matching message")
                                        .disabled(model.reading || model.matchingMessages.isEmpty)
                                    Button { model.moveMatch(1) } label: { Image(systemName: "chevron.down") }
                                        .help("Next matching message (⌘G)").accessibilityLabel("Next matching message")
                                        .disabled(model.reading || model.matchingMessages.isEmpty)
                                }
                            }.frame(height: 24)
                            ScrollViewReader { proxy in
                                ScrollView {
                                    LazyVStack(alignment: .leading, spacing: 16) {
                                        ForEach(transcript.start..<transcript.end, id: \.self) { index in
                                            let message = transcript.messages[index - transcript.start]
                                            VStack(alignment: .leading, spacing: 5) {
                                                HStack {
                                                    Text(message.role.capitalized).font(.headline)
                                                    if let timestamp = message.timestamp, let date = WorkspaceActivityEntry.date(timestamp) {
                                                        Text(date.formatted()).font(.caption).foregroundStyle(.secondary)
                                                    }
                                                }
                                                WorkspaceTranscriptText(text: message.text.reveal(), terms: transcript.matchTerms, preferences: preferences)
                                            }.padding(4)
                                                .background(model.highlightedMessage == index ? Color.accentColor.opacity(0.16) : Color.clear, in: RoundedRectangle(cornerRadius: 5))
                                                .id(index)
                                        }
                                    }.scrollTargetLayout().padding(8)
                                }
                                .accessibilityIdentifier("history-transcript")
                                .focusable().focused($focus, equals: .transcript)
                                .scrollPosition(id: $visibleMessage, anchor: .top)
                                .onChange(of: visibleMessage) { _, message in model.rememberVisibleMessage(message) }
                                .onChange(of: model.scrollRequest) { _, request in
                                    if let request { proxy.scrollTo(request.message, anchor: .top) }
                                }
                                .onAppear { if let request = model.scrollRequest { proxy.scrollTo(request.message, anchor: .top) } }
                            }
                        }
                    } else { WorkspaceEmpty(title: "Conversation preview", detail: "Select a conversation to read or resume it.", symbol: "text.bubble") }
                    Spacer(minLength: 0)
                }.padding(.horizontal, 8).padding(.vertical, 4).frame(minWidth: 360, maxWidth: .infinity, maxHeight: .infinity)
            }
            }
            }
        }.padding(.horizontal, 12).padding(.vertical, 8)
            .onAppear { focus = .search }
            .background(WorkspaceReviewShortcuts(handle: handleShortcut).frame(width: 0, height: 0))
    }
    private var emptyResultsDetail: String {
        guard !model.includeAutomated, model.hiddenAutomated > 0 else { return "" }
        return "\(model.hiddenAutomated) automated \(model.hiddenAutomated == 1 ? "match" : "matches") hidden."
    }
    private func toggleResults() {
        model.layout.collapsed.toggle()
        focus = model.layout.collapsed ? .transcript : .results
    }
    private func handleShortcut(_ key: String, _ modifiers: NSEvent.ModifierFlags) -> Bool {
        if modifiers == .command {
            switch key {
            case "r": if model.canRefreshTranscript { model.refreshTranscript() }
            case "f": focus = .search
            case "g": model.moveMatch(1)
            case "\r": model.act(fork: false)
            default: return false
            }
        } else if modifiers == [.command, .shift] {
            switch key {
            case "g": model.moveMatch(-1)
            case "\r": model.act(fork: true)
            default: return false
            }
        } else if modifiers == [.command, .option], key == "s" { toggleResults() }
        else { return false }
        return true
    }
}

/// The divider has a generous hit area but the same thin rule as native splits.
private struct WorkspaceHistoryResizeCursor: NSViewRepresentable {
    final class CursorView: NSView {
        override func resetCursorRects() { addCursorRect(bounds, cursor: .resizeLeftRight) }
        override func hitTest(_ point: NSPoint) -> NSView? { nil }
    }
    func makeNSView(context: Context) -> CursorView { CursorView() }
    func updateNSView(_ view: CursorView, context: Context) {}
}
