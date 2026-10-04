import Foundation
import UserNotifications

/// `notification.AppNotice`: one notification frame's payload. It carries
/// what a banner shows and what the app reports back, never the event that
/// caused it.
struct AppNotice: Decodable, Equatable {
    var notificationID = ""
    var windowID = 0
    /// Whether a click can focus a pane; profile-wide notices have none.
    var navigable = false
    var title = ""
    var subtitle = ""
    /// The daemon's notification action (`stop`, `permission`, `elicitation`, …).
    var action = ""
    var runID = ""
    var sessionID = ""
    /// What the notice offers besides its click: `mute`, `snooze`, `answer`.
    var actions: [String] = []

    enum CodingKeys: String, CodingKey {
        case notificationID = "notification_id"
        case windowID = "window_id"
        case navigable, title, subtitle, action
        case runID = "run_id"
        case sessionID = "session_id"
        case actions
    }

    init(notificationID: String, windowID: Int = 0, navigable: Bool = false, title: String = "", subtitle: String = "",
         action: String = "", runID: String = "", sessionID: String = "", actions: [String] = []) {
        self.notificationID = notificationID
        self.windowID = windowID
        self.navigable = navigable
        self.title = title
        self.subtitle = subtitle
        self.action = action
        self.runID = runID
        self.sessionID = sessionID
        self.actions = actions
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        notificationID = c.lenient(.notificationID, "")
        windowID = c.lenient(.windowID, 0)
        navigable = c.lenient(.navigable, false)
        title = c.lenient(.title, "")
        subtitle = c.lenient(.subtitle, "")
        action = c.lenient(.action, "")
        runID = c.lenient(.runID, "")
        sessionID = c.lenient(.sessionID, "")
        actions = c.lenient(.actions, [])
    }
}

/// The dispositions the app reports in `notification_disposition`
/// (`protocol.CompanionDispositions`).
enum NotificationDisposition: String, Equatable {
    case delivered
    case clicked
    case dismissed
    case actionMute = "action_mute"
    case actionSnooze = "action_snooze"
    case actionAnswer = "action_answer"
    case failed
}

/// The notification categories the app registers, and which one a notice
/// is shown under. A category's actions are fixed when it is registered, so
/// there is one per set of actions the daemon offers; a notice never shows an
/// action the daemon did not offer it. Every category asks for
/// `customDismissAction`, which is what makes a dismissal reach the app.
enum NotificationCategories {
    /// A session notice: Mute session, Snooze 1h.
    static let session = "amx.session"
    /// A notice with no pane to mute (a profile-wide one): Snooze 1h.
    static let sessionSnoozeOnly = "amx.session.snooze"
    /// An ASL question: Answer…, then the session actions it was offered.
    static let asl = "amx.asl"
    static let aslSnoozeOnly = "amx.asl.snooze"
    /// A permission prompt, or anything offering no action: the operator
    /// must look at the pane, so the banner offers nothing but its click.
    static let plain = "amx.plain"

    /// Action identifiers, as the delegate sees them.
    static let muteAction = "amx.mute"
    static let snoozeAction = "amx.snooze"
    static let answerAction = "amx.answer"

    /// The daemon's offer names (`protocol.NotificationAction*`).
    static let offerMute = "mute"
    static let offerSnooze = "snooze"
    static let offerAnswer = "answer"

    struct Action: Equatable {
        let identifier: String
        let title: String
        let foreground: Bool
    }

    static let mute = Action(identifier: muteAction, title: "Mute session", foreground: false)
    static let snooze = Action(identifier: snoozeAction, title: "Snooze 1h", foreground: false)
    static let answer = Action(identifier: answerAction, title: "Answer…", foreground: true)

    /// Every category and its actions, in display order.
    static let all: [(identifier: String, actions: [Action])] = [
        (session, [mute, snooze]),
        (sessionSnoozeOnly, [snooze]),
        (asl, [answer, mute, snooze]),
        (aslSnoozeOnly, [answer, snooze]),
        (plain, []),
    ]

