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

struct LaunchProfileChoice: Decodable, Equatable {
    let name: String
    let harness: String
}

@MainActor
final class WorkspaceRunPlanModel: ObservableObject {
    let feedbackHistory: WorkspaceFeedbackHistory
    var onFeedbackRuns: (() -> Void)?
    var revealFeedback: (() -> Void)?
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
    @Published private(set) var feedback: WorkspaceFeedback? {
        didSet {
            guard let feedback else { return }
            let saved = (feedback.target, project, profile, harness, reviewHarness, commitMode)
            let destination: WorkspaceFeedbackDestination = feedback.recovery == .runs
                ? .runs(rowID: nil, runID: nil)
                : .plan(.init(plan: saved.0, project: saved.1, profile: saved.2, harness: saved.3, reviewHarness: saved.4, commitMode: saved.5))
            feedbackHistory.record(feedback, destination: destination, available: { [weak self] in
                guard let self else { return false }
                return !self.launching && !self.checking
            }) { [weak self] in
                guard let self, !self.launching, !self.checking else { return }
                if feedback.recovery == .runs { self.onFeedbackRuns?() }
                else {
                    (self.plan, self.project, self.profile, self.harness, self.reviewHarness, self.commitMode) = saved
                    self.revealFeedback?()
                    self.isPresented = true
                    self.check()
                }
            }
        }
    }
    var notice: String? { feedback?.message }
    @Published private(set) var checkedArguments: [String]?
    @Published private(set) var submitted: Set<String> = []
    @Published private(set) var projects: [ProjectEntry] = []
    @Published private(set) var profiles: [LaunchProfileChoice] = []
    @Published private(set) var loadingChoices = false
    @Published private(set) var choicesLoaded = false
    @Published private(set) var choicesError: String?
    var onStarted: ((String) -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent(), feedbackHistory: WorkspaceFeedbackHistory? = nil) {
        self.feedbackHistory = feedbackHistory ?? WorkspaceFeedbackHistory()
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
            && !(preview?.project.isEmpty ?? true) && !(preview?.profile.isEmpty ?? true)
            && projectProblem == nil && profileProblem == nil
    }

    var selectedProject: ProjectEntry? {
        projects.first { $0.profile + "/" + $0.title == project }
    }
    var projectProblem: String? {
        guard choicesLoaded, !project.isEmpty else { return nil }
        guard let selectedProject else { return "Choose a configured project from the list." }
        if let problem = selectedProject.directoryProblem { return "Project directory \(problem): \(selectedProject.directory)" }
        return nil
    }
    var profileProblem: String? {
        guard choicesLoaded, !profile.isEmpty else { return nil }
        return profiles.contains { $0.name == profile } ? nil : "Choose a configured supervisor profile from the list."
    }
    var projectLabel: String {
        if let selectedProject { return "\(selectedProject.title) · \(selectedProject.profile)" }
        if project.isEmpty, !optionsChanged, let preview, !preview.project.isEmpty { return "\(preview.project) (inferred)" }
        return project.isEmpty ? "Infer from plan workspace" : project
    }
    var profileLabel: String {
        if !profile.isEmpty { return profile }
        if let selectedProject { return "\(selectedProject.profile) (project default)" }
        if !optionsChanged, let preview, !preview.profile.isEmpty { return "\(preview.profile) (inferred)" }
        return "Use project default"
    }
    var destinationSummary: String { "Launch in workspace (tmux) · supervisor: \(profileLabel)" }

    func loadChoices() {
        guard !loadingChoices else { return }
        guard let command = command() else { choicesError = AMXWording.current; return }
        loadingChoices = true
        runner.run({
            (ProjectCatalog.fetch(command), command.json([LaunchProfileChoice].self, ["config", "profiles"]).result)
        }) { [weak self] projects, profiles in
            guard let self else { return }
            self.loadingChoices = false
            switch (projects, profiles) {
            case let (.success(projects), .success(profiles)):
                self.projects = projects; self.profiles = profiles
                self.choicesLoaded = true; self.choicesError = nil
            case let (.failure(error), _), let (_, .failure(error)):
                self.choicesError = error.message
            }
        }
    }

    func check() {
        guard !checking, !launching, !selector.isEmpty, projectProblem == nil, profileProblem == nil else { return }
        guard let command = command() else { feedback = WorkspaceFeedback(target: selector, message: AMXWording.current, nextStep: "Check readiness again once amx is found.", recovery: .readiness); return }
        let args = arguments
        checking = true; feedback = nil; preview = nil; checkedArguments = nil
        var checkArgs = args
        checkArgs.insert("--check", at: 2)
        runner.run({ command.json(WorkspacePlanRunResult.self, checkArgs, timeout: 120) }) { [weak self] result in
            guard let self else { return }
            self.checking = false
            guard self.arguments == args else { return }
            switch result {
            case let .success(value):
                self.preview = value; self.checkedArguments = value.started ? nil : args
                self.feedback = value.message.map { WorkspaceFeedback(target: self.selector, message: $0, kind: value.readiness.state == "ready" ? .information : .warning, recovery: .readiness) }
            case let .partial(value, _):
                self.preview = value
                self.checkedArguments = value.readiness.state == "ready" ? nil : args
                self.feedback = WorkspaceFeedback(target: self.selector, message: value.message ?? "The readiness check did not finish successfully.", kind: .warning, nextStep: "Address the findings, then check readiness again.", recovery: .readiness)
            case let .failure(error): self.feedback = WorkspaceFeedback(target: self.selector, message: error.message, nextStep: "Check the plan directory and Settings, then check readiness again.", recovery: .readiness)
            }
        }
    }

    func start() {
        guard canStart, let command = command() else { return }
        let args = arguments
        let plan = selector
        launching = true; feedback = nil
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
                    self.feedback = WorkspaceFeedback(target: plan, message: "Supervising session started. The run will appear after ASL initializes.", kind: .success, recovery: .runs)
                    if let id = value.runtimeSessionID, WorkspaceUtilityView.isRuntimeSessionID(id) { self.onStarted?(id) }
                } else {
                    self.submitted.remove(plan)
                    self.feedback = WorkspaceFeedback(target: plan, message: value.message ?? "The plan was not started: \(value.readiness.state).", kind: .warning, nextStep: "Address the readiness findings before starting again.", recovery: .readiness)
                }
            case let .failure(error):
                self.feedback = WorkspaceFeedback(target: plan, message: error.message + " Check the workspace and Runs before attempting another launch of this plan.", kind: .warning, nextStep: "Opening Runs does not repeat the launch.", recovery: .runs)
            }
        }
    }
}
