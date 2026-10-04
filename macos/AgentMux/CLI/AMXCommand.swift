import Foundation

/// The common error payload every `amx --json` failure carries
/// (`internal/cli.JSONError`).
struct AMXJSONError: Decodable, Equatable {
    struct Candidate: Decodable, Equatable {
        let id: String
        let displayName: String
        let canonicalPath: String?

        enum CodingKeys: String, CodingKey {
            case id
            case displayName = "display_name"
            case canonicalPath = "canonical_path"
        }
    }

    struct Parameter: Decodable, Equatable {
        let name: String
        let description: String?
        let required: Bool
    }

    let code: String
    let message: String
    let candidates: [Candidate]
    let parameters: [Parameter]

    init(code: String, message: String, candidates: [Candidate] = [], parameters: [Parameter] = []) {
        self.code = code
        self.message = message
        self.candidates = candidates
        self.parameters = parameters
    }

    enum CodingKeys: String, CodingKey { case code, message, candidates, parameters }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        code = try c.decode(String.self, forKey: .code)
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""
        candidates = try c.decodeIfPresent([Candidate].self, forKey: .candidates) ?? []
        parameters = try c.decodeIfPresent([Parameter].self, forKey: .parameters) ?? []
    }
}

/// Why an `amx` invocation produced no usable answer.
enum AMXCommandError: Error, Equatable {
    /// The process could not be started, or did not finish within its timeout.
    case launchFailedOrTimedOut(binary: String, timeout: TimeInterval)
    /// amx answered with the structured error envelope.
    case command(AMXJSONError, status: Int32)
    /// The envelope names a schema version this app does not read.
    case unsupportedSchema(Int)
    /// The output was not a schema-v1 envelope (an amx too old for the
    /// command, or a crash); `detail` is the tail of what it wrote.
    case undecodable(status: Int32, detail: String)

    var message: String {
        switch self {
        case let .launchFailedOrTimedOut(binary, timeout):
            return "\(binary) did not start or did not finish within \(Int(timeout))s"
        case let .command(error, _):
            return error.message.isEmpty ? error.code : error.message
        case let .unsupportedSchema(version):
            return "amx answered with schema version \(version); this app reads version \(AMXEnvelope.schemaVersion)"
        case let .undecodable(status, detail):
            return detail.isEmpty ? "amx exited \(status) without a JSON answer" : detail
        }
    }
}

/// A decoded `amx --json` answer.
enum AMXCommandResult<T> {
    case success(T)
    /// Work that completed durably before a later step failed: the data
    /// envelope with a nonzero exit (`cli.WriteJSONPartial`).
    case partial(T, status: Int32)
    case failure(AMXCommandError)

    var value: T? {
        switch self {
        case let .success(value), let .partial(value, _): return value
        case .failure: return nil
        }
    }
}

extension AMXCommandResult: Equatable where T: Equatable {}

/// The schema-v1 command envelope: `{"schema_version":1,"data":…}` on
/// success, `{"schema_version":1,"error":{…}}` on failure.
enum AMXEnvelope {
    static let schemaVersion = 1

    private struct Header: Decodable {
        let schemaVersion: Int?
        enum CodingKeys: String, CodingKey { case schemaVersion = "schema_version" }
    }

    private struct DataEnvelope<T: Decodable>: Decodable {
        let data: T
    }

    private struct ErrorEnvelope: Decodable {
        let error: AMXJSONError
    }

    /// Decodes one invocation's stdout given its exit status. Unknown fields
    /// are ignored, so additive payload growth never breaks the app.
    static func decode<T: Decodable>(_ type: T.Type, stdout: Data, stderr: Data = Data(), status: Int32) -> AMXCommandResult<T> {
        let decoder = JSONDecoder()
        guard let document = firstDocument(in: stdout),
              let object = try? JSONSerialization.jsonObject(with: document) as? [String: Any],
              let header = try? decoder.decode(Header.self, from: document) else {
            return .failure(.undecodable(status: status, detail: tail(stderr.isEmpty ? stdout : stderr)))
        }
        guard header.schemaVersion == schemaVersion else {
            return .failure(.unsupportedSchema(header.schemaVersion ?? 0))
        }
        if object["error"] != nil {
            guard let envelope = try? decoder.decode(ErrorEnvelope.self, from: document) else {
                return .failure(.undecodable(status: status, detail: "amx answered with a malformed error envelope"))
            }
            // A failure envelope always means failure, whatever the exit status.
            return .failure(.command(envelope.error, status: status == 0 ? 1 : status))
        }
        guard object.keys.contains("data") else {
            return .failure(.undecodable(status: status, detail: "amx answered with neither data nor error"))
        }
        do {
            let envelope = try decoder.decode(DataEnvelope<T>.self, from: document)
            return status == 0 ? .success(envelope.data) : .partial(envelope.data, status: status)
        } catch {
            return .failure(.undecodable(status: status, detail: "amx data did not decode: \(error)"))
        }
    }

