import Foundation
import UserNotifications

/// `notification.AppNotice`: one notification frame's payload. It carries
/// what a banner shows and what the app reports back, never the event that
/// caused it.
struct AppNotice: Decodable, Equatable {
    var notificationID = ""
    var windowID = 0
    /// The Kitty instance `windowID` lives in, as the daemon captured it.
    /// Window IDs repeat across instances; empty when none was recorded.
    var kittySocket = ""
    /// Whether a click can focus a pane; profile-wide notices have none.
    var navigable = false
    var title = ""
    var subtitle = ""
    /// The daemon's notification action (`stop`, `permission`, `elicitation`, …).
    var action = ""
    var runID = ""
    var sessionID = ""
    /// The runtime session the notice is about, when the daemon names one.
    var runtimeSessionID = ""
    /// That session's backend; a notice for one other than Kitty has no
    /// window to focus, and a click shows its terminal in the workspace.
    var backend = ""
    /// What the notice offers besides its click: `mute`, `snooze`, `answer`.
    var actions: [String] = []

    enum CodingKeys: String, CodingKey {
        case notificationID = "notification_id"
        case windowID = "window_id"
        case kittySocket = "kitty_socket"
        case navigable, title, subtitle, action
        case runID = "run_id"
        case sessionID = "session_id"
        case runtimeSessionID = "runtime_session_id"
        case backend, actions
    }

    init(notificationID: String, windowID: Int = 0, kittySocket: String = "", navigable: Bool = false, title: String = "", subtitle: String = "",
         action: String = "", runID: String = "", sessionID: String = "", actions: [String] = []) {
        self.notificationID = notificationID
        self.windowID = windowID
        self.kittySocket = kittySocket
        self.navigable = navigable
        self.title = title
        self.subtitle = subtitle
        self.action = action
        self.runID = runID
        self.sessionID = sessionID
        self.actions = actions
    }

    /// The runtime session whose terminal a click shows in the workspace;
    /// empty for a Kitty session, which a click focuses in Kitty.
    var embeddedRuntimeSessionID: String {
        backend.isEmpty || backend == CompanionRuntime.kittyBackend ? "" : runtimeSessionID
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        notificationID = c.lenient(.notificationID, "")
        windowID = c.lenient(.windowID, 0)
        kittySocket = c.lenient(.kittySocket, "")
        navigable = c.lenient(.navigable, false)
        title = c.lenient(.title, "")
        subtitle = c.lenient(.subtitle, "")
        action = c.lenient(.action, "")
        runID = c.lenient(.runID, "")
        sessionID = c.lenient(.sessionID, "")
        runtimeSessionID = c.lenient(.runtimeSessionID, "")
        backend = c.lenient(.backend, "")
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
    case actionRespond = "action_respond"
    case failed
}

/// The notification categories the app registers, and which one a notice
/// is shown under. A category's actions are fixed when it is registered, so
/// there is one per set of actions the daemon offers; a notice never shows an
/// action the daemon did not offer it. Every category asks for
/// `customDismissAction`, which is what makes a dismissal reach the app.
enum NotificationCategories {
    /// A session notice: Mute Session, Snooze All for 1 Hour.
    static let session = "amx.session"
    /// A notice with no session to mute (a profile-wide one): Snooze All for
    /// 1 Hour.
    static let sessionSnoozeOnly = "amx.session.snooze"
    /// An ASL question: Answer…, then the session actions it was offered.
    static let asl = "amx.asl"
    static let aslSnoozeOnly = "amx.asl.snooze"
    /// A permission prompt of a session `amx send` can write to: Answer…,
    /// which shows the session's screen before any key can be pressed.
    static let prompt = "amx.prompt"
    /// A permission prompt anywhere else, or anything offering no action: the
    /// operator must look at the pane, so the banner offers nothing but its
    /// click.
    static let plain = "amx.plain"

    /// Action identifiers, as the delegate sees them.
    static let muteAction = "amx.mute"
    static let snoozeAction = "amx.snooze"
    static let answerAction = "amx.answer"
    static let respondAction = "amx.respond"

