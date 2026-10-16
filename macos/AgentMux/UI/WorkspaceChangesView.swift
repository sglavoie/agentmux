import AppKit
import SwiftUI

/// Also embeddable beside an agent in the workspace's second pane.
struct WorkspaceChangesView: View {
    @ObservedObject var model: WorkspaceChangesModel
    private enum Focus: Hashable { case base, files }
    @FocusState private var focus: Focus?
    @State private var hunk: Int?
    @State private var jumpSequence = 0
    @State private var confirmedTree: WorkspaceWorktreeReview.Review?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Checkout Changes").font(.title2)
            if let changes = model.changes {
                Text(changes.root).font(.caption).textSelection(.enabled)
                Text("Includes changes from every session using this checkout.").font(.caption).foregroundStyle(.secondary)
            }
            HStack {
                TextField("Comparison base", text: $model.base).focused($focus, equals: .base).textFieldStyle(.roundedBorder).onSubmit { model.refresh() }
                    .help("Compare against a branch or commit. ⌘⇧E focuses this field.")
                Button("Compare") { model.refresh() }.help("Refresh comparison (⌘R)").disabled(model.loading || model.landing)
            }
            if model.loading { WorkspaceLoading(title: "Comparing checkout…", retained: model.changes != nil) }
            if let error = model.error {
                WorkspaceNotice(message: error + (model.changes == nil ? "" : " Showing the previous comparison."), kind: .error,
                                action: { model.refresh() })
            }
            if let error = model.worktreeError { Text("Managed worktree review: \(error)").font(.caption).foregroundStyle(.secondary) }
            if let tree = model.worktree {
                HStack {
                    VStack(alignment: .leading) {
                        Text("\(tree.branch ?? "Worktree") → \(tree.sourceBranch)").font(.headline)
                        Text("\(tree.ahead) ahead · \(tree.behind) behind · \(WorkspaceWording.status(tree.lands))").font(.caption)
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
                Text("\(changes.files.count) \(changes.files.count == 1 ? "file" : "files") · +\(changes.files.reduce(0) { $0 + $1.added }) −\(changes.files.reduce(0) { $0 + $1.deleted }) · base \(changes.base.prefix(12))")
                    .font(.caption).foregroundStyle(.secondary)
                if !changes.files.isEmpty {
                    HSplitView {
                        List(selection: Binding(get: { model.selectedFile }, set: { model.select($0) })) {
                            ForEach(changes.files) { file in
                                VStack(alignment: .leading, spacing: 3) {
                                    Text(file.path).lineLimit(2)
                                    Text(file.untracked ? "Untracked" : file.binary ? "Binary" : "+\(file.added) −\(file.deleted)")
                                        .font(.caption).foregroundStyle(.secondary)
                                }.tag(file.path)
                            }
                        }.focused($focus, equals: .files).frame(minWidth: 150, idealWidth: 220, maxWidth: 240)
                            .help("Changed files (⌘⇧F)")
                        VStack(alignment: .leading) {
                            HStack {
                                Text(model.selectedFile ?? "Select a changed file").font(.headline).lineLimit(1).help(model.selectedFile ?? "")
                                Spacer()
                                Button("Open in editor") { model.openFile() }.help("Open the selected file (⌘E)").disabled(model.selectedFile == nil)
                            }
                            HStack {
                                Text("\(model.diff.hunks.count) \(model.diff.hunks.count == 1 ? "hunk" : "hunks")").font(.caption).foregroundStyle(.secondary)
                                Spacer()
                                Button { moveHunk(-1) } label: { Image(systemName: "chevron.up") }
                                    .help("Previous hunk (⌘⇧G)").accessibilityLabel("Previous hunk")
                                Button { moveHunk(1) } label: { Image(systemName: "chevron.down") }
                                    .help("Next hunk (⌘G)").accessibilityLabel("Next hunk")
                            }.disabled(model.reading || model.patchError != nil || model.diff.hunks.isEmpty)
                            if model.reading { WorkspaceLoading(title: "Loading diff…") }
                            if let error = model.patchError {
                                WorkspaceNotice(message: error, kind: .error, actionTitle: "Reload Diff", action: { model.select(model.selectedFile) })
                            }
                            if model.truncated { Text("Diff truncated at 2 MiB. Open the file for the full contents.").foregroundStyle(.orange) }
                            WorkspaceDiffView(diff: model.diff, identity: model.diffIdentity,
                                              loading: model.reading || model.patchIdentity != model.diffIdentity,
                                              jump: hunk, jumpSequence: jumpSequence, reviewState: model.reviewState)
                                .opacity(model.patchIdentity == model.diffIdentity ? 1 : 0)
                                .onChange(of: model.diffIdentity) { _, _ in hunk = nil }
                                .onChange(of: model.diff) { _, _ in hunk = nil }
                        }.padding(8).frame(minWidth: 280, maxWidth: .infinity, maxHeight: .infinity)
                    }
                }
                if changes.files.isEmpty && !model.loading && model.error == nil {
                    WorkspaceEmpty(title: "No changes", detail: "This checkout matches the selected comparison base.", symbol: "checkmark.circle")
                }
            }
            Spacer(minLength: 0)
        }.padding(12)
            .onAppear { focus = .files }
            .background(WorkspaceReviewShortcuts(handle: handleShortcut).frame(width: 0, height: 0))
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
    private func handleShortcut(_ key: String, _ modifiers: NSEvent.ModifierFlags) -> Bool {
        if modifiers == .command {
            switch key {
            case "r": if !model.loading && !model.landing { model.refresh() }
            case "e": model.openFile()
            case "g": if !model.reading && model.patchError == nil { moveHunk(1) }
            default: return false
            }
        } else if modifiers == [.command, .shift] {
            switch key {
            case "g": if !model.reading && model.patchError == nil { moveHunk(-1) }
            case "f": focus = .files
            case "e": focus = .base
            default: return false
            }
        } else { return false }
        return true
    }

    private func moveHunk(_ delta: Int) {
        let count = model.diff.hunks.count
        guard count > 0 else { return }
        hunk = hunk.map { ($0 + delta + count) % count } ?? (delta > 0 ? 0 : count - 1)
        jumpSequence += 1
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
            let host = WorkspaceReviewHost(rootView: WorkspaceChangesView(model: model))
            host.closesWindow = true
            window.contentView = host
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
