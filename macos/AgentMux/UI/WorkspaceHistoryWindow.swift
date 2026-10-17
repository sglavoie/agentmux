import AppKit
import SwiftUI

@MainActor
final class WorkspaceHistoryWindow: NSObject, NSWindowDelegate {
    let model: WorkspaceHistoryModel
    private(set) var window: NSWindow?
    var onVisibilityChanged: (Bool) -> Void = { _ in }

    init(model: WorkspaceHistoryModel) { self.model = model }

    func show(project: String, conversation: ConversationIdentity?) {
        if window == nil {
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 680),
                                  styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
            window.title = "Conversation History"
            window.minSize = NSSize(width: 720, height: 450)
            window.isReleasedWhenClosed = false
            window.delegate = self
            let host = WorkspaceReviewHost(rootView: WorkspaceHistoryView(model: model))
            host.closesWindow = true
            window.contentView = host
            window.setFrameAutosaveName("WorkspaceHistory")
            window.center()
            self.window = window
        }
        onVisibilityChanged(true)
        model.open(project: project, conversation: conversation)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    func windowWillClose(_ notification: Notification) {
        model.close()
        onVisibilityChanged(false)
    }
}

struct WorkspaceHistoryView: View {
    @ObservedObject var preferences: WorkspaceReadingPreferences = .shared
    @ObservedObject var model: WorkspaceHistoryModel
    @State private var visibleMessage: Int?
    private enum Focus: Hashable { case search, results }
    @FocusState private var focus: Focus?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                TextField("Search full conversations", text: $model.query)
                    .focused($focus, equals: .search)
                    .onKeyPress(.downArrow) { focus = .results; return .handled }
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { model.search() }
                    .onChange(of: model.query) { _, _ in model.scheduleSearch() }
                Button("Search") { model.search() }
                    .help("Search conversations. ⌘F focuses the search field.")
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
            HSplitView {
                VStack(spacing: 6) {
                WorkspaceStatus(loading: model.loading, title: "Searching…", retained: !model.entries.isEmpty,
                                feedback: model.error.map { WorkspaceFeedback(target: "Conversation search", message: $0,
                                    nextStep: "Check Settings if amx is unavailable, then search again.") },
                                recover: { model.search() })
                List(selection: Binding(get: { model.selection }, set: { model.select($0) })) {
                    ForEach(model.entries) { entry in
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                Text(entry.conversation.displayName).font(.headline)
                                if model.liveConversations.contains(entry.id) { Text("Live").font(.caption).foregroundStyle(.green) }
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
                    .frame(minWidth: 200, idealWidth: 300, maxWidth: .infinity)
                    .overlay {
                        if model.entries.isEmpty && !model.loading && model.error == nil {
                            VStack(spacing: 10) {
                                Text("No conversations found").font(.headline)
                                if !model.resultsQuery.isEmpty { Button("Clear search") { model.query = "" } }
                                if model.scope != .all { Button("Search all history") { model.scope = .all } }
                                if !model.includeAutomated {
                                    Button(model.hiddenAutomated > 0 ? "Include \(model.hiddenAutomated) automated matches" : "Include automated sessions") { model.includeAutomated = true }
                                }
                            }.padding(12)
                        }
                    }
                    if model.hasMoreResults {
                        Button(model.loadingMore ? "Loading…" : "Load more conversations") { model.loadMoreResults() }
                            .disabled(model.loading || model.loadingMore)
                    }
                    if !model.entries.isEmpty && model.hiddenAutomated > 0 {
                        Button("Include \(model.hiddenAutomated) automated matches") { model.includeAutomated = true }.font(.caption)
                    }
                }
                VStack(alignment: .leading, spacing: 4) {
                    if let selected = model.selected {
                        HStack {
                            Text(selected.conversation.displayName).font(.headline).lineLimit(1).help(selected.conversation.displayName + "\n" + selected.conversation.cwd)
                                .frame(maxWidth: .infinity, alignment: .leading)
                            Button(model.liveConversations.contains(selected.id) ? "Focus live session" : "Resume") { model.act(fork: false) }
                                .buttonStyle(.borderedProminent).fixedSize()
                                .help("Resume or focus the selected session (⌘Return)").disabled(!model.canAct)
                            Button("Fork") { model.act(fork: true) }.fixedSize().help("Fork the selected conversation (⌘⇧Return)").disabled(!model.canAct)
                            Button { model.refreshTranscript() } label: { Image(systemName: "arrow.clockwise") }
                                .help("Refresh conversation without moving your place (⌘R)")
                                .accessibilityLabel("Refresh conversation").disabled(!model.canRefreshTranscript)
                        }
                        WorkspaceStatus(loading: model.reading || model.acting,
                                        title: model.acting ? "Opening conversation…" : "Loading transcript…", retained: model.transcript != nil,
                                        showTarget: model.transcriptError == nil && model.actionFeedback != nil,
                                        feedback: model.transcriptError.map { WorkspaceFeedback(target: selected.conversation.displayName,
                                            message: $0, nextStep: "Reload the transcript to check again. Existing text remains readable.", recovery: .transcript) } ?? model.actionFeedback,
                                        recover: { if model.transcriptError != nil { model.refreshTranscript() } else { model.search() } })
                        if let transcript = model.transcript {
                            HStack(spacing: 4) {
                                Menu {
                                    Text(transcript.total == 0 ? "No messages" : "Messages \(transcript.start + 1)–\(transcript.end) of \(transcript.total)")
                                    Button("Load earlier messages") { model.loadEarlier(anchor: visibleMessage) }.disabled(!model.canLoadEarlier)
                                    Button("Load later messages") { model.loadLater(anchor: visibleMessage) }.disabled(!model.canLoadLater)
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
                                            }.id(index)
                                        }
                                    }.scrollTargetLayout().padding(8)
                                }
                                .accessibilityIdentifier("history-transcript")
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
        }.padding(.horizontal, 12).padding(.vertical, 8)
            .onAppear { focus = .search }
            .background(WorkspaceReviewShortcuts(handle: handleShortcut).frame(width: 0, height: 0))
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
        } else { return false }
        return true
    }
}
