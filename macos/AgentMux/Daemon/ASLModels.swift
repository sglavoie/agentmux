import Foundation

// The ASL request surface (`asl_snapshot`, `asl_question`, `asl_action`) as
// the daemon carries it: `protocol.ASLRequest` out, `protocol.ASLResponse`
// back, with the `aslbridge` row, body, and receipt types inside. Decoding is
// lenient like the rest of the wire, and nothing here holds a question body or
// an answer as a plain `String`.

/// Operator-authored or question prose. It encodes and decodes as an ordinary
/// JSON string, but every way of formatting it — `description`, `dump`,
/// string interpolation, a containing type's default description — renders a
/// fixed placeholder, so neither a question nor an answer can reach a log line
/// or a diagnostic. Mirrors `aslbridge.SecretText`.
struct SecretText: Codable, Equatable, Hashable, ExpressibleByStringLiteral,
    CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    private let value: String

    init(_ value: String = "") { self.value = value }
    init(stringLiteral value: String) { self.value = value }

    init(from decoder: Decoder) throws {
        value = (try? decoder.singleValueContainer().decode(String.self)) ?? ""
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(value)
    }

    /// The text itself. Call it only where the value is shown to the operator
    /// or is about to leave the process on the daemon socket.
    func reveal() -> String { value }

    var isEmpty: Bool { value.isEmpty }
    /// Whether nothing but whitespace was typed.
    var isBlank: Bool { value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var description: String { "[redacted]" }
    var debugDescription: String { "\"[redacted]\"" }
    var customMirror: Mirror { Mirror(self, children: [], displayStyle: .struct) }
}

/// The bridge method names (`aslbridge.Method*`) the panel invokes.
enum ASLMethod {
    static let questionReply = "question.reply"
    static let questionResolve = "question.resolve"
    static let runResume = "run.resume"
    static let runStopAfterCurrent = "run.stop_after_current"
    static let runAbort = "run.abort"
    static let workbenchEnsure = "workbench.ensure"
    static let runRedirect = "run.redirect"
}

/// `aslbridge.Resolution*`: how `question.resolve` closes a question.
enum ASLResolution {
    static let `continue` = "continue"
    static let pause = "pause"
}

/// `aslbridge.Operation*`: a receipt's status.
enum ASLOperationStatus {
    static let accepted = "accepted"
    static let consumed = "consumed"
    static let rejected = "rejected"
    static let expired = "expired"
    static let superseded = "superseded"
}

/// `protocol.ASLRequest`. Empty fields are left off the wire, as Go's
/// `omitempty` would.
struct ASLRequestParams: Encodable, Equatable, CustomStringConvertible {
    var method = ""
    var planDir = ""
    var rowID = ""
    var runID = ""
    var questionID = ""
    var operationID = ""
    var actionToken = ""
    var resolution = ""
    var note = ""
    var text = SecretText()

    enum CodingKeys: String, CodingKey {
        case method, resolution, note, text
        case planDir = "plan_dir"
        case rowID = "row_id"
        case runID = "run_id"
        case questionID = "question_id"
        case operationID = "operation_id"
        case actionToken = "action_token"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        func put(_ value: String, _ key: CodingKeys) throws {
            if !value.isEmpty { try c.encode(value, forKey: key) }
        }
        try put(method, .method)
        try put(planDir, .planDir)
        try put(rowID, .rowID)
        try put(runID, .runID)
        try put(questionID, .questionID)
        try put(operationID, .operationID)
        try put(actionToken, .actionToken)
        try put(resolution, .resolution)
        try put(note, .note)
        if !text.isEmpty { try c.encode(text, forKey: .text) }
    }

    /// Body-free: names the call, never the token or the answer.
    var description: String {
        "ASLRequestParams{method:\(method) run:\(runID) question:\(questionID) operation:\(operationID) text:\(text.isEmpty ? 0 : text.reveal().utf8.count)b}"
    }
}

/// `aslbridge.Status`: the integration's capability block, which accompanies
/// every ASL reply.
struct ASLStatus: Decodable, Equatable {
    var available = false
    var mutationsEnabled = false
    var snapshotStale = false
    var reason = ""