    /// The whole output when it is one document, else its first non-empty line.
    private static func firstDocument(in data: Data) -> Data? {
        let text = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return nil }
        if (try? JSONSerialization.jsonObject(with: Data(text.utf8))) != nil {
            return Data(text.utf8)
        }
        return text.split(whereSeparator: \.isNewline).first.map { Data($0.utf8) }
    }

    private static func tail(_ data: Data) -> String {
        let text = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        let last = text.split(whereSeparator: \.isNewline).suffix(3).joined(separator: "\n")
        return String(last.suffix(512))
    }
}

/// Runs the resolved `amx` with the login-shell environment.
///
/// The app runs outside Kitty, and amx learns which Kitty to talk to only from
/// `KITTY_LISTEN_ON`, so every invocation names it explicitly: inherited
/// `KITTY_*` variables are always stripped (an app started from a Kitty shell
/// would otherwise aim every command at that one window), and
/// `KITTY_LISTEN_ON` is set only from the caller's argument.
struct AMXCommand {
    typealias Runner = (_ executable: String, _ arguments: [String], _ environment: [String: String], _ timeout: TimeInterval) -> ProcessOutput?

    static let defaultTimeout: TimeInterval = 10

    var binary: URL
    var launchEnvironment: LaunchEnvironment
    var inheritedEnvironment: [String: String] = ProcessInfo.processInfo.environment
    var runner: Runner = { ProcessOutput.run($0, arguments: $1, environment: $2, timeout: $3) }

    /// The child's whole environment.
    static func childEnvironment(
        inherited: [String: String],
        launch: LaunchEnvironment,
        kittyListenOn: String?
    ) -> [String: String] {
        var environment = inherited.filter { !$0.key.hasPrefix("KITTY_") }
        environment.merge(launch.commandEnvironment) { _, launched in launched }
        if let kittyListenOn, !kittyListenOn.isEmpty {
            environment["KITTY_LISTEN_ON"] = kittyListenOn
        }
        return environment
    }

    func environment(kittyListenOn: String?) -> [String: String] {
        Self.childEnvironment(inherited: inheritedEnvironment, launch: launchEnvironment, kittyListenOn: kittyListenOn)
    }

    /// One run, undecoded.
    func run(_ arguments: [String], kittyListenOn: String? = nil, timeout: TimeInterval = defaultTimeout) -> Result<ProcessOutput, AMXCommandError> {
        guard let output = runner(binary.path, arguments, environment(kittyListenOn: kittyListenOn), timeout) else {
            return .failure(.launchFailedOrTimedOut(binary: binary.path, timeout: timeout))
        }
        return .success(output)
    }

    /// One `--json` run decoded through the schema-v1 envelope. `--json` is
    /// appended when `arguments` does not already carry it.
    func json<T: Decodable>(
        _ type: T.Type,
        _ arguments: [String],
        kittyListenOn: String? = nil,
        timeout: TimeInterval = defaultTimeout
    ) -> AMXCommandResult<T> {
        let full = arguments.contains("--json") ? arguments : arguments + ["--json"]
        switch run(full, kittyListenOn: kittyListenOn, timeout: timeout) {
        case let .failure(error):
            return .failure(error)
        case let .success(output):
            return AMXEnvelope.decode(type, stdout: output.stdout, stderr: output.stderr, status: output.status)
        }
    }

    /// `amx <path> --help`, parsed; nil when it fails, which an amx that does
    /// not know `path` does.
    func help(_ path: [String] = [], timeout: TimeInterval = 3) -> AMXHelp? {
        guard case let .success(output) = run(path + ["--help"], timeout: timeout), output.status == 0 else {
            return nil
        }
        return AMXHelp(String(decoding: output.stdout, as: UTF8.self))
    }
}
