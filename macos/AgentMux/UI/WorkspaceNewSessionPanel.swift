import AppKit
import SwiftUI

struct WorkspaceNewSessionView: View {
    @ObservedObject var model: WorkspaceNewSessionModel
    let cancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("New Session Below").font(.title2.bold())
            Text(model.source.project).font(.headline)
            Picker("Harness", selection: $model.profile) {
                ForEach(model.profiles, id: \.name) { profile in
                    Text("\(profile.harness) · \(profile.name)").tag(profile.name)
                }
            }.disabled(model.loading || model.launching || model.submitted)
            VStack(alignment: .leading, spacing: 6) {
                Text("Directory (optional)")
                HStack {
                    TextField(model.defaultDirectory, text: $model.directory)
                        .accessibilityLabel("Launch directory")
                    Button("Choose…", action: chooseDirectory)
                }.disabled(model.loading || model.launching || model.submitted)
                Text("Leave empty to use \(model.defaultDirectory.isEmpty ? "the current session's directory" : model.defaultDirectory). Relative paths start there.")
                    .font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
            }
            if model.loading { ProgressView("Loading harnesses and directory…").controlSize(.small) }
            if model.launching { ProgressView("Starting session…").controlSize(.small) }
            if let error = model.error {
                Text(error).foregroundStyle(.red).textSelection(.enabled)
                if model.project == nil && !model.submitted { Button("Retry") { model.load() } }
            }
            HStack {
                Spacer()
                Button("Cancel", action: cancel).keyboardShortcut(.cancelAction).disabled(model.launching)
                Button("Launch Session") { model.launch() }.keyboardShortcut(.defaultAction).disabled(!model.canLaunch)
            }
        }.padding(20).frame(width: 460).fixedSize(horizontal: false, vertical: true)
    }

    private func chooseDirectory() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false; panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"
        panel.directoryURL = URL(fileURLWithPath: model.launchDirectory, isDirectory: true)
        guard panel.runModal() == .OK, let url = panel.url else { return }
        model.directory = url.path
    }
}

@MainActor
final class WorkspaceNewSessionPanel {
    let model: WorkspaceNewSessionModel
    let panel = NSPanel(contentRect: .zero, styleMask: [.titled], backing: .buffered, defer: false)

    init(model: WorkspaceNewSessionModel) {
        self.model = model
        panel.isReleasedWhenClosed = false
        panel.title = "New Session Below"
        panel.contentView = NSHostingView(rootView: WorkspaceNewSessionView(model: model, cancel: { [weak self] in self?.close() }))
        if let content = panel.contentView { panel.setContentSize(content.fittingSize) }
    }
    func show(on window: NSWindow) {
        guard panel.sheetParent == nil else { return }
        window.beginSheet(panel)
        model.load()
    }
    func close() {
        guard !model.launching else { return }
        panel.sheetParent?.endSheet(panel)
        panel.orderOut(nil)
    }
}
