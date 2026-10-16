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
    @Published var base = "HEAD"
    @Published private(set) var changes: WorkspaceChanges?
    @Published private(set) var selectedFile: String?
    @Published private(set) var patch = SecretText()
    @Published private(set) var diff = WorkspaceDiff("")
    @Published private(set) var patchIdentity: String?
    @Published private(set) var truncated = false
    @Published private(set) var loading = false
    @Published private(set) var reading = false
    @Published private(set) var landing = false
    @Published private(set) var error: String?
    @Published private(set) var patchError: String?
    @Published private(set) var worktreeError: String?
    @Published private(set) var landNotice: String?
    @Published private(set) var worktree: WorkspaceWorktreeReview.Review?
    var onOpenFile: ((String) -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var target: [String] = []
    private var generation = 0
    private var patchGeneration = 0

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.command = command
        self.runner = runner
    }

    var landingReason: String? {
        guard let worktree else { return "No managed worktree review is available." }
        if loading || landing { return "Wait for the current operation to finish." }
        if let error { return error }
        if worktree.dirty { return "Commit or discard the worktree’s uncommitted changes before landing." }
        if worktree.lands != "fast_forward" { return "Landing requires a fast-forward; current result: \(worktree.lands)." }
        return nil
    }
    var canLand: Bool { landingReason == nil }
    var diffIdentity: String { [changes?.root ?? "", changes?.base ?? "", selectedFile ?? ""].joined(separator: "\u{0}") }

    func open(runtimeSessionID: String) {
        guard !landing else { return }
        target = ["--runtime-session", runtimeSessionID]
        changes = nil
        base = "HEAD"
        refresh()
    }

    func open(directory: String) {
        guard !landing else { return }
        target = ["--directory", directory]
        changes = nil
        base = "HEAD"
        refresh()
    }

    func refresh() {
        guard !landing else { return }
        generation += 1
        patchGeneration += 1
        let generation = generation
        loading = false
        reading = false
        patch = SecretText()
        error = nil
        worktree = nil
        worktreeError = nil
        guard let command = command(), !target.isEmpty else { error = "Select a checkout and check that amx is available."; return }
        loading = true
        let args = ["session", "changes", "--json", "--base", base] + target
        runner.run({ command.json(WorkspaceChanges.self, args, timeout: 30) }) { [weak self] result in
            guard let self, self.generation == generation else { return }
            self.loading = false
            switch result {
            case let .failure(error): self.error = error.message
            case let .success(value), let .partial(value, _):
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
        patchGeneration += 1
        let sequence = patchGeneration
        selectedFile = file
        patch = SecretText()
        patchError = nil
        truncated = false
        reading = false
        guard let file, let changes, let command = command() else { return }
        reading = true
        let args = ["session", "changes", "--json", "--directory", changes.root, "--base", changes.base, "--file", file]
        runner.run({ command.json(WorkspaceChanges.self, args, timeout: 30) }) { [weak self] result in
            guard let self, self.patchGeneration == sequence else { return }
            self.reading = false
            switch result {
            case let .failure(error): self.patchError = error.message
            case let .success(value), let .partial(value, _):
                self.patch = SecretText(value.patch ?? "No text diff is available.")
                self.diff = WorkspaceDiff(self.patch.reveal())
                self.patchIdentity = self.diffIdentity
                self.truncated = value.truncated
            }
        }
    }

    private func loadManagedReview(root: String, command: AMXCommand, generation: Int) {
        runner.run({ () -> Result<WorkspaceWorktreeReview?, AMXCommandError> in
            switch command.json(WorkspaceManagedTrees.self, ["worktree", "list"], timeout: 20).result {
            case let .failure(error): return .failure(error)
            case let .success(list):
                let root = URL(fileURLWithPath: root).resolvingSymlinksInPath().standardizedFileURL.path
                guard let tree = list.worktrees.first(where: { URL(fileURLWithPath: $0.path).resolvingSymlinksInPath().standardizedFileURL.path == root }) else { return .success(nil) }
                return command.json(WorkspaceWorktreeReview.self, ["worktree", "diff", "--json", "--", tree.id], timeout: 30).result.map(Optional.some)
            }
        }) { [weak self] result in
            guard let self, self.generation == generation else { return }
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
            landNotice = "The worktree review changed. Review it again before confirming a landing."
            return
        }
        guard canLand, let worktree, let command = command() else { return }
        landing = true
        landNotice = nil
        runner.run({ command.json(WorkspaceLandResult.self, ["worktree", "land", "--json", "--yes", "--", worktree.id], timeout: 60, mutates: true) }) { [weak self] result in
            guard let self else { return }
            self.landing = false
            switch result {
            case let .failure(error): self.landNotice = error.message
            case let .success(value): self.landNotice = value.message ?? (value.changed ? "Worktree landed." : "No branch change was needed.")
            case let .partial(value, _): self.landNotice = (value.message ?? "The branch may have moved.") + " Check the checkout before retrying."
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
        generation += 1
        patchGeneration += 1
        loading = false
        reading = false
        patch = SecretText()
        diff = WorkspaceDiff("")
    }
}
