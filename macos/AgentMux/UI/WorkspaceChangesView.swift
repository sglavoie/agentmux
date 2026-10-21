import AppKit
import SwiftUI

/// Also embeddable beside an agent in the workspace's second pane.
struct WorkspaceChangesView: View {
    @ObservedObject var model: WorkspaceChangesModel
    private enum Focus: Hashable { case base, files }
    @FocusState private var focus: Focus?
    @State private var diffFocusRequest: UUID?
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
                Button(action: toggleFiles) { Image(systemName: "sidebar.left") }
                    .help("Show or hide changed files (⌘⌥S)")
                    .accessibilityLabel(model.layout.collapsed ? "Show changed files" : "Hide changed files")
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
            if model.worktree != nil || model.worktreeError != nil {
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
                                ForEach(Array((tree.warnings ?? []).enumerated()), id: \.offset) { _, warning in Text(warning).foregroundStyle(WorkspaceReviewColors.warning) }
                                ForEach(Array((tree.conflicts ?? []).enumerated()), id: \.offset) { _, conflict in Text("Conflict: " + conflict).foregroundStyle(WorkspaceReviewColors.failure) }
                                if let reason = model.landingReason { Text(reason).foregroundStyle(.secondary) }
                            }.textSelection(.enabled).padding(16).frame(maxWidth: .infinity, alignment: .leading)
                        }.frame(width: 400, height: 240)
                    }
                    Spacer(minLength: 0)
                    Button("Compare with Source") { model.compareWithSource() }.disabled(model.loading || model.reviewing || model.landing)
                    Button("Land…") { confirmedTree = tree }.disabled(!model.canLand).help(model.landingReason ?? "Fast-forward the source branch")
                }
                if let error = model.worktreeError {
                    WorkspaceStatus(feedback: WorkspaceFeedback(target: "Managed worktree review", message: error,
                        nextStep: "Refresh the comparison to reload the worktree review."), recover: { model.refresh() })
                }
            }.frame(height: 24)
            }
            WorkspaceReadFailure(title: "Comparison", error: model.error, retained: model.changes != nil,
                                 loading: model.loading, retry: { model.refresh() })
            if let changes = model.changes {
                if !changes.files.isEmpty {
                    WorkspaceReviewSplit(layout: $model.layout, title: "Changed files") {
                        List(selection: Binding(get: { model.selectedFile }, set: { model.select($0) })) {
                            ForEach(changes.files) { file in
                                VStack(alignment: .leading, spacing: 3) {
                                    WorkspacePath(path: file.path, display: fileLabels[file.path])
                                    Text(file.untracked ? "Untracked" : file.binary ? "Binary" : "+\(file.added) −\(file.deleted)")
                                        .font(.caption).foregroundStyle(.secondary)
                                }.tag(file.path)
                            }
                        }.focused($focus, equals: .files)
                            .help("Changed files (⌘⇧F)")
                    } detail: {
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
                                    Toggle("Show diff headers", isOn: $showPatchHeaders)
                                    Button("Larger Text") { model.diffFontSize = min(24, model.diffFontSize + 1) }.disabled(model.diffFontSize >= 24)
                                    Button("Smaller Text") { model.diffFontSize = max(10, model.diffFontSize - 1) }.disabled(model.diffFontSize <= 10)
                                    Button("Reset Text Size") { model.diffFontSize = 12 }
                                    Divider()
                                    Button("Copy Unified Diff") { copy(model.diff.patch) }
                                        .disabled(model.reading || model.patchIdentity != model.diffIdentity || model.patchError != nil)
                                    Button("Copy Changed Source Lines") {
                                        copy(model.diff.lines.filter { $0.kind == .context || $0.kind == .added }.map { String($0.text.dropFirst()) }.joined(separator: "\n"))
                                    }.disabled(model.reading || model.patchIdentity != model.diffIdentity || model.patchError != nil)
                                } label: { Image(systemName: "ellipsis.circle") }
                                    .menuStyle(.borderlessButton).fixedSize().help("Diff display and copy options")
                                    .accessibilityLabel("Diff display and copy options")
                            }
                            WorkspaceReadFailure(title: "Diff", error: model.patchError, retained: model.patchIdentity == model.diffIdentity,
                                                 loading: model.reading, retry: { model.select(model.selectedFile) })
                            WorkspaceDiffView(diff: model.diff, identity: model.diffIdentity,
                                              loading: model.reading || model.patchIdentity != model.diffIdentity,
                                              jump: hunk, jumpSequence: jumpSequence, reviewState: model.reviewState,
                                              fontSize: model.diffFontSize, wrapLines: model.wrapDiffLines,
                                              compactMetadata: !showPatchHeaders, focusRequest: diffFocusRequest, onVisibleHunk: {
                                                  visibleHunk = $0
                                                  hunk = $0
                                              },
                                              onCopy: { copyFeedback = $0 })
                                .opacity(model.patchIdentity == model.diffIdentity ? 1 : 0)
                                .overlay { diffEmptyState }
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
            .onAppear {
                if model.layout.collapsed { diffFocusRequest = UUID() }
                else { focus = .files }
            }
            .onChange(of: focus) { _, value in if value != nil { diffFocusRequest = nil } }
            .task(id: copyFeedback.revision) {
                guard copyFeedback.succeeded else { return }
                do { try await Task.sleep(for: WorkspaceCopyNotice.duration) } catch { return }
                guard !Task.isCancelled else { return }
                copyFeedback = WorkspaceCopyFeedback()
            }
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
    @ViewBuilder private var diffEmptyState: some View {
        if model.selectedFile == nil {
            WorkspaceEmpty(title: "No file selected", detail: "Select a changed file to review its diff.", symbol: "doc.text.magnifyingglass")
        } else if !model.reading && model.patchError == nil && model.patchIdentity == model.diffIdentity && model.diff.lines.isEmpty {
            WorkspaceEmpty(title: "No line changes", detail: "This file changed without a textual diff, such as a mode change or a binary file.", symbol: "doc")
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
    private func toggleFiles() {
        model.layout.collapsed.toggle()
        if model.layout.collapsed { focus = nil; diffFocusRequest = UUID() }
        else { diffFocusRequest = nil; focus = .files }
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
            case "f": model.layout.collapsed = false; focus = .files
            case "e": focus = .base
            default: return false
            }
        } else if modifiers == [.command, .option], key == "s" { toggleFiles() }
        else { return false }
        return true
    }

    private func moveHunk(_ delta: Int) {
        let count = model.diff.hunks.count
        guard count > 0 else { return }
        // Viewport changes update hunk above. Keep explicit steps too: the next
        // hunk may already fit on screen and therefore cause no scroll event.
        hunk = hunk.map { ($0 + delta + count) % count } ?? (delta > 0 ? 0 : count - 1)
        jumpSequence += 1
    }
}

