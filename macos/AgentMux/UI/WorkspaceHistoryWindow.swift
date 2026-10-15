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
            window.contentView = NSHostingView(rootView: WorkspaceHistoryView(model: model))
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

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                TextField("Search full conversations", text: $model.query)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { model.search() }
                    .onChange(of: model.query) { _, _ in model.scheduleSearch() }
                Button("Search") { model.search() }
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
                if model.loading { ProgressView().controlSize(.small) }
                Text("\(model.entries.count) conversations").foregroundStyle(.secondary)
            }
            if model.scope == .project { Text(model.project).font(.caption).foregroundStyle(.secondary) }
            if model.scope == .conversation, let identity = model.currentConversation {
                Text(identity.encoded).font(.caption).foregroundStyle(.secondary)
            }
            if let error = model.error { Text(error).foregroundStyle(.red).textSelection(.enabled) }
            HSplitView {
                List(selection: Binding(get: { model.selection }, set: { model.select($0) })) {
                    ForEach(model.entries) { entry in
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                Text(entry.conversation.displayName).font(.headline)
                                if model.liveConversations.contains(entry.id) { Text("Live").font(.caption).foregroundStyle(.green) }
                            }
                            Text("\(entry.conversation.harness) · \(entry.conversation.profile)").font(.caption).foregroundStyle(.secondary)
                            Text(entry.preview.reveal()).font(.caption).lineLimit(3)
                            if let matches = entry.matches { Text("\(matches) matches").font(.caption2).foregroundStyle(.secondary) }
                        }.padding(.vertical, 3).tag(entry.id)
                    }
                }.frame(minWidth: 230, idealWidth: 320, maxWidth: 450)
                    .overlay {
                        if model.entries.isEmpty && !model.loading { Text("No conversations match.").foregroundStyle(.secondary) }
                    }
                VStack(alignment: .leading, spacing: 8) {
                    if let selected = model.selected {
                        Text(selected.conversation.displayName).font(.title2)
                        Text(selected.conversation.cwd).font(.caption).textSelection(.enabled)
                        HStack {
                            Button(model.liveConversations.contains(selected.id) ? "Focus live session" : "Resume") { model.act(fork: false) }
                            Button("Fork") { model.act(fork: true) }
                            if model.acting { ProgressView().controlSize(.small) }
                        }.disabled(!model.canAct)
                        if let notice = model.actionNotice { Text(notice).font(.callout).textSelection(.enabled) }
                        if let error = model.transcriptError {
                            Text(error).foregroundStyle(.red)
                            Button("Load Again") { model.loadTranscript() }
                        }
                        if model.reading { ProgressView("Loading transcript…") }
                        if let transcript = model.transcript {
                            Text("Showing \(transcript.messages.count) of \(transcript.total) messages").font(.caption).foregroundStyle(.secondary)
                            if model.canLoadEarlier { Button("Load earlier messages") { model.loadEarlier() } }
                            ScrollView {
                                LazyVStack(alignment: .leading, spacing: 16) {
                                    ForEach(Array(transcript.messages.enumerated()), id: \.offset) { _, message in
                                        VStack(alignment: .leading, spacing: 5) {
                                            Text(message.role.capitalized).font(.headline)
                                            Text(message.text.reveal()).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
                                        }
                                    }
                                }.padding(8)
                            }
                        }
                    } else { Text("Select a conversation to preview its transcript.").foregroundStyle(.secondary) }
                    Spacer(minLength: 0)
                }.padding(12).frame(minWidth: 360, maxWidth: .infinity, maxHeight: .infinity)
            }
        }.padding(12)
    }
}