    /// The daemon's offer names (`protocol.NotificationAction*`).
    static let offerMute = "mute"
    static let offerSnooze = "snooze"
    static let offerAnswer = "answer"
    static let offerRespond = "respond"

    struct Action: Equatable {
        let identifier: String
        let title: String
        let foreground: Bool
    }

    static let mute = Action(identifier: muteAction, title: "Mute Session", foreground: false)
    static let snooze = Action(identifier: snoozeAction, title: "Snooze All for 1 Hour", foreground: false)
    static let answer = Action(identifier: answerAction, title: "Answer…", foreground: true)
    static let respond = Action(identifier: respondAction, title: "Answer…", foreground: true)

    /// Every category and its actions, in display order.
    static let all: [(identifier: String, actions: [Action])] = [
        (session, [mute, snooze]),
        (sessionSnoozeOnly, [snooze]),
        (asl, [answer, mute, snooze]),
        (aslSnoozeOnly, [answer, snooze]),
        (prompt, [respond]),
        (plain, []),
    ]

    /// The category a notice is shown under. A permission notice gets at most
    /// Answer…, and only when that was offered for a session the workspace
    /// embeds; nothing else it was offered is shown. Otherwise the offer
    /// decides, and an offer this app has no category for degrades to fewer
    /// actions, never to an action that was not offered.
    static func category(for notice: AppNotice) -> String {
        let offered = Set(notice.actions)
        guard notice.action != "permission" else {
            return offered.contains(offerRespond) && !notice.embeddedRuntimeSessionID.isEmpty ? prompt : plain
        }
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
        case respondAction: return .actionRespond
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
    /// Removes every banner the app has shown, from this launch or an
    /// earlier one.
    func removeAll()
}

/// The real center.
final class SystemNotificationCenter: NotificationCenterClient {
    let center: UNUserNotificationCenter
    static let authorizationOptions: UNAuthorizationOptions = [.alert, .sound, .badge]

    init(center: UNUserNotificationCenter = .current()) {
        self.center = center
    }

    func authorizationStatus(_ completion: @escaping (UNAuthorizationStatus) -> Void) {
        center.getNotificationSettings { [self] settings in
            // Earlier builds registered only banners and sound. Register the
            // new badge capability for those installs, without overriding a
            // user who disabled badges or denied notifications.
            if settings.authorizationStatus == .authorized, settings.badgeSetting == .notSupported {
                requestAuthorization { _, _ in completion(settings.authorizationStatus) }
            } else {
                completion(settings.authorizationStatus)
            }
        }
    }

    func requestAuthorization(_ completion: @escaping (Bool, Error?) -> Void) {
        center.requestAuthorization(options: Self.authorizationOptions, completionHandler: completion)
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

    func removeAll() {
        center.removeAllDeliveredNotifications()
        center.removeAllPendingNotificationRequests()
    }
}

/// A desktop notification a program in one of the workspace's terminals
/// asked for (OSC 9 or 777). The app's own: the daemon never hears of it.
struct TerminalNotice: Equatable {
    /// The terminal it came from: a session's, or a utility view's. A view
    /// is never a session; its notices are navigation only.
    enum Origin: Hashable {
        case session(runtimeSessionID: String)
        case view(id: String)

        /// The banner thread, and what its notices are withdrawn by.
        var thread: String {
            switch self {
            case let .session(id): return "runtime:\(id)"
            case let .view(id): return "view:\(id)"
            }
        }
    }

