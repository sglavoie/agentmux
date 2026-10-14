import UserNotifications
import XCTest
@testable import AgentMux

/// A notification center that records what it was asked and answers only
/// when a test says so.
final class FakeNotificationCenter: NotificationCenterClient {
    var status: UNAuthorizationStatus = .notDetermined
    var categories: Set<UNNotificationCategory> = []
    var added: [UNNotificationRequest] = []
    var removed: [String] = []
    var permissionRequests = 0
    private var permissionCompletions: [(Bool, Error?) -> Void] = []
    private var addCompletions: [String: (Error?) -> Void] = [:]
    /// When set, `add` answers at once with this result.
    var autoAdd: Error?? = .some(nil)

    func authorizationStatus(_ completion: @escaping (UNAuthorizationStatus) -> Void) {
        completion(status)
    }

    func requestAuthorization(_ completion: @escaping (Bool, Error?) -> Void) {
        permissionRequests += 1
        permissionCompletions.append(completion)
    }

    func answerPermission(granted: Bool) {
        status = granted ? .authorized : .denied
        let pending = permissionCompletions
        permissionCompletions = []
        pending.forEach { $0(granted, nil) }
    }

    func setCategories(_ categories: Set<UNNotificationCategory>) {
        self.categories = categories
    }

    func add(_ request: UNNotificationRequest, completion: @escaping (Error?) -> Void) {
        added.append(request)
        if let result = autoAdd {
            completion(result)
        } else {
            addCompletions[request.identifier] = completion
        }
    }

    func finishAdd(_ id: String, error: Error? = nil) {
        addCompletions.removeValue(forKey: id)?(error)
    }

    func remove(identifiers: [String]) {
        removed.append(contentsOf: identifiers)
    }
}

final class NotificationPresenterTests: XCTestCase {
    struct Report: Equatable {
        let id: String
        let disposition: NotificationDisposition
        let error: String?
    }

    struct AddFailed: LocalizedError {
        var errorDescription: String? { "add failed" }
    }

    private var center: FakeNotificationCenter!
    private var reports: [Report] = []
    private var responses: [NotificationPresenter.Response] = []
    private var claims: [NotificationClaim] = []

    override func setUp() {
        super.setUp()
        center = FakeNotificationCenter()
        reports = []
        responses = []
        claims = []
    }

    private func presenter(enabled: Bool = true, status: UNAuthorizationStatus = .authorized) -> NotificationPresenter {
        center.status = status
        let presenter = NotificationPresenter(center: center, enabled: enabled, schedule: { $0() })
        presenter.report = { [unowned self] id, disposition, error in
            self.reports.append(Report(id: id, disposition: disposition, error: error))
        }
        presenter.onResponse = { [unowned self] in self.responses.append($0) }
        presenter.onClaimChange = { [unowned self] in self.claims.append($0) }
        presenter.refreshAuthorization()
        return presenter
    }

    private let stop = AppNotice(notificationID: "n-1", windowID: 7, navigable: true, title: "API", subtitle: "Stopped",
                                 action: "stop", actions: ["mute", "snooze"])

    // MARK: Categories and actions

