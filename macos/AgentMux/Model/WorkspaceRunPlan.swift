import Foundation
import Combine

struct WorkspacePlanRunResult: Decodable {
    struct Readiness: Decodable {
        struct Finding: Decodable {
            let severity: String
            let category: String
            let message: String
            let detail: String?
        }
        let state: String
        let pending: Int
        let complete: Int
        let findings: [Finding]
        enum CodingKeys: String, CodingKey {
            case state, findings
            case pending = "pending_sessions", complete = "complete_sessions"
        }
    }
    let started: Bool
    let message: String?
    let readiness: Readiness
    let command: String
    let project: String
    let profile: String
    let directory: String
    let runtimeSessionID: String?
    enum CodingKeys: String, CodingKey {
        case started, message, readiness, command, project, profile, directory
        case runtimeSessionID = "runtime_session_id"
    }
}

@MainActor
final class WorkspaceRunPlanModel: ObservableObject {
    @Published var isPresented = false
    @Published var plan = ""
    @Published var project = ""
    @Published var profile = ""
    @Published var harness = ""
    @Published var reviewHarness = ""
    @Published var commitMode = ""
    @Published private(set) var checking = false
    @Published private(set) var launching = false
    @Published private(set) var preview: WorkspacePlanRunResult?
    @Published private(set) var notice: String?
    @Published private(set) var checkedArguments: [String]?
    @Published private(set) var submitted: Set<String> = []
    var onStarted: ((String) -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.command = command; self.runner = runner
    }

    var selector: String { plan.trimmingCharacters(in: .whitespacesAndNewlines) }
    var arguments: [String] {
        var args = ["plans", "run", "--json", "--backend", "tmux"]
        for (flag, value) in [("--project", project), ("--profile", profile), ("--harness", harness), ("--review-harness", reviewHarness)] {
            let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty { args += [flag, trimmed] }
        }
        if !commitMode.isEmpty { args += ["--commit-\(commitMode)"] }
        return args + ["--", selector]
    }
    var optionsChanged: Bool { checkedArguments != nil && checkedArguments != arguments }
    var canStart: Bool {
        !checking && !launching && !selector.isEmpty && !submitted.contains(selector)
            && checkedArguments == arguments && preview?.readiness.state == "ready"
    }

    func check() {
        guard !checking, !launching, !selector.isEmpty else { return }
        guard let command = command() else { notice = "amx is not available."; return }
        let args = arguments
        checking = true; notice = nil; preview = nil; checkedArguments = nil
        var checkArgs = args
        checkArgs.insert("--check", at: 2)
        runner.run({ command.json(WorkspacePlanRunResult.self, checkArgs, timeout: 120) }) { [weak self] result in
            guard let self else { return }
            self.checking = false
            guard self.arguments == args else { return }
            switch result {
            case let .success(value):
                self.preview = value; self.checkedArguments = value.started ? nil : args
                self.notice = value.message
            case let .partial(value, _):
                self.preview = value
                self.checkedArguments = value.readiness.state == "ready" ? nil : args
                self.notice = value.message ?? "The readiness check did not finish successfully."
            case let .failure(error): self.notice = error.message
            }
        }
    }

    func start() {
        guard canStart, let command = command() else { return }
        let args = arguments
        let plan = selector
        launching = true; notice = nil
        // A slow or ambiguous launch cannot be repeated from this form. The
        // managed supervisor may exist before ASL has acquired the plan lock.
        submitted.insert(plan)
        runner.run({ command.json(WorkspacePlanRunResult.self, args, timeout: 120, mutates: true) }) { [weak self] result in
            guard let self else { return }
            self.launching = false
            switch result {
            case let .success(value), let .partial(value, _):
                self.preview = value
                if value.started {
                    self.notice = "Supervising session started. The run will appear after ASL initializes."
                    if let id = value.runtimeSessionID, WorkspaceUtilityView.isRuntimeSessionID(id) { self.onStarted?(id) }
                } else {
                    self.submitted.remove(plan)
                    self.notice = value.message ?? "The plan was not started: \(value.readiness.state)."
                }
            case let .failure(error):
                self.notice = error.message + " Check the workspace and Runs before attempting another launch of this plan."
            }
        }
    }
}
