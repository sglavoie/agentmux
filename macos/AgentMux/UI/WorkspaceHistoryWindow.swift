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
    @ObservedObject var model: WorkspaceHistoryModel
    @State private var visibleMessage: Int?
    private enum Focus: Hashable { case search, results }
    @FocusState private var focus: Focus?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Conversation History").font(.title2)
            HStack {
                TextField("Search full conversations", text: $model.query)
                    .focused($focus, equals: .search)
                    .onKeyPress(.downArrow) { focus = .results; return .handled }
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { model.search() }
                    .onChange(of: model.query) { _, _ in model.scheduleSearch() }
                Button("Search") { model.search() }
                    .help("Search conversations. ⌘F focuses the search field.")
            }
            HStack {
                Picker("Search in", selection: $model.scope) {
                    ForEach(WorkspaceHistoryScope.allCases, id: \.self) { scope in
                        Text(scope.rawValue).tag(scope)
                            .disabled(scope == .project && model.project.isEmpty || scope == .conversation && model.currentConversation == nil)
                    }
                }.frame(maxWidth: 270)
                    .onChange(of: model.scope) { _, _ in model.search() }
                Toggle("Include automated sessions", isOn: $model.includeAutomated)
                    .onChange(of: model.includeAutomated) { _, _ in model.search() }
                Spacer()

                Text("\(model.entries.count) of \(model.totalResults) conversations").foregroundStyle(.secondary)
            }
            if model.scope == .project { Text(model.project).font(.caption).foregroundStyle(.secondary) }
            if model.scope == .conversation, let identity = model.currentConversation {
                Text(identity.encoded).font(.caption).foregroundStyle(.secondary)
            }
            if model.loading { WorkspaceLoading(title: "Searching conversations…", retained: !model.entries.isEmpty) }
            if let error = model.error {
                WorkspaceNotice(message: error + (model.entries.isEmpty ? "" : " Showing the previous results."), kind: .error,
                                action: { model.search() })
            }
            HSplitView {
                VStack(spacing: 6) {
                List(selection: Binding(get: { model.selection }, set: { model.select($0) })) {
                    ForEach(model.entries) { entry in
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                Text(entry.conversation.displayName).font(.headline)
                                if model.liveConversations.contains(entry.id) { Text("Live").font(.caption).foregroundStyle(.green) }
                            }
                            Text("\(entry.conversation.harness) · \(entry.conversation.profile)").font(.caption).foregroundStyle(.secondary)
                            if model.scope == .all && !entry.conversation.cwd.isEmpty {
                                Text(entry.conversation.cwd)
                                    .font(.caption).foregroundStyle(.secondary).lineLimit(1).help(entry.conversation.cwd)
                            }
                            if let lastAt = entry.conversation.lastAt {
                                TimelineView(.periodic(from: .now, by: 30)) { context in
                                    Text(lastAt.formatted(.relative(presentation: .named, unitsStyle: .abbreviated)))
                                        .font(.caption).foregroundStyle(.secondary).help(lastAt.formatted())
                                }
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
                VStack(alignment: .leading, spacing: 8) {
                    if let selected = model.selected {
                        Text(selected.conversation.displayName).font(.title2)
                        Text(selected.conversation.cwd).font(.caption).textSelection(.enabled)
                        HStack {
                            Button(model.liveConversations.contains(selected.id) ? "Focus live session" : "Resume") { model.act(fork: false) }
                                .buttonStyle(.borderedProminent)
                                .help("Resume or focus the selected session (⌘Return)")
                            Button("Fork") { model.act(fork: true) }.help("Fork the selected conversation (⌘⇧Return)")
                            if model.acting { ProgressView().controlSize(.small) }
                            Spacer()
                            Button { model.refreshTranscript() } label: { Image(systemName: "arrow.clockwise") }
                                .help("Refresh conversation without moving your place (⌘R)")
                                .accessibilityLabel("Refresh conversation").disabled(model.reading)
                        }.disabled(!model.canAct)
                        Text(model.liveConversations.contains(selected.id)
                             ? "Return to the running conversation, or fork a separate conversation."
                             : "Continue this conversation in the workspace, or fork a separate one.")
                            .font(.caption).foregroundStyle(.secondary)
                        if let notice = model.actionNotice { Text(notice).font(.callout).textSelection(.enabled) }
                        if let error = model.transcriptError {
                            WorkspaceNotice(message: error, kind: .error, action: { model.loadTranscript() })
                        }
                        if model.reading { WorkspaceLoading(title: "Loading transcript…", retained: model.transcript != nil) }
                        if let transcript = model.transcript {
                            HStack {
                                Text(transcript.total == 0 ? "No messages" : "Messages \(transcript.start + 1)–\(transcript.end) of \(transcript.total)")
                                    .font(.caption).foregroundStyle(.secondary)
                                Spacer()
                                if !model.resultsQuery.isEmpty {
                                    Text(model.matchingMessages.isEmpty ? "No matches in conversation prose" : "Matching message \(model.matchIndex + 1) of \(model.matchingMessages.count)")
                                        .font(.caption).foregroundStyle(.secondary)
                                    Button { model.moveMatch(-1) } label: { Image(systemName: "chevron.up") }
                                        .help("Previous matching message (⌘⇧G)").accessibilityLabel("Previous matching message")
                                        .disabled(model.reading || model.matchingMessages.isEmpty)
                                    Button { model.moveMatch(1) } label: { Image(systemName: "chevron.down") }
                                        .help("Next matching message (⌘G)").accessibilityLabel("Next matching message")
                                        .disabled(model.reading || model.matchingMessages.isEmpty)
                                }
                            }
                            HStack {
                                if model.newMessages > 0 {
                                    Button("\(model.newMessages) new \(model.newMessages == 1 ? "message" : "messages")") { model.showNewMessages() }.disabled(model.reading)
                                }
                                if model.canLoadEarlier { Button("Load earlier messages") { model.loadEarlier(anchor: visibleMessage) } }
                                Spacer()
                                if model.canLoadLater { Button("Load later messages") { model.loadLater(anchor: visibleMessage) } }
                            }
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
                                                WorkspaceTranscriptText(text: message.text.reveal(), terms: transcript.matchTerms)
                                            }.id(index)
                                        }
                                    }.scrollTargetLayout().padding(8)
                                }
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
                }.padding(12).frame(minWidth: 360, maxWidth: .infinity, maxHeight: .infinity)
            }
        }.padding(12)
            .onAppear { focus = .search }
            .background(WorkspaceReviewShortcuts(handle: handleShortcut).frame(width: 0, height: 0))
    }
    private func handleShortcut(_ key: String, _ modifiers: NSEvent.ModifierFlags) -> Bool {
        if modifiers == .command {
            switch key {
            case "r": model.search()
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