    func testCategoriesAreRegisteredWithTheirActions() throws {
        _ = presenter()
        let byID = Dictionary(uniqueKeysWithValues: center.categories.map { ($0.identifier, $0) })
        XCTAssertEqual(Set(byID.keys), ["amx.session", "amx.session.snooze", "amx.asl", "amx.asl.snooze", "amx.prompt", "amx.plain"])
        let session = try XCTUnwrap(byID["amx.session"])
        XCTAssertEqual(session.actions.map(\.title), ["Mute session", "Snooze 1h"])
        XCTAssertEqual(session.actions.map(\.identifier), ["amx.mute", "amx.snooze"])
        XCTAssertTrue(session.options.contains(.customDismissAction), "dismissals must reach the app")
        let asl = try XCTUnwrap(byID["amx.asl"])
        XCTAssertEqual(asl.actions.first?.title, "Answer…")
        XCTAssertTrue(asl.actions.first?.options.contains(.foreground) ?? false)
        XCTAssertEqual(byID["amx.plain"]?.actions.count, 0)
        let prompt = try XCTUnwrap(byID["amx.prompt"])
        XCTAssertEqual(prompt.actions.map(\.title), ["Answer…"])
        XCTAssertEqual(prompt.actions.map(\.identifier), ["amx.respond"])
        XCTAssertTrue(prompt.actions.first?.options.contains(.foreground) ?? false)
        for category in center.categories {
            XCTAssertTrue(category.options.contains(.customDismissAction), category.identifier)
            for action in category.actions {
                XCTAssertFalse(action.options.contains(.destructive), action.identifier)
            }
        }
    }

    func testCategoryFollowsTheOffer() {
        func category(_ action: String, _ actions: [String]) -> String {
            NotificationCategories.category(for: AppNotice(notificationID: "n", action: action, actions: actions))
        }
        XCTAssertEqual(category("stop", ["mute", "snooze"]), "amx.session")
        XCTAssertEqual(category("hook_silence", ["snooze"]), "amx.session.snooze")
        XCTAssertEqual(category("elicitation", ["mute", "snooze", "answer"]), "amx.asl")
        XCTAssertEqual(category("elicitation", ["snooze", "answer"]), "amx.asl.snooze")
        XCTAssertEqual(category("stop", []), "amx.plain")
        XCTAssertEqual(category("stop", ["mute"]), "amx.plain", "never an action that was not offered")
        // Permission prompts get nothing, whatever they were offered.
        XCTAssertEqual(category("permission", ["mute", "snooze"]), "amx.plain")
        // A permission prompt may offer to show itself, and only on a session
        // the workspace embeds; no other action rides along with it.
        func permission(_ actions: [String], backend: String, runtimeSessionID: String = "rs-1") -> String {
            var notice = AppNotice(notificationID: "n", action: "permission", actions: actions)
            notice.runtimeSessionID = runtimeSessionID
            notice.backend = backend
            return NotificationCategories.category(for: notice)
        }
        XCTAssertEqual(permission(["respond"], backend: "tmux"), "amx.prompt")
        XCTAssertEqual(permission(["respond", "mute", "snooze"], backend: "tmux"), "amx.prompt")
        XCTAssertEqual(permission(["respond"], backend: "kitty"), "amx.plain")
        XCTAssertEqual(permission(["respond"], backend: "tmux", runtimeSessionID: ""), "amx.plain")
        XCTAssertEqual(permission([], backend: "tmux"), "amx.plain", "never an action that was not offered")
        XCTAssertEqual(category("stop", ["respond", "mute", "snooze"]), "amx.session", "respond is for a prompt")
    }

    func testResponseIdentifiersMapToDispositions() {
        XCTAssertEqual(NotificationCategories.disposition(forAction: UNNotificationDefaultActionIdentifier), .clicked)
        XCTAssertEqual(NotificationCategories.disposition(forAction: UNNotificationDismissActionIdentifier), .dismissed)
        XCTAssertEqual(NotificationCategories.disposition(forAction: "amx.mute"), .actionMute)
        XCTAssertEqual(NotificationCategories.disposition(forAction: "amx.snooze"), .actionSnooze)
        XCTAssertEqual(NotificationCategories.disposition(forAction: "amx.answer"), .actionAnswer)
        XCTAssertEqual(NotificationCategories.disposition(forAction: "amx.respond"), .actionRespond)
        XCTAssertNil(NotificationCategories.disposition(forAction: "other"))
        XCTAssertEqual(NotificationDisposition.actionMute.rawValue, "action_mute")
    }

