import AppKit
import SwiftUI

struct WorkspaceHandoffView: View {
    @ObservedObject var model: WorkspaceHandoffModel
    let cancel: () -> Void
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Continue with Another Agent").font(.title2.bold())
            HStack {
                Text("From " + model.source.encoded).font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                Spacer()
                Button("Read Source Conversation") { model.onSource?() }.disabled(model.launching)
            }
            Picker("Receiving agent", selection: $model.profile) {
                ForEach(model.profiles, id: \.name) { profile in Text("\(profile.harness) · \(profile.name)").tag(profile.name) }
            }.disabled(model.loading || model.submitted)
            if !model.directory.isEmpty {
                Text("Checkout: " + model.directory).font(.caption).textSelection(.enabled)
            }
            Text("Context to pass along").font(.headline)
            Text("Review and edit this snapshot of the conversation and checkout. The receiving agent will verify repository state before acting.")
                .font(.caption).foregroundStyle(.secondary)
            TextEditor(text: $model.content).font(.system(.body, design: .monospaced))
                .accessibilityLabel("Handoff context")
                .frame(minHeight: 220, maxHeight: .infinity)
                .overlay(RoundedRectangle(cornerRadius: 5).stroke(Color.secondary.opacity(0.3)))
                .disabled(model.loading || model.submitted)
            Text("Next instruction (optional)").font(.headline)
            TextField("For example: Review this implementation for concurrency bugs", text: $model.instruction, axis: .vertical)
                .lineLimit(2...4).disabled(model.loading || model.submitted)
            if model.content.utf8.count > 1024 * 1024 || model.instruction.utf8.count > 16 * 1024 {
                Text("Context must fit within 1 MiB and the next instruction within 16 KiB.").font(.caption).foregroundStyle(.red)
            }
            if model.loading { ProgressView("Loading conversation and checkout context…").controlSize(.small) }
            if model.launching { ProgressView("Starting receiving agent…").controlSize(.small) }
            if let error = model.error { Text(error).foregroundStyle(.red).textSelection(.enabled) }
            if !model.artifactPath.isEmpty {
                Button("Reveal Saved Handoff") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: model.artifactPath)]) }
            }
            HStack {
                if model.directory.isEmpty && !model.loading { Button("Retry Preview") { model.load() }.disabled(model.submitted) }
                Spacer()
                Button(model.submitted && !model.launching ? "Close" : "Cancel", action: cancel).keyboardShortcut(.cancelAction).disabled(model.launching)
                Button("Launch Agent") { model.launch() }.keyboardShortcut(.defaultAction).disabled(!model.canLaunch)
            }
        }.padding(20).frame(minWidth: 620, minHeight: 600)
    }
}

@MainActor
final class WorkspaceHandoffPanel: NSObject, NSWindowDelegate {
    let model: WorkspaceHandoffModel
    let panel = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 680, height: 700), styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
    init(model: WorkspaceHandoffModel) {
        self.model = model
        super.init()
        panel.delegate = self
        panel.isReleasedWhenClosed = false
        panel.title = "Continue with Another Agent"
        panel.contentView = NSHostingView(rootView: WorkspaceHandoffView(model: model, cancel: { [weak self] in self?.close() }))
        panel.minSize = NSSize(width: 660, height: 640)
    }
    // Keep the source window usable while the operator reviews context.
    func show(on window: NSWindow) {
        panel.setFrameOrigin(NSPoint(x: window.frame.midX - panel.frame.width / 2, y: window.frame.midY - panel.frame.height / 2))
        panel.makeKeyAndOrderFront(nil)
        model.load()
    }
    func windowShouldClose(_ sender: NSWindow) -> Bool { !model.launching }
    func windowWillClose(_ notification: Notification) { model.close() }
    func close() {
        guard !model.launching else { return }
        model.close()
        panel.orderOut(nil)
    }
}
