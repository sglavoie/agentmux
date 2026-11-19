import AppKit
import SwiftUI

struct WorkspaceNewSessionView: View {
    @ObservedObject var model: WorkspaceNewSessionModel
    let cancel: () -> Void
    let chooseDirectory: () -> Void

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
}

@MainActor
final class WorkspaceNewSessionPanel {
    let model: WorkspaceNewSessionModel
    let panel = NSPanel(contentRect: .zero, styleMask: [.titled], backing: .buffered, defer: false)
    private(set) var directoryPicker: NSOpenPanel?
    private let makeDirectoryPicker: () -> NSOpenPanel

    init(model: WorkspaceNewSessionModel, makeDirectoryPicker: @escaping () -> NSOpenPanel = { NSOpenPanel() }) {
        self.model = model
        self.makeDirectoryPicker = makeDirectoryPicker
        WorkspaceWindowChrome.adopt(panel)
        panel.isReleasedWhenClosed = false
        panel.title = "New Session Below"
        panel.contentView = NSHostingView(rootView: WorkspaceNewSessionView(
            model: model,
            cancel: { [weak self] in self?.close() },
            chooseDirectory: { [weak self] in self?.chooseDirectory() }
        ))
        if let content = panel.contentView { panel.setContentSize(content.fittingSize) }
    }
    func show(on window: NSWindow) {
        guard panel.sheetParent == nil, window.attachedSheet == nil else { return }
        window.beginSheet(panel)
        model.load()
    }
    func close() {
        guard !model.launching else { return }
        let picker = directoryPicker
        directoryPicker = nil
        picker?.cancel(nil)
        panel.sheetParent?.endSheet(panel)
        panel.orderOut(nil)
    }

    func chooseDirectory() {
        guard panel.sheetParent != nil, panel.attachedSheet == nil, directoryPicker == nil,
              !model.loading, !model.launching, !model.submitted else { return }
        let picker = makeDirectoryPicker()
        WorkspaceWindowChrome.adopt(picker)
        picker.canChooseFiles = false
        picker.canChooseDirectories = true
        picker.allowsMultipleSelection = false
        picker.prompt = "Choose"
        picker.directoryURL = URL(fileURLWithPath: model.launchDirectory, isDirectory: true)
        directoryPicker = picker
        // The session dialog is already a sheet. Keep its picker in that sheet
        // hierarchy and return to AppKit instead of nesting a modal run loop.
        picker.beginSheetModal(for: panel) { [weak self, weak picker] response in
            MainActor.assumeIsolated {
                guard let self, let picker, self.directoryPicker === picker else { return }
                self.directoryPicker = nil
                if response == .OK, let url = picker.url { self.model.directory = url.path }
            }
        }
    }
}