    func testRequestCarriesIdentityAndThread() {
        let request = NotificationPresenter.request(for: stop)
        XCTAssertEqual(request.identifier, "n-1")
        XCTAssertEqual(request.content.threadIdentifier, "kitty:#7")
        XCTAssertEqual(request.content.title, "API")
        XCTAssertEqual(request.content.subtitle, "Stopped")
        XCTAssertEqual(request.content.categoryIdentifier, "amx.session")
        XCTAssertNil(request.trigger)
    }

    func testThreadsFollowTheRuntimeSessionThenTheKittyWindow() {
        var tmux = AppNotice(notificationID: "n-2", windowID: 0, title: "API")
        tmux.runtimeSessionID = "rs-1"
        tmux.backend = "tmux"
        XCTAssertEqual(NotificationPresenter.threadIdentifier(for: tmux), "runtime:rs-1")
        var other = tmux
        other.runtimeSessionID = "rs-2"
        XCTAssertNotEqual(NotificationPresenter.threadIdentifier(for: other), NotificationPresenter.threadIdentifier(for: tmux),
                          "two tmux sessions never share a thread")
        let kittyA = AppNotice(notificationID: "n-3", windowID: 4, kittySocket: "unix:/tmp/a")
        let kittyB = AppNotice(notificationID: "n-4", windowID: 4, kittySocket: "unix:/tmp/b")
        XCTAssertEqual(NotificationPresenter.threadIdentifier(for: kittyA), "kitty:unix:/tmp/a#4")
        XCTAssertNotEqual(NotificationPresenter.threadIdentifier(for: kittyA), NotificationPresenter.threadIdentifier(for: kittyB))
        XCTAssertEqual(NotificationPresenter.threadIdentifier(for: AppNotice(notificationID: "n-5")), "amx")
    }

    func testOnlyNoticesWaitingOnTheOperatorMakeASound() {
        for action in ["permission", "elicitation"] {
            let request = NotificationPresenter.request(for: AppNotice(notificationID: "n-\(action)", action: action))
            XCTAssertNotNil(request.content.sound, action)
        }
        XCTAssertNil(NotificationPresenter.request(for: stop).content.sound)
    }

    func testFrameDecodes() throws {
        let json = """
        {"notification_id":"n-9","window_id":3,"navigable":true,"title":"T","action":"elicitation",
         "run_id":"r1","session_id":"s1","actions":["mute","snooze","answer"],"extra":1}
        """
        let notice = try JSONDecoder().decode(AppNotice.self, from: Data(json.utf8))
        XCTAssertEqual(notice, AppNotice(notificationID: "n-9", windowID: 3, navigable: true, title: "T",
                                         action: "elicitation", runID: "r1", sessionID: "s1",
                                         actions: ["mute", "snooze", "answer"]))
    }

    // MARK: Disposition reporting

    func testAuthorizedFrameIsAddedThenReportedDelivered() {
        let presenter = presenter()
        presenter.present(stop)
        XCTAssertEqual(center.added.map(\.identifier), ["n-1"])
        XCTAssertEqual(reports, [Report(id: "n-1", disposition: .delivered, error: nil)])
        XCTAssertEqual(center.permissionRequests, 0)
    }