    /// The category a notice is shown under. A permission notice gets no
    /// actions whatever it was offered; otherwise the offer decides, and an
    /// offer this app has no category for degrades to fewer actions, never
    /// to an action that was not offered.
    static func category(for notice: AppNotice) -> String {
        guard notice.action != "permission" else { return plain }
        let offered = Set(notice.actions)
        let mute = offered.contains(offerMute)
        let snooze = offered.contains(offerSnooze)
        if offered.contains(offerAnswer) {
            if mute && snooze { return asl }
            if snooze { return aslSnoozeOnly }
            return plain
        }
        if mute && snooze { return session }
        if snooze { return sessionSnoozeOnly }
        return plain
    }

    /// The disposition a banner response reports; nil for an identifier the
    /// app never registered.
    static func disposition(forAction identifier: String) -> NotificationDisposition? {
        switch identifier {
        case UNNotificationDefaultActionIdentifier: return .clicked
        case UNNotificationDismissActionIdentifier: return .dismissed
        case muteAction: return .actionMute
        case snoozeAction: return .actionSnooze
        case answerAction: return .actionAnswer
        default: return nil
        }
    }

    static func unCategories() -> Set<UNNotificationCategory> {
        Set(all.map { entry in
            UNNotificationCategory(
                identifier: entry.identifier,
                actions: entry.actions.map { action in
                    UNNotificationAction(identifier: action.identifier, title: action.title,
                                         options: action.foreground ? [.foreground] : [])
                },
                intentIdentifiers: [],
                options: [.customDismissAction]
            )
        })
    }
}

/// Whether the app may show notifications, as far as the presenter cares.
enum NotificationAuthorization: Equatable {
    /// Never asked: the first notification frame asks.
    case notDetermined
    case authorized
    case denied

    init(_ status: UNAuthorizationStatus) {
        switch status {
        case .authorized, .provisional, .ephemeral: self = .authorized
        case .notDetermined: self = .notDetermined
        case .denied: self = .denied
        @unknown default: self = .denied
        }
    }
}

/// What the app tells subscribe about the notification backend role.
struct NotificationClaim: Equatable {
    var backend = false
    var authorized = false

    /// The app claims the role while `amxUseAppNotifications` is on and it is
    /// authorized, or has never been asked. The daemon sends frames only to a
    /// claimant that says it is authorized, and permission is requested on the
    /// first frame rather than at launch, so a never-asked app has to claim to
    /// be asked at all. That first notification may still end on Alerter: the
    /// daemon waits only 3s for `delivered`, and a denial reports `failed`.
    static func of(enabled: Bool, authorization: NotificationAuthorization) -> NotificationClaim {
        let claims = enabled && authorization != .denied
        return NotificationClaim(backend: claims, authorized: claims)
    }
}

/// The part of `UNUserNotificationCenter` the presenter uses, so tests can
/// drive it with a fake. Completions may arrive on any thread.
protocol NotificationCenterClient: AnyObject {
    func authorizationStatus(_ completion: @escaping (UNAuthorizationStatus) -> Void)
    func requestAuthorization(_ completion: @escaping (Bool, Error?) -> Void)
    func setCategories(_ categories: Set<UNNotificationCategory>)
    func add(_ request: UNNotificationRequest, completion: @escaping (Error?) -> Void)
    func remove(identifiers: [String])
}

/// The real center.
final class SystemNotificationCenter: NotificationCenterClient {
    let center: UNUserNotificationCenter

    init(center: UNUserNotificationCenter = .current()) {
        self.center = center
    }

    func authorizationStatus(_ completion: @escaping (UNAuthorizationStatus) -> Void) {
        center.getNotificationSettings { completion($0.authorizationStatus) }
    }

    func requestAuthorization(_ completion: @escaping (Bool, Error?) -> Void) {
        center.requestAuthorization(options: [.alert, .sound], completionHandler: completion)
    }

    func setCategories(_ categories: Set<UNNotificationCategory>) {
        center.setNotificationCategories(categories)
    }

    func add(_ request: UNNotificationRequest, completion: @escaping (Error?) -> Void) {
        center.add(request, withCompletionHandler: completion)
    }