@MainActor
final class WorkspaceChangesWindow: ReviewWindowController {
    let model: WorkspaceChangesModel
    init(model: WorkspaceChangesModel) {
        self.model = model
        super.init(title: "Checkout Changes", autosaveName: "WorkspaceChanges", size: NSSize(width: 1050, height: 700))
    }

    func show() { present() }
    override func makeContentView() -> NSView { Self.host(WorkspaceChangesView(model: model)) }
    /// Reshowing reads the checkout again, unless `open` already is.
    override func willPresent() { refreshIfIdle(olderThan: 0) }
    /// An agent edits files while the operator is in another app: coming
    /// back compares again rather than showing the old diff as current.
    override func applicationDidBecomeActive() { refreshIfIdle(olderThan: Self.reactivationInterval) }
    override var canClose: Bool { !model.landing }
    override func didClose() { model.close() }

    /// Reactivation within this many seconds of the last comparison (the
    /// activation `present` itself causes) reads nothing again.
    static let reactivationInterval: TimeInterval = 2

    private func refreshIfIdle(olderThan interval: TimeInterval) {
        guard !model.loading, !model.landing, let refreshed = model.comparisonRefreshedAt,
              Date().timeIntervalSince(refreshed) >= interval else { return }
        model.refresh()
    }
}
