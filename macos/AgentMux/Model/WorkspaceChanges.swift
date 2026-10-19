import Foundation
import Combine

struct WorkspaceChanges: Decodable, Equatable {
    struct File: Decodable, Equatable, Identifiable {
        let path: String
        let added: Int
        let deleted: Int
        let binary: Bool
        let untracked: Bool
        var id: String { path }
    }
    let root: String
    let base: String
    let files: [File]
    let patch: String?
    let truncated: Bool
}

struct WorkspaceManagedTrees: Decodable {
    struct Tree: Decodable { let id: String; let path: String }
    let worktrees: [Tree]
    let warnings: [String]?
}

struct WorkspaceWorktreeReview: Decodable, Equatable {
    struct Review: Decodable, Equatable {
        let id: String
        let path: String
        let branch: String?
        let sourceBranch: String
        let mergeBase: String
        let ahead: Int
        let behind: Int
        let dirty: Bool
        let lands: String
        let warnings: [String]?
        let conflicts: [String]?
        enum CodingKeys: String, CodingKey {
            case id, path, branch, ahead, behind, dirty, lands, warnings, conflicts
            case sourceBranch = "source_branch"
            case mergeBase = "merge_base"
        }
    }
    let review: Review
}

struct WorkspaceLandResult: Decodable {
    let changed: Bool
    let message: String?
}

@MainActor
final class WorkspaceChangesModel: ObservableObject {
    @Published var layout: WorkspaceReviewState.SidebarLayout { didSet { reviewState.changesLayout = layout } }
    let feedbackHistory: WorkspaceFeedbackHistory
    @Published private(set) var comparisonRefreshedAt: Date?
    @Published private(set) var diffRefreshedAt: Date?
    @Published var base = "HEAD"
    let readingPreferences: WorkspaceReadingPreferences
    private var appearanceSubscription: AnyCancellable?
    var diffFontSize: Double {
        get { readingPreferences.effectiveFontSize }
        set { readingPreferences.fontSize = newValue }
    }
    var wrapDiffLines: Bool {
        get { readingPreferences.wrapLines }
        set { readingPreferences.wrapLines = newValue }
    }
    @Published private(set) var changes: WorkspaceChanges?
    @Published private(set) var selectedFile: String?
    @Published private(set) var patch = SecretText()
    @Published private(set) var diff = WorkspaceDiff("")
    @Published private(set) var patchIdentity: String?
    @Published private(set) var truncated = false
    @Published private(set) var loading = false
    @Published private(set) var reading = false
    @Published private(set) var landing = false
    @Published private(set) var reviewing = false
    @Published private(set) var error: String?
    @Published private(set) var patchError: String?
    @Published private(set) var worktreeError: String?
    @Published private(set) var landFeedback: WorkspaceFeedback? {
        didSet {
            guard let landFeedback else { return }
            let reveal = onFeedbackRecovery
            feedbackHistory.record(landFeedback, available: { [weak self] in reveal != nil || (self != nil && self?.landing == false) }) { [weak self] in
                if let reveal { reveal(landFeedback.target) }
                else { self?.open(directory: landFeedback.target) }
            }
        }
    }
    var landNotice: String? { landFeedback?.message }
    @Published private(set) var worktree: WorkspaceWorktreeReview.Review?
    var onFeedbackRecovery: ((String) -> Void)?
    var onOpenFile: ((String) -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var target: [String] = []
    private var generation = 0
    private var patchGeneration = 0
    private var comparisonCancellation: ProcessCancellation?
    private var patchCancellation: ProcessCancellation?
    private var reviewCancellation: ProcessCancellation?
    let reviewState: WorkspaceReviewState
    private var originalTarget: [String] = []

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent(), defaults: UserDefaults? = nil, feedbackHistory: WorkspaceFeedbackHistory? = nil) {
        self.feedbackHistory = feedbackHistory ?? WorkspaceFeedbackHistory()
        self.command = command
        self.runner = runner
        reviewState = WorkspaceReviewState(defaults: defaults)
        layout = reviewState.changesLayout
        readingPreferences = defaults === UserDefaults.standard ? .shared : WorkspaceReadingPreferences(defaults: defaults)
        appearanceSubscription = readingPreferences.objectWillChange.sink { [weak self] in self?.objectWillChange.send() }
    }