    init(available: Bool = false, mutationsEnabled: Bool = false, snapshotStale: Bool = false, reason: String = "") {
        self.available = available
        self.mutationsEnabled = mutationsEnabled
        self.snapshotStale = snapshotStale
        self.reason = reason
    }

    enum CodingKeys: String, CodingKey {
        case available, reason
        case mutationsEnabled = "mutations_enabled"
        case snapshotStale = "snapshot_stale"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        available = c.lenient(.available, false)
        mutationsEnabled = c.lenient(.mutationsEnabled, false)
        snapshotStale = c.lenient(.snapshotStale, false)
        reason = c.lenient(.reason, "")
    }
}

/// `aslbridge.SnapshotMeta`: where a snapshot came from.
struct ASLSnapshotMeta: Decodable, Equatable {
    var generation: UInt64 = 0
    var ageSeconds: Int64 = 0
    var stale = false

    init(generation: UInt64 = 0, ageSeconds: Int64 = 0, stale: Bool = false) {
        self.generation = generation
        self.ageSeconds = ageSeconds
        self.stale = stale
    }

    enum CodingKeys: String, CodingKey {
        case generation, stale
        case ageSeconds = "age_seconds"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        generation = c.lenient(.generation, 0)
        ageSeconds = c.lenient(.ageSeconds, 0)
        stale = c.lenient(.stale, false)
    }
}

/// `aslbridge.Question`: body-free question metadata on a row.
struct ASLRowQuestion: Decodable, Equatable {
    var contextKind = ""
    var questionID = ""
    var unansweredCount = 0
    var questionIDs: [String] = []
    var askedAt = ""

    init(contextKind: String = "", questionID: String = "", unansweredCount: Int = 0, questionIDs: [String] = [], askedAt: String = "") {
        self.contextKind = contextKind
        self.questionID = questionID
        self.unansweredCount = unansweredCount
        self.questionIDs = questionIDs
        self.askedAt = askedAt
    }

    enum CodingKeys: String, CodingKey {
        case contextKind = "context_kind"
        case questionID = "question_id"
        case unansweredCount = "unanswered_count"
        case questionIDs = "question_ids"
        case askedAt = "asked_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        contextKind = c.lenient(.contextKind, "")
        questionID = c.lenient(.questionID, "")
        unansweredCount = c.lenient(.unansweredCount, 0)
        questionIDs = c.lenient(.questionIDs, [])
        askedAt = c.lenient(.askedAt, "")
    }
}

/// `aslbridge.Row`: one projected run, with the actions ASL permits on it
/// and the token that authorizes them.
struct ASLRow: Decodable, Equatable {
    var rowID = ""
    var planDir = ""
    var label = ""
    var runID = ""
    var sessionID = ""
    var primaryState = ""
    var phase = ""
    var pauseReason = ""
    var question: ASLRowQuestion?
    var actions: [String] = []
    var disabledReasons: [String: String] = [:]
    var actionToken = ""

    init() {}

    enum CodingKeys: String, CodingKey {
        case label, phase, question, actions
        case rowID = "row_id"
        case planDir = "plan_dir"
        case runID = "run_id"
        case sessionID = "session_id"
        case primaryState = "primary_state"
        case pauseReason = "pause_reason"
        case disabledReasons = "disabled_reasons"
        case actionToken = "action_token"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        rowID = c.lenient(.rowID, "")
        planDir = c.lenient(.planDir, "")
        label = c.lenient(.label, "")
        runID = c.lenient(.runID, "")
        sessionID = c.lenient(.sessionID, "")
        primaryState = c.lenient(.primaryState, "")
        phase = c.lenient(.phase, "")
        pauseReason = c.lenient(.pauseReason, "")
        question = c.lenient(.question)
        actions = c.lenient(.actions, [])
        disabledReasons = c.lenient(.disabledReasons, [:])
        actionToken = c.lenient(.actionToken, "")
    }

    /// `asl.RowLabel`: the label, else the plan directory's name, else the
    /// row ID.
    var displayLabel: String {
        let trimmed = label.trimmingCharacters(in: .whitespaces)
        if !trimmed.isEmpty { return trimmed }
        if !planDir.isEmpty { return (planDir as NSString).lastPathComponent }
        return rowID.isEmpty ? "(unnamed run)" : rowID
    }
}

