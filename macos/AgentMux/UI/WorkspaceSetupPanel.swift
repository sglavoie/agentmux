import AppKit
import SwiftUI

struct WorkspaceSetupView: View {
    @ObservedObject var model: WorkspaceSetupModel
    let cancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Set up first project").font(.title2.bold())
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    if model.loading { ProgressView("Looking for installed agents and recent projects…").controlSize(.small) }
                    if model.detection != nil {
                        if model.choices.isEmpty {
                            Text("No installed agent or saved profile found. Looked for: \(model.searchedCLIs). Install and authenticate an agent CLI, or set up manually.")
                        } else {
                            if model.choices.count > 1 {
                                Picker("Agent CLI", selection: $model.harness) {
                                    ForEach(model.choices, id: \.harness) { Text($0.name).tag($0.harness) }
                                }
                            } else if let selected = model.selected {
                                Text("Found \(selected.name)").font(.headline)
                            }
                            if let selected = model.selected {
                                Text(selected.cliAvailable ? (selected.cliPath ?? selected.harness) : "Saved profile found; install \(selected.harness) before launching.")
                                    .font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                            }
                            Text("Project directory").font(.headline)
                            if model.directories.isEmpty {
                                Text("No recent project directories found. Choose a folder to get started.")
                            } else {
                                Picker("Project directory", selection: $model.directory) {
                                    ForEach(model.directories, id: \.self) { Text($0).tag($0) }
                                }.pickerStyle(.radioGroup).labelsHidden()
                            }
                            Button("Choose another folder…", action: chooseDirectory)
                            Toggle("Sync Agent Mux hooks into \(model.selected?.home ?? "the profile home") (needed for live status)", isOn: $model.syncHooks)
                                .toggleStyle(.checkbox)
                            Text("Setup saves your project. Before launching it from the workspace, sign in to your agent if you have not already.")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                        ForEach(model.detection?.warnings ?? [], id: \.self) { Text($0).font(.caption).foregroundStyle(.secondary) }
                    }
                }.frame(maxWidth: .infinity, alignment: .leading)
                    .disabled(model.loading || model.saving || model.submitted)
                if let error = model.error {
                    Text(error).foregroundStyle(.red).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading).padding(.top, 8)
                }
            }
            if model.saving { ProgressView("Saving project and hook choices…").controlSize(.small) }
            HStack {
                Button("Set up manually…") { model.manual() }.disabled(model.saving)
                if model.detection == nil && !model.loading {
                    Button("Retry detection") { model.detect() }.disabled(model.submitted)
                }
                Spacer()
                Button(model.saved ? "Close" : "Cancel", action: cancel)
                    .keyboardShortcut(.cancelAction).disabled(model.saving)
                Button("Set up") { model.setUp() }.keyboardShortcut(.defaultAction).disabled(!model.canSetUp)
            }
        }.padding(20).frame(width: 600, height: 470)
    }

    private func chooseDirectory() {
        let panel = NSOpenPanel()
        WorkspaceWindowChrome.adopt(panel)
        panel.canChooseFiles = false; panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false; panel.prompt = "Choose"
        if !model.directory.isEmpty { panel.directoryURL = URL(fileURLWithPath: model.directory, isDirectory: true) }
        guard panel.runModal() == .OK, let url = panel.url else { return }
        model.directory = url.path
    }
}

@MainActor
final class WorkspaceSetupPanel {
    let model: WorkspaceSetupModel
    let panel = NSPanel(contentRect: .zero, styleMask: [.titled], backing: .buffered, defer: false)

    init(model: WorkspaceSetupModel) {
        self.model = model
        WorkspaceWindowChrome.adopt(panel)
        panel.title = "Set up first project"
        panel.isReleasedWhenClosed = false
        panel.contentView = NSHostingView(rootView: WorkspaceSetupView(model: model, cancel: { [weak self] in self?.close() }))
        if let content = panel.contentView { panel.setContentSize(content.fittingSize) }
    }

    func show(on window: NSWindow) {
        guard panel.sheetParent == nil, window.attachedSheet == nil else { return }
        window.beginSheet(panel)
        model.detect()
    }

    func close() {
        guard !model.saving else { return }
        model.close()
        panel.sheetParent?.endSheet(panel)
        panel.orderOut(nil)
    }
}

extension WorkspaceWindowController {
    func showSetup() {
        guard window.attachedSheet == nil else { return }
        if setupPanel == nil {
            let model = WorkspaceSetupModel(command: { [weak self] in self?.delegate?.workspaceRecapCommand() }, runner: setupRunner)
            let panel = WorkspaceSetupPanel(model: model)
            model.onSaved = { [weak self] in self?.delegate?.workspaceSetupFinished() }
            model.onFinished = { [weak panel] in panel?.close() }
            model.onManual = { [weak self, weak panel] in
                panel?.close()
                self?.open(.setup)
            }
            setupPanel = panel
        }
        setupPanel?.show(on: window)
    }
}