    var landingReason: String? {
        guard let worktree else { return "No managed worktree review is available." }
        if loading || reviewing || landing { return "Wait for the current operation to finish." }
        if let error { return error }
        if let worktreeError { return worktreeError }
        if worktree.dirty { return "Commit or discard the worktree’s uncommitted changes before landing." }
        if worktree.lands != "fast_forward" { return "Landing requires a fast-forward; current result: \(worktree.lands)." }
        return nil
    }
    var worktreeSummary: String {
        guard let worktree else { return "Worktree details" }
        let conflicts = worktree.conflicts?.count ?? 0, warnings = worktree.warnings?.count ?? 0
        if conflicts + warnings > 0 { return "\(conflicts) conflicts · \(warnings) warnings…" }
        return WorkspaceWording.status(worktree.lands) + "…"
    }
    var canLand: Bool { landingReason == nil }
    var diffIdentity: String { [changes?.root ?? "", changes?.base ?? "", selectedFile ?? ""].joined(separator: "\u{0}") }

    func open(runtimeSessionID: String) {
        guard !landing else { return }
        target = ["--runtime-session", runtimeSessionID]
        changes = nil
        comparisonRefreshedAt = nil
        diffRefreshedAt = nil
        patchIdentity = nil
        worktree = nil
        landFeedback = nil
        restoreComparison()
        refresh()
    }

    func open(directory: String) {
        guard !landing else { return }
        target = ["--directory", directory]
        changes = nil
        comparisonRefreshedAt = nil
        diffRefreshedAt = nil
        patchIdentity = nil
        worktree = nil
        landFeedback = nil
        restoreComparison()
        refresh()
    }

    private func restoreComparison() {
        originalTarget = target
        let saved = reviewState.comparison(target)
        base = saved?.base ?? "HEAD"
        selectedFile = saved?.file
    }

    private func saveComparison() {
        let value = WorkspaceReviewState.Comparison(base: base, file: selectedFile)
        reviewState.saveComparison(value, target: target)
        reviewState.saveComparison(value, target: originalTarget)
    }

    func refresh() {
        guard !landing else { return }
        cancelReads()
        generation += 1
        patchGeneration += 1
        let generation = generation
        loading = false
        reading = false
        patch = SecretText()
        error = nil
        reviewing = true
        worktreeError = nil
        guard !target.isEmpty else { reviewing = false; error = "Select a checkout to compare."; return }
        guard let command = command() else { reviewing = false; error = "amx is not available. Check Settings and retry."; return }
        loading = true
        let cancellation = ProcessCancellation()
        comparisonCancellation = cancellation
        let args = ["session", "changes", "--json", "--base", base] + target
        runner.run({ command.json(WorkspaceChanges.self, args, timeout: 30, cancellation: cancellation) }) { [weak self] result in
            guard let self, self.generation == generation else { return }
            self.loading = false
            switch result {
            case let .failure(error): self.reviewing = false; self.error = error.message
            case let .success(value), let .partial(value, _):
                self.comparisonRefreshedAt = Date()
                self.changes = value
                // Keep the visible review anchored to this checkout even if
                // the agent changes its working directory later.
                self.target = ["--directory", value.root]
                self.select(value.files.contains { $0.path == self.selectedFile } ? self.selectedFile : value.files.first?.path)
                self.loadManagedReview(root: value.root, command: command, generation: generation)
            }
        }
    }

    func select(_ file: String?) {
        patchCancellation?.cancel()
        patchGeneration += 1
        let sequence = patchGeneration
        if selectedFile != file { diffRefreshedAt = nil }
        selectedFile = file
        saveComparison()
        patch = SecretText()
        patchError = nil
        truncated = false
        reading = false
        guard let file, let changes else { return }
        guard let command = command() else { patchError = "amx is not available. Check Settings and retry."; return }
        reading = true
        let cancellation = ProcessCancellation()
        patchCancellation = cancellation
        let args = ["session", "changes", "--json", "--directory", changes.root, "--base", changes.base, "--file", file]
        // Parsing and word matching run with the read, off the UI thread.
        runner.run({ () -> AMXCommandResult<(WorkspaceChanges, WorkspaceDiff)> in
            let result = command.json(WorkspaceChanges.self, args, timeout: 30, cancellation: cancellation)
            guard !cancellation.isCancelled else { return .failure(.cancelled) }
            return result.map { ($0, WorkspaceDiff($0.patch ?? "No text diff is available.")) }
        }) { [weak self] result in
            guard let self, self.patchGeneration == sequence else { return }
            self.reading = false
            switch result {
            case let .failure(error): self.patchError = error.message
            case let .success((value, diff)), let .partial((value, diff), _):
                self.diffRefreshedAt = Date()
                self.patch = SecretText(value.patch ?? "No text diff is available.")
                self.diff = diff
                self.patchIdentity = self.diffIdentity
                self.truncated = value.truncated
            }
        }
    }