/// `aslbridge.Snapshot`, reduced to its rows.
struct ASLSnapshot: Decodable, Equatable {
    var rows: [ASLRow] = []

    init(rows: [ASLRow] = []) { self.rows = rows }

    enum CodingKeys: String, CodingKey { case rows }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        rows = c.lenient(.rows, [])
    }
}

/// `aslbridge.QuestionBody`: the deliberately fetched prose for one question.
struct ASLQuestionBody: Decodable, Equatable {
    var planDir = ""
    var rowID = ""
    var runID = ""
    var questionID = ""
    var contextKind = ""
    var body = SecretText()
    var bodyBytes = 0
    var actionToken = ""

    init(planDir: String = "", rowID: String = "", runID: String = "", questionID: String = "", contextKind: String = "",
         body: SecretText = SecretText(), bodyBytes: Int = 0, actionToken: String = "") {
        self.planDir = planDir
        self.rowID = rowID
        self.runID = runID
        self.questionID = questionID
        self.contextKind = contextKind
        self.body = body
        self.bodyBytes = bodyBytes
        self.actionToken = actionToken
    }

    enum CodingKeys: String, CodingKey {
        case body
        case planDir = "plan_dir"
        case rowID = "row_id"
        case runID = "run_id"
        case questionID = "question_id"
        case contextKind = "context_kind"
        case bodyBytes = "body_bytes"
        case actionToken = "action_token"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        planDir = c.lenient(.planDir, "")
        rowID = c.lenient(.rowID, "")
        runID = c.lenient(.runID, "")
        questionID = c.lenient(.questionID, "")
        contextKind = c.lenient(.contextKind, "")
        body = c.lenient(.body, SecretText())
        bodyBytes = c.lenient(.bodyBytes, 0)
        actionToken = c.lenient(.actionToken, "")
    }
}

/// `aslbridge.Operation`: a body-free receipt for one accepted mutation.
struct ASLOperation: Decodable, Equatable {
    var operationID = ""
    var method = ""
    var status = ""
    var questionID = ""

    init(operationID: String = "", method: String = "", status: String = "", questionID: String = "") {
        self.operationID = operationID
        self.method = method
        self.status = status
        self.questionID = questionID
    }

    enum CodingKeys: String, CodingKey {
        case method, status
        case operationID = "operation_id"
        case questionID = "question_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        operationID = c.lenient(.operationID, "")
        method = c.lenient(.method, "")
        status = c.lenient(.status, "")
        questionID = c.lenient(.questionID, "")
    }
}

/// `protocol.ASLResponse`: whichever result the request produced, plus the
/// capability block.
struct ASLResult: Decodable, Equatable {
    var status = ASLStatus()
    var snapshot: ASLSnapshot?
    var meta: ASLSnapshotMeta?
    var question: ASLQuestionBody?
    var operation: ASLOperation?

    init(status: ASLStatus = ASLStatus(), snapshot: ASLSnapshot? = nil, meta: ASLSnapshotMeta? = nil,
         question: ASLQuestionBody? = nil, operation: ASLOperation? = nil) {
        self.status = status
        self.snapshot = snapshot
        self.meta = meta
        self.question = question
        self.operation = operation
    }

    enum CodingKeys: String, CodingKey { case status, snapshot, meta, question, operation }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        status = c.lenient(.status, ASLStatus())
        snapshot = c.lenient(.snapshot)
        meta = c.lenient(.meta)
        question = c.lenient(.question)
        operation = c.lenient(.operation)
    }
}

/// Why an ASL request produced no result.
enum ASLRequestError: Error, Equatable {
    /// Nothing listens on the daemon socket, so nothing was sent.
    case notRunning
    /// The daemon predates the ASL requests.
    case unsupported
    /// The daemon answered and refused: `<bridge code>: <message>`, or a
    /// daemon-authored reason. Only the code is ever shown.
    case refused(String)
    /// The request was sent and no usable answer came back; for an action
    /// the outcome is unknown.
    case transport(String)
}