    var title: String
    var body: String
    /// What titles it: the session's name or the view's title.
    var source: String
    var origin: Origin
}

/// What became of a terminal notice.
enum TerminalOutcome: Equatable {
    case shown
    /// App notifications are off, or not allowed.
    case unavailable
    case snoozed
    case muted
    /// What it came from is in the pane of the key workspace window.
    case onScreen
}

/// Shows the daemon's notification frames through the operating system's
/// notification center and reports what became of each.
///
/// - A frame is shown as a banner whose identifier is the `notification_id`
///   and whose thread is the window, under the category its offer maps to.
/// - `delivered` is reported once `add` succeeds; a failed `add`, a denied
///   permission, or app notifications switched off report `failed`, which
///   sends the notification through Alerter instead.
/// - A click reports `clicked` (the daemon focuses a Kitty pane; the app
///   asks for any other session with `focus_session`), a dismissal
///   `dismissed`, and an action `action_*`; the app carries the action out
///   through `onAction`.
/// - `notification_retract` removes the banner, including one still waiting
///   on the permission prompt, which is then never shown.
/// - When the connection the daemon sent a notice on ends, the daemon ends
///   that delivery and will never retract it, so the banner is removed then
///   (`connectionEnded`); one from an earlier launch, at launch
///   (`removeEarlierLaunches`).
///
/// Main-thread confined: every entry point is called on the main thread,
/// and completions are hopped back to it through `schedule`.
final class NotificationPresenter: NSObject, UNUserNotificationCenterDelegate {
    /// What the app does about a response, beyond reporting it.
    struct Response: Equatable {
        let notificationID: String
        let disposition: NotificationDisposition
        let windowID: Int
        /// The captured Kitty socket; empty for a notice that named none.
        let kittySocket: String
        let navigable: Bool
        let runID: String
        let sessionID: String
        /// Set when the notice is about a session the workspace embeds.
        var embeddedRuntimeSessionID = ""
        /// Set for a terminal notice from a utility view: the view a click
        /// opens the workspace on.
        var workspaceViewID = ""
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
    /// Called when the system's answer about notifications changes, whether
    /// or not the claim does, so Settings can say it was denied.
    var onAuthorizationChange: (NotificationAuthorization) -> Void = { _ in }

    /// Whether `amxUseAppNotifications` is on.
    var enabled: Bool {
        didSet { if enabled != oldValue { publishClaim() } }
    }

    private(set) var authorization: NotificationAuthorization = .notDetermined
    private(set) var claim: NotificationClaim
    /// Notices waiting on the permission prompt.
    private var awaitingPermission: [AppNotice] = []
    /// The terminal notices shown, by thread, oldest first: the center
    /// removes by identifier only.
    private var terminalIdentifiers: [String: [String]] = [:]
    /// Terminal notices handed to `add` and not yet answered.
    private var terminalAdding: Set<String> = []
    /// Terminal notices withdrawn or pushed out while still being added,
    /// which are removed again once `add` finishes. Kept apart from
    /// `retracted`: nothing about a terminal notice is reported.
    private var terminalWithdrawn: Set<String> = []
    private var permissionInFlight = false
    /// Notices retracted before `add` finished, which must not be shown.
    private var retracted: Set<String> = []
    /// Notices handed to `add` and not yet answered.
    private var adding: Set<String> = []
    /// Daemon notices shown on the current connection and not retracted.
    private(set) var shown: Set<String> = []

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
        onAuthorizationChange(value)
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
        shown.insert(id)
        report(id, .delivered, nil)
    }

    /// What a terminal notice's identifier starts with. Such a notice is
    /// the app's own, raised by a program in an embedded terminal (OSC 9 or
    /// 777): the daemon never heard of it, so nothing about it is reported.
    static let terminalPrefix = "amx-terminal:"

    /// The most terminal notices of one thread kept in Notification Center.
    /// An older one is removed when a newer one pushes it out, so whatever
    /// is still delivered is still remembered for withdrawal.
    static let terminalNoticesKept = 32

    /// Shows a program's desktop notification, titled by the session or view
    /// it came from, unless it should not be shown now: app notifications
    /// are off or not allowed, everything is snoozed, its session is muted,
    /// or what it came from is on screen in the key window. It never asks
    /// for permission, which belongs to the daemon's notices.
    @discardableResult
    func presentTerminal(_ notice: TerminalNotice, snoozed: Bool, muted: Bool, onScreen: Bool) -> TerminalOutcome {
        guard enabled, authorization == .authorized else { return .unavailable }
        if snoozed { return .snoozed }
        if muted { return .muted }
        if onScreen { return .onScreen }
        let request = Self.terminalRequest(notice)
        var kept = terminalIdentifiers[notice.origin.thread, default: []]
        kept.append(request.identifier)
        let evicted = kept.count - Self.terminalNoticesKept
        if evicted > 0 {
            // Forgetting a notice still shown would leave it behind when
            // its session or view is withdrawn.
            removeTerminal(Array(kept.prefix(evicted)))
            kept.removeFirst(evicted)
        }
        terminalIdentifiers[notice.origin.thread] = kept
        let id = request.identifier
        terminalAdding.insert(id)
        center.add(request) { [weak self] error in
            self?.schedule { self?.finishTerminalAdd(id, error: error) }
        }
        return .shown
    }

