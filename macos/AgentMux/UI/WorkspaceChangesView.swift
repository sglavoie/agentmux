import AppKit
import SwiftUI

/// Also embeddable beside an agent in the workspace's second pane.
struct WorkspaceChangesView: View {
    @ObservedObject var model: WorkspaceChangesModel
    @State private var confirmedTree: WorkspaceWorktreeReview.Review?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Checkout Changes").font(.title2)
            if let changes = model.changes {
                Text(changes.root).font(.caption).textSelection(.enabled)
                Text("Includes changes from every session using this checkout.").font(.caption).foregroundStyle(.secondary)
            }
            HStack {
                TextField("Comparison base", text: $model.base).textFieldStyle(.roundedBorder).onSubmit { model.refresh() }
                Button("Compare") { model.refresh() }.disabled(model.loading || model.landing)
                if model.loading { ProgressView().controlSize(.small) }
            }
            if let error = model.error { Text(error).foregroundStyle(.red).textSelection(.enabled) }
            if let error = model.worktreeError { Text("Managed worktree review: \(error)").font(.caption).foregroundStyle(.secondary) }
            if let tree = model.worktree {
                HStack {
                    VStack(alignment: .leading) {
                        Text("\(tree.branch ?? "Worktree") → \(tree.sourceBranch)").font(.headline)
                        Text("\(tree.ahead) ahead · \(tree.behind) behind · \(tree.lands)").font(.caption)
                        ForEach(tree.warnings ?? [], id: \.self) { Text($0).font(.caption).foregroundStyle(.orange) }
                        ForEach(tree.conflicts ?? [], id: \.self) { Text("Conflict: \($0)").font(.caption).foregroundStyle(.red) }
                    }
                    Spacer()
                    Button("Compare with source") { model.compareWithSource() }.disabled(model.loading || model.landing)
                    Button("Land…") { confirmedTree = tree }.disabled(!model.canLand).help(model.landingReason ?? "Fast-forward the source branch")
                }
            }
            if let notice = model.landNotice { Text(notice).textSelection(.enabled) }
            if let changes = model.changes {
                Text("\(changes.files.count) files · +\(changes.files.reduce(0) { $0 + $1.added }) −\(changes.files.reduce(0) { $0 + $1.deleted }) · base \(changes.base.prefix(12))")
                    .font(.caption).foregroundStyle(.secondary)
                HSplitView {
                    List(selection: Binding(get: { model.selectedFile }, set: { model.select($0) })) {
                        ForEach(changes.files) { file in
                            VStack(alignment: .leading, spacing: 3) {
                                Text(file.path).lineLimit(2)
                                Text(file.untracked ? "Untracked" : file.binary ? "Binary" : "+\(file.added) −\(file.deleted)")
                                    .font(.caption).foregroundStyle(.secondary)
                            }.tag(file.path)
                        }
                    }.frame(minWidth: 150, idealWidth: 220, maxWidth: 340)
                    VStack(alignment: .leading) {
                        HStack {
                            Text(model.selectedFile ?? "Select a changed file").font(.headline).lineLimit(1)
                            Spacer()
                            Button("Open in editor") { model.openFile() }.disabled(model.selectedFile == nil)
                        }
                        if model.reading { ProgressView("Loading diff…") }
                        if let error = model.patchError {
                            Text(error).foregroundStyle(.red).textSelection(.enabled)
                            Button("Reload diff") { model.select(model.selectedFile) }
                        }
                        if model.truncated { Text("Diff truncated at 2 MiB. Open the file for the full contents.").foregroundStyle(.orange) }
                        ScrollView([.horizontal, .vertical]) {
                            Text(model.patch.reveal()).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .topLeading)
                        }
                    }.padding(8).frame(minWidth: 280, maxWidth: .infinity, maxHeight: .infinity)
                }
                if changes.files.isEmpty { Text("No changes against this base.").foregroundStyle(.secondary) }
            }
            Spacer(minLength: 0)
        }.padding(12)
            .alert("Land onto \(confirmedTree?.sourceBranch ?? "the source branch")?",
                   isPresented: Binding(get: { confirmedTree != nil }, set: { if !$0 { confirmedTree = nil } })) {
                Button("Cancel", role: .cancel) { confirmedTree = nil }
                Button("Land") {
                    if let confirmedTree { model.land(target: confirmedTree) }
                    confirmedTree = nil
                }
            } message: {
                Text("Fast-forward the source branch to the committed work in \(confirmedTree?.path ?? "this worktree"). The checkout must be clean and inactive. The worktree and its branch are retained.")
            }
    }
}

@MainActor
final class WorkspaceChangesWindow: NSObject, NSWindowDelegate {
    let model: WorkspaceChangesModel
    private(set) var window: NSWindow?
    var onVisibilityChanged: (Bool) -> Void = { _ in }
    init(model: WorkspaceChangesModel) { self.model = model }

    func show() {
        if window == nil {
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1050, height: 700),
                                  styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
            window.title = "Checkout Changes"
            window.minSize = NSSize(width: 720, height: 450)
            window.isReleasedWhenClosed = false
            window.delegate = self
            window.contentView = NSHostingView(rootView: WorkspaceChangesView(model: model))
            window.setFrameAutosaveName("WorkspaceChanges")
            window.center()
            self.window = window
        }
        onVisibilityChanged(true)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool { !model.landing }
    func windowWillClose(_ notification: Notification) { model.close(); onVisibilityChanged(false) }
}