    func testFrameDataIsDecodedAndAMalformedOneIgnored() throws {
        let presenter = presenter()
        presenter.present(frame: Data(#"{"notification_id":"n-2","title":"x"}"#.utf8))
        presenter.present(frame: Data("not json".utf8))
        presenter.present(frame: Data(#"{"title":"no id"}"#.utf8))
        XCTAssertEqual(center.added.map(\.identifier), ["n-2"])
    }

    func testFailedAddReportsFailed() {
        let presenter = presenter()
        center.autoAdd = .some(AddFailed())
        presenter.present(stop)
        XCTAssertEqual(reports, [Report(id: "n-1", disposition: .failed, error: "add failed")])
    }

    func testDisabledOrDeniedReportsFailedWithoutShowing() {
        let off = presenter(enabled: false)
        off.present(stop)
        let denied = presenter(status: .denied)
        denied.present(AppNotice(notificationID: "n-2"))
        XCTAssertTrue(center.added.isEmpty)
        XCTAssertEqual(reports.map(\.disposition), [.failed, .failed])
        XCTAssertEqual(reports.map(\.id), ["n-1", "n-2"])
    }

    func testResponsesAreReportedAndHandedToTheApp() {
        let presenter = presenter()
        let info: [AnyHashable: Any] = [
            NotificationPresenter.UserInfoKey.windowID: 7,
            NotificationPresenter.UserInfoKey.navigable: true,
            NotificationPresenter.UserInfoKey.runID: "r1",
            NotificationPresenter.UserInfoKey.sessionID: "",
        ]
        presenter.handleResponse(notificationID: "n-1", actionIdentifier: UNNotificationDefaultActionIdentifier, userInfo: info)
        presenter.handleResponse(notificationID: "n-2", actionIdentifier: UNNotificationDismissActionIdentifier, userInfo: [:])
        presenter.handleResponse(notificationID: "n-3", actionIdentifier: "amx.snooze", userInfo: info)
        presenter.handleResponse(notificationID: "n-4", actionIdentifier: "amx.mute", userInfo: info)
        presenter.handleResponse(notificationID: "n-5", actionIdentifier: "amx.answer", userInfo: info)
        presenter.handleResponse(notificationID: "n-6", actionIdentifier: "unknown", userInfo: info)
        XCTAssertEqual(reports, [
            Report(id: "n-1", disposition: .clicked, error: nil),
            Report(id: "n-2", disposition: .dismissed, error: nil),
            Report(id: "n-3", disposition: .actionSnooze, error: nil),
            Report(id: "n-4", disposition: .actionMute, error: nil),
            Report(id: "n-5", disposition: .actionAnswer, error: nil),
        ])
        XCTAssertEqual(responses.first, NotificationPresenter.Response(
            notificationID: "n-1", disposition: .clicked, windowID: 7, kittySocket: "", navigable: true, runID: "r1", sessionID: ""))
        XCTAssertEqual(responses[1].windowID, 0)
        XCTAssertFalse(responses[1].navigable)
    }

    /// A program's own notification (OSC 9/777) is the app's: shown only when
    /// it can be, never reported to the daemon, and a click on one goes to
    /// the session it came from.
    func testATerminalsNotificationIsShownButNeverReported() throws {
        XCTAssertFalse(presenter(enabled: false).presentTerminal(title: "t", body: "b", source: "api", runtimeSessionID: nil))
        XCTAssertFalse(presenter(status: .denied).presentTerminal(title: "t", body: "b", source: "api", runtimeSessionID: nil))
        let undecided = presenter(status: .notDetermined)
        XCTAssertFalse(undecided.presentTerminal(title: "t", body: "b", source: "api", runtimeSessionID: nil))
        XCTAssertEqual(center.permissionRequests, 0, "a program does not get to ask for permission")
        XCTAssertEqual(center.added, [])

        let shown = presenter()
        XCTAssertTrue(shown.presentTerminal(title: "Build", body: "done", source: "api — work", runtimeSessionID: "rs-1"))
        XCTAssertTrue(shown.presentTerminal(title: "", body: "tests passed", source: "Dashboard", runtimeSessionID: nil))
        XCTAssertEqual(center.added.count, 2)
        let session = try XCTUnwrap(center.added.first)
        XCTAssertTrue(session.identifier.hasPrefix(NotificationPresenter.terminalPrefix))
        XCTAssertEqual(session.content.title, "Build")
        XCTAssertEqual(session.content.subtitle, "api — work")
        XCTAssertEqual(session.content.body, "done")
        XCTAssertEqual(session.content.threadIdentifier, "runtime:rs-1")
        XCTAssertEqual(session.content.categoryIdentifier, NotificationCategories.plain)
        let view = center.added[1].content
        XCTAssertEqual(view.title, "Dashboard", "OSC 9 has no title: the source is one")
        XCTAssertEqual(view.subtitle, "")
        XCTAssertEqual(reports, [], "the daemon never hears of it")

        shown.handleResponse(notificationID: session.identifier, actionIdentifier: UNNotificationDefaultActionIdentifier,
                                 userInfo: session.content.userInfo)
        XCTAssertEqual(reports, [])
        XCTAssertEqual(responses.last?.embeddedRuntimeSessionID, "rs-1")
        XCTAssertEqual(responses.last?.disposition, .clicked)
    }

    // MARK: Permission

    func testPermissionIsAskedOnTheFirstFrameNotAtLaunch() {
        let presenter = presenter(status: .notDetermined)
        XCTAssertEqual(center.permissionRequests, 0, "nothing is asked before a frame arrives")
        presenter.present(stop)
        presenter.present(AppNotice(notificationID: "n-2"))
        XCTAssertEqual(center.permissionRequests, 1, "one prompt for every waiting frame")
        XCTAssertTrue(center.added.isEmpty)
        center.answerPermission(granted: true)
        XCTAssertEqual(center.added.map(\.identifier), ["n-1", "n-2"])
        XCTAssertEqual(reports.map(\.disposition), [.delivered, .delivered])
        XCTAssertEqual(presenter.authorization, .authorized)
    }

    func testDeniedPermissionReportsFailedAndDropsTheClaim() {
        let presenter = presenter(status: .notDetermined)
        XCTAssertEqual(presenter.claim, NotificationClaim(backend: true, authorized: true), "a never-asked app claims, to be asked")
        presenter.present(stop)
        center.answerPermission(granted: false)
        XCTAssertEqual(reports, [Report(id: "n-1", disposition: .failed, error: "notifications are not authorized")])
        XCTAssertEqual(claims, [NotificationClaim(backend: false, authorized: false)])
    }

    // MARK: Claim

    func testClaimFollowsPreferenceAndAuthorization() {
        XCTAssertEqual(NotificationClaim.of(enabled: true, authorization: .authorized), NotificationClaim(backend: true, authorized: true))
        XCTAssertEqual(NotificationClaim.of(enabled: true, authorization: .notDetermined), NotificationClaim(backend: true, authorized: true))
        XCTAssertEqual(NotificationClaim.of(enabled: true, authorization: .denied), NotificationClaim())
        XCTAssertEqual(NotificationClaim.of(enabled: false, authorization: .authorized), NotificationClaim())
        XCTAssertEqual(NotificationAuthorization(.provisional), .authorized)

        let presenter = presenter()
        presenter.enabled = false
        presenter.enabled = false
        presenter.enabled = true
        XCTAssertEqual(claims, [NotificationClaim(), NotificationClaim(backend: true, authorized: true)])

        // Revoked in System Settings: noticed on the next refresh.
        center.status = .denied
        presenter.refreshAuthorization()
        XCTAssertEqual(claims.last, NotificationClaim())
        presenter.refreshAuthorization()
        XCTAssertEqual(claims.count, 3, "an unchanged claim is not republished")
    }

    func testSubscribeCarriesTheClaim() throws {
        let params = SubscribeParams(clientVersion: "1", notificationBackend: true, notificationsAuthorized: true)
        let data = try JSONEncoder().encode(DaemonRequest.subscribing(params))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let subscribe = try XCTUnwrap(object["subscribe"] as? [String: Any])
        XCTAssertEqual(subscribe["notification_backend"] as? Bool, true)
        XCTAssertEqual(subscribe["notifications_authorized"] as? Bool, true)
    }

    // MARK: Retract

    func testRetractRemovesTheBanner() {
        let presenter = presenter()
        presenter.present(stop)
        presenter.retract(NotificationRetractFrame(notificationID: "n-1", reason: "superseded"))
        XCTAssertEqual(center.removed, ["n-1"])
        presenter.retract(NotificationRetractFrame(notificationID: "", reason: "x"))
        XCTAssertEqual(center.removed, ["n-1"], "an empty id removes nothing")
    }

    func testRetractWhileAwaitingPermissionNeverShows() {
        let presenter = presenter(status: .notDetermined)
        presenter.present(stop)
        presenter.retract(NotificationRetractFrame(notificationID: "n-1", reason: "unconfirmed"))
        center.answerPermission(granted: true)
        XCTAssertTrue(center.added.isEmpty)
        XCTAssertTrue(reports.isEmpty)
    }

    func testRetractWhileAddingRemovesItOnceAdded() {
        let presenter = presenter()
        center.autoAdd = nil
        presenter.present(stop)
        presenter.retract(NotificationRetractFrame(notificationID: "n-1", reason: "unconfirmed"))
        center.finishAdd("n-1")
        XCTAssertEqual(center.removed, ["n-1", "n-1"])
        XCTAssertTrue(reports.isEmpty, "a withdrawn notice is not reported delivered")
    }

    // MARK: Wire

    func testDispositionRequestEncodesAsTheDaemonReadsIt() throws {
        let params = DispositionParams(notificationID: "n-1", disposition: "failed", error: "add failed")
        let data = try JSONEncoder().encode(DaemonRequest.disposing(params))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object["type"] as? String, "notification_disposition")
        let disposition = try XCTUnwrap(object["disposition"] as? [String: Any])
        XCTAssertEqual(disposition["notification_id"] as? String, "n-1")
        XCTAssertEqual(disposition["disposition"] as? String, "failed")
        XCTAssertEqual(disposition["error"] as? String, "add failed")

        let clicked = try JSONEncoder().encode(DaemonRequest.disposing(DispositionParams(notificationID: "n-1", disposition: "clicked")))
        let clickedObject = try XCTUnwrap(JSONSerialization.jsonObject(with: clicked) as? [String: Any])
        XCTAssertEqual((clickedObject["disposition"] as? [String: Any])?.keys.sorted(), ["disposition", "notification_id"])
    }

    func testDispositionReplies() {
        XCTAssertNil(DaemonClient.dispositionError(fromReply: Data(#"{"ok":true}"#.utf8)))
        XCTAssertEqual(DaemonClient.dispositionError(fromReply: Data(#"{"ok":false,"error":"bad"}"#.utf8)), "bad")
        XCTAssertNotNil(DaemonClient.dispositionError(fromReply: Data("x".utf8)))

        var sent: Data?
        let client = DaemonClient(socketPath: "/tmp/none", roundTrip: { _, request, _ in
            sent = request
            return Data(#"{"ok":true}"#.utf8)
        })
        XCTAssertNil(client.notificationDisposition(DispositionParams(notificationID: "n-1", disposition: "delivered")))
        XCTAssertNotNil(sent)
    }

    func testSessionLookupRefusesAmbiguousWindows() {
        var a = CompanionSession(windowID: 4)
        a.kittySocket = "unix:/tmp/kitty-1"
        var b = CompanionSession(windowID: 4)
        b.kittySocket = "unix:/tmp/kitty-2"
        let c = CompanionSession(windowID: 5)
        XCTAssertNil(AppDelegate.session(windowID: 4, in: [a, b, c]), "window IDs repeat across Kitty instances")
        XCTAssertEqual(AppDelegate.session(windowID: 5, in: [a, b, c]), c)
        XCTAssertNil(AppDelegate.session(windowID: 0, in: [c]))
    }

    func testNotificationSocketSurvivesUserInfoRoundTrip() throws {
        let json = #"{"notification_id":"n-8","window_id":4,"kitty_socket":"unix:/tmp/kitty-2","navigable":true,"actions":["mute"]}"#
        let notice = try JSONDecoder().decode(AppNotice.self, from: Data(json.utf8))
        XCTAssertEqual(notice.kittySocket, "unix:/tmp/kitty-2")

        let presenter = presenter()
        let userInfo = NotificationPresenter.request(for: notice).content.userInfo
        presenter.handleResponse(notificationID: "n-8", actionIdentifier: UNNotificationDefaultActionIdentifier, userInfo: userInfo)
        presenter.handleResponse(notificationID: "n-8", actionIdentifier: "amx.mute", userInfo: userInfo)
        XCTAssertEqual(responses.map(\.kittySocket), ["unix:/tmp/kitty-2", "unix:/tmp/kitty-2"])
        XCTAssertEqual(responses.map(\.windowID), [4, 4])

        // A banner posted before the socket existed reads back as none.
        presenter.handleResponse(notificationID: "n-0", actionIdentifier: "amx.mute",
                                 userInfo: [NotificationPresenter.UserInfoKey.windowID: 4])
        XCTAssertEqual(responses.last?.kittySocket, "")
    }

    func testNotificationResponseUsesCapturedKittySocket() {
        func response(_ disposition: NotificationDisposition, socket: String, windowID: Int = 4) -> NotificationPresenter.Response {
            NotificationPresenter.Response(notificationID: "n-1", disposition: disposition, windowID: windowID,
                                           kittySocket: socket, navigable: true, runID: "", sessionID: "")
        }
        var kitty = CompanionKitty()
        kitty.socket = "unix:/tmp/kitty-1"
        kitty.sockets = ["unix:/tmp/kitty-1", "unix:/tmp/kitty-2"]
        // The snapshot instance's row names no socket of its own.
        let local = CompanionSession(windowID: 4)
        var foreign = CompanionSession(windowID: 4)
        foreign.kittySocket = "unix:/tmp/kitty-2"

        // Duplicate IDs: click and mute both follow the captured instance.
        for disposition in [NotificationDisposition.clicked, .actionMute] {
            let fromForeign = AppDelegate.origin(of: response(disposition, socket: "unix:/tmp/kitty-2"), in: [local, foreign], snapshotKitty: kitty)
            XCTAssertEqual(fromForeign, AppDelegate.NoticeOrigin(socket: "unix:/tmp/kitty-2", session: foreign))
            let fromLocal = AppDelegate.origin(of: response(disposition, socket: "unix:/tmp/kitty-1"), in: [local, foreign], snapshotKitty: kitty)
            XCTAssertEqual(fromLocal, AppDelegate.NoticeOrigin(socket: "unix:/tmp/kitty-1", session: local))
        }

        // The origin row is gone and only the other instance's window 4 is
        // listed: a click still raises the origin, and Mute refuses rather
        // than muting the unrelated pane.
        let missing = AppDelegate.origin(of: response(.actionMute, socket: "unix:/tmp/kitty-2"), in: [local], snapshotKitty: kitty)
        XCTAssertEqual(missing, AppDelegate.NoticeOrigin(socket: "unix:/tmp/kitty-2", session: nil))

        // An old notice with no socket, while two instances are known, names
        // neither instance nor session.
        let old = AppDelegate.origin(of: response(.clicked, socket: ""), in: [local, foreign], snapshotKitty: kitty)
        XCTAssertEqual(old, AppDelegate.NoticeOrigin(socket: nil, session: nil))
        let oldMissing = AppDelegate.origin(of: response(.actionMute, socket: ""), in: [local], snapshotKitty: kitty)
        XCTAssertEqual(oldMissing, AppDelegate.NoticeOrigin(socket: nil, session: nil))

        // With a single Kitty there is nothing to confuse it with.
        var single = CompanionKitty()
        single.socket = "unix:/tmp/kitty-1"
        let alone = AppDelegate.origin(of: response(.actionMute, socket: ""), in: [local], snapshotKitty: single)
        XCTAssertEqual(alone, AppDelegate.NoticeOrigin(socket: "unix:/tmp/kitty-1", session: local))
    }
}