    private func loadManagedReview(root: String, command: AMXCommand, generation: Int) {
        reviewCancellation?.cancel()
        let cancellation = ProcessCancellation()
        reviewCancellation = cancellation
        runner.run({ () -> Result<WorkspaceWorktreeReview?, AMXCommandError> in
            switch command.json(WorkspaceManagedTrees.self, ["worktree", "list"], timeout: 20, cancellation: cancellation).result {
            case let .failure(error): return .failure(error)
            case let .success(list):
                let root = URL(fileURLWithPath: root).resolvingSymlinksInPath().standardizedFileURL.path
                guard let tree = list.worktrees.first(where: { URL(fileURLWithPath: $0.path).resolvingSymlinksInPath().standardizedFileURL.path == root }) else { return .success(nil) }
                return command.json(WorkspaceWorktreeReview.self, ["worktree", "diff", "--json", "--", tree.id], timeout: 30, cancellation: cancellation).result.map(Optional.some)
            }
        }) { [weak self] result in
            guard let self, self.generation == generation else { return }
            self.reviewing = false
            switch result {
            case let .failure(error): self.worktreeError = error.message
            case let .success(value): self.worktree = value?.review
            }
        }
    }

    func compareWithSource() {
        guard let worktree else { return }
        base = worktree.mergeBase
        refresh()
    }

    /// Called only after the view's explicit confirmation. The CLI verifies
    /// clean/inactive/fast-forward conditions again at execution time.
    func land(target: WorkspaceWorktreeReview.Review? = nil) {
        if let target, target != worktree {
            landFeedback = WorkspaceFeedback(target: target.path, message: "The worktree review changed. Review it again before confirming a landing.", kind: .warning)
            return
        }
        guard canLand, let worktree, let command = command() else { return }
        landing = true
        landFeedback = nil
        runner.run({ command.json(WorkspaceLandResult.self, ["worktree", "land", "--json", "--yes", "--", worktree.id], timeout: 60, mutates: true) }) { [weak self] result in
            guard let self else { return }
            self.landing = false
            switch result {
            case let .failure(error): self.landFeedback = WorkspaceFeedback(target: worktree.path, message: error.message,
                nextStep: "Refresh and inspect the source branch before trying to land again.")
            case let .success(value): self.landFeedback = WorkspaceFeedback(target: worktree.path,
                message: value.message ?? (value.changed ? "Worktree landed." : "No branch change was needed."), kind: .success, recovery: nil)
            case let .partial(value, _): self.landFeedback = WorkspaceFeedback(target: worktree.path,
                message: (value.message ?? "The branch may have moved.") + " Check the checkout before retrying.", kind: .warning,
                nextStep: "Refresh only reads the checkout; it does not repeat the landing.")
            }
            self.refresh()
        }
    }

    func openFile() {
        guard let changes, let selectedFile else { return }
        let root = URL(fileURLWithPath: changes.root).standardizedFileURL
        let url = root.appendingPathComponent(selectedFile).standardizedFileURL
        guard url.path.hasPrefix(root.path + "/"), FileManager.default.fileExists(atPath: url.path) else {
            patchError = "This file was removed or is outside the checkout. Refresh the changes."
            return
        }
        onOpenFile?(url.path)
    }

    func close() {
        cancelReads()
        generation += 1
        patchGeneration += 1
        loading = false
        reading = false
        reviewing = false
        patch = SecretText()
        diff = WorkspaceDiff("")
    }

    private func cancelReads() {
        comparisonCancellation?.cancel()
        patchCancellation?.cancel()
        reviewCancellation?.cancel()
    }
}