    func remove(identifiers: [String]) {
        center.removeDeliveredNotifications(withIdentifiers: identifiers)
        center.removePendingNotificationRequests(withIdentifiers: identifiers)
    }
}

/// Shows the daemon's notification frames through the operating system's
/// notification center and reports what became of each.
///
/// - A frame is shown as a banner whose identifier is the `notification_id`
///   and whose thread is the window, under the category its offer maps to.
/// - `delivered` is reported once `add` succeeds; a failed `add`, a denied
///   permission, or app notifications switched off report `failed`, which
///   sends the notification through Alerter instead.
/// - A click reports `clicked` (the daemon focuses the pane), a dismissal
///   `dismissed`, and an action `action_*`; the app carries the action out
///   through `onAction`.
/// - `notification_retract` removes the banner, including one still waiting
///   on the permission prompt, which is then never shown.
///
/// Main-thread confined: every entry point is called on the main thread,
/// and completions are hopped back to it through `schedule`.
final class NotificationPresenter: NSObject, UNUserNotificationCenterDelegate {
    /// What the app does about a response, beyond reporting it.
    struct Response: Equatable {
        let notificationID: String
        let disposition: NotificationDisposition
        let windowID: Int
        let navigable: Bool
        let runID: String
        let sessionID: String
    }

    private let center: NotificationCenterClient
    private let schedule: (@escaping () -> Void) -> Void
    /// Sends one `notification_disposition`; the caller does the I/O off the
    /// main thread.
    var report: (_ notificationID: String, _ disposition: NotificationDisposition, _ error: String?) -> Void = { _, _, _ in }
    /// The app's own follow-up to a click or an action.
    var onResponse: (Response) -> Void = { _ in }
    /// Called when the claim subscribe should carry changes.
    var onClaimChange: (NotificationClaim) -> Void = { _ in }

    /// Whether `amxUseAppNotifications` is on.
    var enabled: Bool {
        didSet { if enabled != oldValue { publishClaim() } }
    }

    private(set) var authorization: NotificationAuthorization = .notDetermined
    private(set) var claim: NotificationClaim
    /// Notices waiting on the permission prompt.
    private var awaitingPermission: [AppNotice] = []
    private var permissionInFlight = false
    /// Notices retracted before `add` finished, which must not be shown.
    private var retracted: Set<String> = []
    /// Notices handed to `add` and not yet answered.
    private var adding: Set<String> = []

    init(center: NotificationCenterClient, enabled: Bool,
         schedule: @escaping (@escaping () -> Void) -> Void = { work in DispatchQueue.main.async(execute: work) }) {
        self.center = center
        self.enabled = enabled
        self.schedule = schedule
        claim = NotificationClaim.of(enabled: enabled, authorization: .notDetermined)
        super.init()
        center.setCategories(NotificationCategories.unCategories())
    }

    /// Reads the authorization again; it can change in System Settings at any
    /// time, and nothing tells the app.
    func refreshAuthorization() {
        center.authorizationStatus { [weak self] status in
            self?.schedule { self?.setAuthorization(NotificationAuthorization(status)) }
        }
    }

    private func setAuthorization(_ value: NotificationAuthorization) {
        guard authorization != value else { return }
        authorization = value
        publishClaim()
    }

    private func publishClaim() {
        let next = NotificationClaim.of(enabled: enabled, authorization: authorization)
        guard next != claim else { return }
        claim = next
        onClaimChange(next)
    }

    // MARK: Frames

    /// One notification frame's payload.
    func present(frame data: Data) {
        guard let notice = try? JSONDecoder().decode(AppNotice.self, from: data), !notice.notificationID.isEmpty else {
            return
        }
        present(notice)
    }

    func present(_ notice: AppNotice) {
        guard enabled else { return report(notice.notificationID, .failed, "app notifications are off") }
        switch authorization {
        case .authorized:
            add(notice)
        case .denied:
            report(notice.notificationID, .failed, "notifications are not authorized")
        case .notDetermined:
            awaitingPermission.append(notice)
            askPermission()
        }
    }

