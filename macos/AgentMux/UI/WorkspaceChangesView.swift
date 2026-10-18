import AppKit
import SwiftUI

/// Also embeddable beside an agent in the workspace's second pane.
struct WorkspaceChangesView: View {
    @ObservedObject var model: WorkspaceChangesModel
    private enum Focus: Hashable { case base, files }
    @FocusState private var focus: Focus?
    @State private var hunk: Int?
    @State private var visibleHunk: Int?
    @State private var showPatchHeaders = false
    @State private var jumpSequence = 0
    @State private var copyFeedback = WorkspaceCopyFeedback()
    @State private var showWorktreeDetails = false
    @State private var confirmedTree: WorkspaceWorktreeReview.Review?

    var body: some View {
        let fileLabels = WorkspaceWording.uniqueSuffixes(model.changes?.files.map(\.path) ?? [])
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("Checkout Changes").font(.headline).fixedSize()
                if let changes = model.changes {
                    Text(changes.root).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                        .truncationMode(.middle).textSelection(.enabled)
                        .help(changes.root + "\nIncludes changes from every session using this checkout.")
                }
                Spacer(minLength: 0)
                WorkspaceReviewStatus(reads: [
                    .init(title: "Comparison", loading: model.loading, retained: model.changes != nil, refreshedAt: model.comparisonRefreshedAt,
                          feedback: model.error.map { WorkspaceFeedback(target: "Comparison", message: $0) }, retry: { model.refresh() }),
                    .init(title: "Diff", loading: model.reading, retained: model.patchIdentity == model.diffIdentity,
                          refreshedAt: model.patchIdentity == model.diffIdentity ? model.diffRefreshedAt : nil,
                          feedback: diffFeedback, retry: { model.select(model.selectedFile) })
                ])
                WorkspaceStatus(feedback: model.landFeedback, recover: { model.refresh() }, compactFeedback: true).frame(width: 24)
                WorkspaceFeedbackHistoryButton(history: model.feedbackHistory)
            }.frame(height: 24)
            HStack {
                TextField("Comparison base", text: $model.base).focused($focus, equals: .base).textFieldStyle(.roundedBorder).onSubmit { model.refresh() }
                    .frame(minWidth: 90, idealWidth: 150, maxWidth: 200)
                    .help("Compare against a branch or commit. ⌘⇧E focuses this field.")
                Button("Compare") { model.refresh() }.help("Refresh comparison (⌘R)").disabled(model.loading || model.landing)
                if let changes = model.changes {
                    Text("\(changes.files.count) \(changes.files.count == 1 ? "file" : "files") · +\(changes.files.reduce(0) { $0 + $1.added }) −\(changes.files.reduce(0) { $0 + $1.deleted })")
                        .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                        .help("Compared with " + changes.base + ". Includes every session using this checkout.")
                }
                Spacer(minLength: 0)
            }
            HStack(spacing: 8) {
                if let tree = model.worktree {
                    Text("\(tree.branch ?? "Worktree") → \(tree.sourceBranch)").font(.caption).lineLimit(1).truncationMode(.middle)
                        .help("\(tree.branch ?? "Worktree") → \(tree.sourceBranch)")
                    Button { showWorktreeDetails = true } label: {
                        Text(model.worktreeSummary).lineLimit(1)
                    }.popover(isPresented: $showWorktreeDetails) {
                        ScrollView {
                            VStack(alignment: .leading, spacing: 8) {
                                WorkspacePath(path: tree.path).font(.headline)
                                Text("\(tree.ahead) ahead · \(tree.behind) behind · \(WorkspaceWording.status(tree.lands))")
                                ForEach(Array((tree.warnings ?? []).enumerated()), id: \.offset) { _, warning in Text(warning).foregroundStyle(.orange) }
                                ForEach(Array((tree.conflicts ?? []).enumerated()), id: \.offset) { _, conflict in Text("Conflict: " + conflict).foregroundStyle(.red) }
                                if let reason = model.landingReason { Text(reason).foregroundStyle(.secondary) }
                            }.textSelection(.enabled).padding(16).frame(maxWidth: .infinity, alignment: .leading)
                        }.frame(width: 400, height: 240)
                    }
                    Spacer(minLength: 0)
                    Button("Compare with source") { model.compareWithSource() }.disabled(model.loading || model.reviewing || model.landing)
                    Button("Land…") { confirmedTree = tree }.disabled(!model.canLand).help(model.landingReason ?? "Fast-forward the source branch")
                }
                if let error = model.worktreeError {
                    WorkspaceStatus(feedback: WorkspaceFeedback(target: "Managed worktree review", message: error,
                        nextStep: "Refresh the comparison to reload the worktree review."), recover: { model.refresh() })
                }
            }.frame(height: 24)
            if let changes = model.changes {
                if !changes.files.isEmpty {
                    HSplitView {
                        List(selection: Binding(get: { model.selectedFile }, set: { model.select($0) })) {
                            ForEach(changes.files) { file in
                                VStack(alignment: .leading, spacing: 3) {
                                    WorkspacePath(path: file.path, display: fileLabels[file.path])
                                    Text(file.untracked ? "Untracked" : file.binary ? "Binary" : "+\(file.added) −\(file.deleted)")
                                        .font(.caption).foregroundStyle(.secondary)
                                }.tag(file.path)
                            }
                        }.focused($focus, equals: .files).frame(minWidth: 150, idealWidth: 220, maxWidth: 240)
                            .help("Changed files (⌘⇧F)")
                        VStack(alignment: .leading, spacing: 4) {
                            HStack(spacing: 6) {
                                Text(model.selectedFile.map { fileLabels[$0] ?? $0 } ?? "Select a changed file").font(.headline).lineLimit(1).truncationMode(.head)
                                    .help(model.selectedFile ?? "")
                                Spacer(minLength: 0)
                                if !model.diff.hunks.isEmpty {
                                    Text("Hunk \(min((visibleHunk ?? 0) + 1, model.diff.hunks.count)) of \(model.diff.hunks.count)")
                                        .font(.caption).foregroundStyle(.secondary).fixedSize()
                                }
                                Button { moveHunk(-1) } label: { Image(systemName: "chevron.up") }
                                    .help("Previous hunk (⌘⇧G)").accessibilityLabel("Previous hunk")
                                    .disabled(model.reading || model.patchError != nil || model.diff.hunks.isEmpty)
                                Button { moveHunk(1) } label: { Image(systemName: "chevron.down") }
                                    .help("Next hunk (⌘G); \(model.diff.hunks.count) hunks").accessibilityLabel("Next hunk")
                                    .disabled(model.reading || model.patchError != nil || model.diff.hunks.isEmpty)
                                Button { model.openFile() } label: { Image(systemName: "square.and.pencil") }
                                    .help("Open in editor (⌘E)").accessibilityLabel("Open in editor").disabled(model.selectedFile == nil)
                                Menu {
                                    Toggle("Wrap lines", isOn: $model.wrapDiffLines)
                                    Toggle("Show patch headers", isOn: $showPatchHeaders)
                                    Button("Larger text") { model.diffFontSize = min(24, model.diffFontSize + 1) }.disabled(model.diffFontSize >= 24)
                                    Button("Smaller text") { model.diffFontSize = max(10, model.diffFontSize - 1) }.disabled(model.diffFontSize <= 10)
                                    Button("Reset text size") { model.diffFontSize = 12 }
                                    Divider()
                                    Button("Copy unified patch") { copy(model.diff.patch) }
                                        .disabled(model.reading || model.patchIdentity != model.diffIdentity || model.patchError != nil)
                                    Button("Copy changed source lines") {
                                        copy(model.diff.lines.filter { $0.kind == .context || $0.kind == .added }.map { String($0.text.dropFirst()) }.joined(separator: "\n"))
                                    }.disabled(model.reading || model.patchIdentity != model.diffIdentity || model.patchError != nil)
                                } label: { Image(systemName: "ellipsis.circle") }
                                    .menuStyle(.borderlessButton).fixedSize().help("Diff display and copy options")
                                    .accessibilityLabel("Diff display and copy options")
                            }
                            WorkspaceDiffView(diff: model.diff, identity: model.diffIdentity,
                                              loading: model.reading || model.patchIdentity != model.diffIdentity,
                                              jump: hunk, jumpSequence: jumpSequence, reviewState: model.reviewState,
                                              fontSize: model.diffFontSize, wrapLines: model.wrapDiffLines,
                                              compactMetadata: !showPatchHeaders, onVisibleHunk: { visibleHunk = $0 },
                                              onCopy: { copyFeedback = $0 })
                                .opacity(model.patchIdentity == model.diffIdentity ? 1 : 0)
                                .onChange(of: model.diffIdentity) { _, _ in hunk = nil; visibleHunk = nil; copyFeedback = WorkspaceCopyFeedback() }
                                .onChange(of: model.diff) { _, _ in hunk = nil; visibleHunk = nil; copyFeedback = WorkspaceCopyFeedback() }
                        }.padding(4).frame(minWidth: 280, maxWidth: .infinity, maxHeight: .infinity)
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
    private var diffFeedback: WorkspaceFeedback? {
        let target = model.selectedFile ?? "Diff"
        if let error = model.patchError { return WorkspaceFeedback(target: target, message: error, nextStep: "Reload the diff, or refresh the comparison if the file changed.") }
        if let message = copyFeedback.message {
            return WorkspaceFeedback(target: target, message: message, kind: copyFeedback.succeeded ? .success : .error,
                nextStep: model.truncated ? "The displayed diff is truncated at 2 MiB. Open the file for the full contents." : "", recovery: nil)
        }
        if model.truncated { return WorkspaceFeedback(target: target, message: "Diff truncated at 2 MiB.", kind: .warning,
            nextStep: "Open the file in your editor for the full contents.", recovery: nil) }
        return nil
    }
    private func copy(_ text: String) { copyFeedback.copy(text) }

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
