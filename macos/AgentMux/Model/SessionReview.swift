import Foundation

/// Operator judgement stays independent of the agent's live state.
struct SessionReview: Decodable, Equatable {
    var epoch = ""
    var accounted = false
    var reviewed = false
    var note = ""
    var projectNote = ""
    init() {}
    enum CodingKeys: String, CodingKey { case epoch, accounted, reviewed, note; case projectNote = "project_note" }
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        epoch = c.lenient(.epoch, "")
        accounted = c.lenient(.accounted, false)
        reviewed = c.lenient(.reviewed, false)
        note = c.lenient(.note, "")
        projectNote = c.lenient(.projectNote, "")
    }
    var label: String { accounted ? "Accounted for" : reviewed ? "Needs review" : "" }
}

struct SessionReviewRequest: Encodable {
    var targets: [String: String] = [:]
    var project: String?
    var accounted: Bool?
    var note: String?
    var undoToken: String?
    enum CodingKeys: String, CodingKey { case targets, project, accounted, note; case undoToken = "undo_token" }
}

struct SessionReviewResult: Decodable {
    var undoToken: String?
    var count: Int
    enum CodingKeys: String, CodingKey { case count; case undoToken = "undo_token" }
}

extension DaemonClient {
    func sessionReview(_ review: SessionReviewRequest) -> Result<SessionReviewResult, Error> {
        struct Request: Encodable { let type = "session_review"; var review: SessionReviewRequest }
        struct Response: Decodable { var ok: Bool; var error: String?; var review: SessionReviewResult? }
        do {
            let reply = try roundTrip(socketPath, JSONEncoder().encode(Request(review: review)), 5)
            let response = try JSONDecoder().decode(Response.self, from: reply)
            guard response.ok, let result = response.review else {
                throw NSError(domain: "SessionReview", code: 1, userInfo: [NSLocalizedDescriptionKey: response.error ?? "The daemon did not confirm the annotation."])
            }
            return .success(result)
        } catch { return .failure(error) }
    }
}