    private func finishTerminalAdd(_ id: String, error: Error?) {
        terminalAdding.remove(id)
        // Withdrawn while it was being added: the removal may have come
        // before the center had it, so it is removed again now. Identifiers
        // are never reused, so a newer notice of the same origin is untouched.
        if terminalWithdrawn.remove(id) != nil, error == nil {
            center.remove(identifiers: [id])
        }
    }

    /// Removes terminal notices, remembering those still being added.
    private func removeTerminal(_ ids: [String]) {
        terminalWithdrawn.formUnion(ids.filter(terminalAdding.contains))
        center.remove(identifiers: ids)
    }

    /// Removes the terminal notices of a session or view, which is being
    /// shown: they have nothing left to say.
    func withdrawTerminal(_ origin: TerminalNotice.Origin) {
        guard let ids = terminalIdentifiers.removeValue(forKey: origin.thread), !ids.isEmpty else { return }
        removeTerminal(ids)
    }

    static func terminalRequest(_ notice: TerminalNotice) -> UNNotificationRequest {
        let content = UNMutableNotificationContent()
        // OSC 9 carries a body alone; the source then titles it.
        content.title = notice.title.isEmpty ? notice.source : notice.title
        if !notice.title.isEmpty, !notice.source.isEmpty { content.subtitle = notice.source }
        content.body = notice.body
        content.categoryIdentifier = NotificationCategories.plain
        content.threadIdentifier = notice.origin.thread
        switch notice.origin {
        case let .session(id):
            content.userInfo = [UserInfoKey.embeddedRuntimeSessionID: id]
        case let .view(id):
            content.userInfo = [UserInfoKey.workspaceViewID: id]
        }
        let identifier = "\(terminalPrefix)\(notice.origin.thread):\(UUID().uuidString)"
        return UNNotificationRequest(identifier: identifier, content: content, trigger: nil)
    }

    /// Identifiers of the app's own notices about what the operator asked
    /// of it (a launch that failed). The daemon never hears of them.
    static let appPrefix = "amx-app:"

    /// Shows the app's own notice, when app notifications are on and allowed;
    /// false otherwise, so the caller can get the operator's attention some
    /// other way. Like a terminal's, it never asks for permission. The
    /// center can still refuse a notice it was handed: `failed` then runs,
    /// on the main thread, so the caller can fall back after all.
    @discardableResult
    func presentApp(title: String, body: String, failed: @escaping () -> Void = {}) -> Bool {
        guard enabled, authorization == .authorized else { return false }
        center.add(Self.appRequest(title: title, body: body)) { [schedule] error in
            guard error != nil else { return }
            schedule(failed)
        }
        return true
    }

    static func appRequest(title: String, body: String) -> UNNotificationRequest {
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.categoryIdentifier = NotificationCategories.plain
        content.threadIdentifier = "app"
        return UNNotificationRequest(identifier: "\(appPrefix)\(UUID().uuidString)", content: content, trigger: nil)
    }

    /// A `notification_retract` frame.
    func retract(_ frame: NotificationRetractFrame) {
        let id = frame.notificationID
        guard !id.isEmpty else { return }
        if awaitingPermission.contains(where: { $0.notificationID == id }) || adding.contains(id) {
            retracted.insert(id)
        }
        shown.remove(id)
        center.remove(identifiers: [id])
    }

    /// The feed's connection ended. The daemon ended every delivery it made
    /// on it and can never retract one now, so each banner it left would
    /// stay in Notification Center after its session moved on: they go,
    /// along with any still being added or waiting on the permission prompt.
    /// The app's own terminal and app notices are not the daemon's and stay.
    func connectionEnded() {
        let pending = adding.union(awaitingPermission.map(\.notificationID))
        retracted.formUnion(pending)
        let ids = Array(shown)
        shown = []
        if !ids.isEmpty { center.remove(identifiers: ids) }
    }

