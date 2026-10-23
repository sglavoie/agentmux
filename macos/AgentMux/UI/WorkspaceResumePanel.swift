import AppKit
import SwiftUI

struct WorkspaceResumeView: View {
    @ObservedObject var model: WorkspaceResumeModel
    let cancel: () -> Void
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Resume Conversation").font(.title2.bold())
            Text(model.identity.encoded).font(.caption).textSelection(.enabled)
            if let conversation = model.conversation {
                Text(conversation.displayName).font(.headline)
                Text("Profile: \(conversation.profile.isEmpty ? "Recorded source profile" : conversation.profile) · \(conversation.harness)")
                Text(conversation.cwd).font(.caption).textSelection(.enabled)
            }
            Text("Destination: Workspace (tmux). If this conversation is already running, its existing session will be focused.")
                .foregroundStyle(.secondary)
            if model.reading { ProgressView("Checking conversation…").controlSize(.small) }
            if model.acting { ProgressView("Opening conversation…").controlSize(.small) }
            if let error = model.error {
                WorkspaceNotice(message: error, kind: .error, actionTitle: "Refresh", action: { model.load() })
            }
            HStack {
                WorkspaceFeedbackHistoryButton(history: model.feedbackHistory)
                Spacer()
                Button("Cancel", action: cancel).keyboardShortcut(.cancelAction).disabled(model.acting)
                Button("Resume Conversation") { model.resume() }.keyboardShortcut(.defaultAction).disabled(!model.canResume)
            }
        }.padding(20).frame(width: 460).fixedSize(horizontal: false, vertical: true)
    }
}

@MainActor
final class WorkspaceResumePanel {
    let model: WorkspaceResumeModel
    let panel = NSPanel(contentRect: .zero, styleMask: [.titled], backing: .buffered, defer: false)
    init(model: WorkspaceResumeModel) {
        self.model = model
        panel.isReleasedWhenClosed = false
        panel.title = "Resume Conversation"
        panel.contentView = NSHostingView(rootView: WorkspaceResumeView(model: model, cancel: { [weak self] in self?.close() }))
        if let content = panel.contentView { panel.setContentSize(content.fittingSize) }
        model.onFinished = { [weak self] in self?.close() }
    }
    func show(on window: NSWindow) {
        guard panel.sheetParent == nil else { return }
        window.beginSheet(panel)
        model.load()
    }
    func close() {
        guard !model.acting else { return }
        model.close()
        panel.sheetParent?.endSheet(panel)
        panel.orderOut(nil)
    }
}