    private func askPermission() {
        guard !permissionInFlight else { return }
        permissionInFlight = true
        center.requestAuthorization { [weak self] granted, _ in
            self?.schedule { self?.finishPermission(granted: granted) }
        }
    }

    private func finishPermission(granted: Bool) {
        permissionInFlight = false
        setAuthorization(granted ? .authorized : .denied)
        let waiting = awaitingPermission
        awaitingPermission = []
        for notice in waiting {
            if retracted.remove(notice.notificationID) != nil { continue }
            present(notice)
        }
    }

    private func add(_ notice: AppNotice) {
        let id = notice.notificationID
        adding.insert(id)
        center.add(Self.request(for: notice)) { [weak self] error in
            self?.schedule { self?.finishAdd(id, error: error) }
        }
    }

    private func finishAdd(_ id: String, error: Error?) {
        adding.remove(id)
        if let error {
            retracted.remove(id)
            return report(id, .failed, error.localizedDescription)
        }
        if retracted.remove(id) != nil {
            // Withdrawn while it was being added: the daemon has moved on.
            center.remove(identifiers: [id])
            return
        }
        report(id, .delivered, nil)
    }

    /// A `notification_retract` frame.
    func retract(_ frame: NotificationRetractFrame) {
        let id = frame.notificationID
        guard !id.isEmpty else { return }
        if awaitingPermission.contains(where: { $0.notificationID == id }) || adding.contains(id) {
            retracted.insert(id)
        }
        center.remove(identifiers: [id])
    }

    /// The request a notice is shown with.
    static func request(for notice: AppNotice) -> UNNotificationRequest {
        let content = UNMutableNotificationContent()
        content.title = notice.title
        if !notice.subtitle.isEmpty { content.subtitle = notice.subtitle }
        content.categoryIdentifier = NotificationCategories.category(for: notice)
        content.threadIdentifier = notice.windowID > 0 ? String(notice.windowID) : "amx"
        content.userInfo = [
            UserInfoKey.windowID: notice.windowID,
            UserInfoKey.navigable: notice.navigable,
            UserInfoKey.runID: notice.runID,
            UserInfoKey.sessionID: notice.sessionID,
        ]
        return UNNotificationRequest(identifier: notice.notificationID, content: content, trigger: nil)
    }

    enum UserInfoKey {
        static let windowID = "amx_window_id"
        static let navigable = "amx_navigable"
        static let runID = "amx_run_id"
        static let sessionID = "amx_session_id"
    }

    // MARK: Responses

    /// One banner response: reports it, then hands it to the app.
    func handleResponse(notificationID: String, actionIdentifier: String, userInfo: [AnyHashable: Any]) {
        guard let disposition = NotificationCategories.disposition(forAction: actionIdentifier) else { return }
        report(notificationID, disposition, nil)
        onResponse(Response(
            notificationID: notificationID,
            disposition: disposition,
            windowID: userInfo[UserInfoKey.windowID] as? Int ?? 0,
            navigable: userInfo[UserInfoKey.navigable] as? Bool ?? false,
            runID: userInfo[UserInfoKey.runID] as? String ?? "",
            sessionID: userInfo[UserInfoKey.sessionID] as? String ?? ""
        ))
    }

    // MARK: UNUserNotificationCenterDelegate

    /// An accessory app counts as foreground whenever its menu is open; show
    /// the banner regardless.
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list])
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        let id = response.notification.request.identifier
        let action = response.actionIdentifier
        let userInfo = response.notification.request.content.userInfo
        schedule { [weak self] in
            self?.handleResponse(notificationID: id, actionIdentifier: action, userInfo: userInfo)
            completionHandler()
        }
    }
}

/// One value shared between the main thread and a worker thread.
final class LockedValue<Value> {
    private let lock = NSLock()
    private var stored: Value

    init(_ value: Value) {
        stored = value
    }

    var value: Value {
        get { lock.lock(); defer { lock.unlock() }; return stored }
        set { lock.lock(); stored = newValue; lock.unlock() }
    }
}