    /// At launch: banners an earlier launch showed can no longer be
    /// retracted by the daemon or withdrawn by the app, so none is kept.
    func removeEarlierLaunches() {
        center.removeAll()
    }

    /// The request a notice is shown with.
    static func request(for notice: AppNotice) -> UNNotificationRequest {
        let content = UNMutableNotificationContent()
        content.title = notice.title
        if !notice.subtitle.isEmpty { content.subtitle = notice.subtitle }
        content.categoryIdentifier = NotificationCategories.category(for: notice)
        content.threadIdentifier = threadIdentifier(for: notice)
        if soundedActions.contains(notice.action) { content.sound = .default }
        content.userInfo = [
            UserInfoKey.windowID: notice.windowID,
            UserInfoKey.kittySocket: notice.kittySocket,
            UserInfoKey.navigable: notice.navigable,
            UserInfoKey.runID: notice.runID,
            UserInfoKey.sessionID: notice.sessionID,
            UserInfoKey.embeddedRuntimeSessionID: notice.embeddedRuntimeSessionID,
        ]
        return UNNotificationRequest(identifier: notice.notificationID, content: content, trigger: nil)
    }

    /// The actions that wait on the operator, whose banners make a sound.
    static let soundedActions: Set<String> = ["permission", "elicitation"]

    /// The thread a notice is grouped under: its runtime session when the
    /// daemon names one, else its Kitty window (window IDs repeat across Kitty
    /// instances, so with the socket), else one thread for profile-wide notices.
    static func threadIdentifier(for notice: AppNotice) -> String {
        if !notice.runtimeSessionID.isEmpty { return "runtime:\(notice.runtimeSessionID)" }
        if notice.windowID > 0 { return "kitty:\(notice.kittySocket)#\(notice.windowID)" }
        return "amx"
    }

    enum UserInfoKey {
        static let windowID = "amx_window_id"
        static let kittySocket = "amx_kitty_socket"
        static let navigable = "amx_navigable"
        static let runID = "amx_run_id"
        static let sessionID = "amx_session_id"
        static let embeddedRuntimeSessionID = "amx_embedded_runtime_session_id"
        static let workspaceViewID = "amx_workspace_view_id"
    }

    // MARK: Responses

    /// One banner response: reports it, then hands it to the app.
    func handleResponse(notificationID: String, actionIdentifier: String, userInfo: [AnyHashable: Any]) {
        guard let disposition = NotificationCategories.disposition(forAction: actionIdentifier) else { return }
        if !notificationID.hasPrefix(Self.terminalPrefix), !notificationID.hasPrefix(Self.appPrefix) {
            report(notificationID, disposition, nil)
        }
        onResponse(Response(
            notificationID: notificationID,
            disposition: disposition,
            windowID: userInfo[UserInfoKey.windowID] as? Int ?? 0,
            kittySocket: userInfo[UserInfoKey.kittySocket] as? String ?? "",
            navigable: userInfo[UserInfoKey.navigable] as? Bool ?? false,
            runID: userInfo[UserInfoKey.runID] as? String ?? "",
            sessionID: userInfo[UserInfoKey.sessionID] as? String ?? "",
            embeddedRuntimeSessionID: userInfo[UserInfoKey.embeddedRuntimeSessionID] as? String ?? "",
            workspaceViewID: userInfo[UserInfoKey.workspaceViewID] as? String ?? ""
        ))
    }

    // MARK: UNUserNotificationCenterDelegate

    /// An accessory app counts as foreground whenever its menu is open; show
    /// the banner regardless, and play its sound when the request has one.
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler(Self.foregroundPresentation(for: notification.request.content))
    }

    /// How a notification is shown while the app is in the foreground: always
    /// as a banner and in the list, with a sound only when its content asks
    /// for one (`.sound` is needed for that whatever the content says).
    static func foregroundPresentation(for content: UNNotificationContent) -> UNNotificationPresentationOptions {
        content.sound == nil ? [.banner, .list] : [.banner, .list, .sound]
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
