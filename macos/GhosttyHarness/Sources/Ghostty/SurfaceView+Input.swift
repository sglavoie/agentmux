// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit

// Keyboard, IME, mouse, and the Edit menu, kept as close to upstream's
// SurfaceView_AppKit as one window over one surface allows: tt binds keys only
// the kitty keyboard protocol can tell apart (ctrl+m, ctrl+i, shift+enter,
// ctrl+j), so every key reaches `ghostty_surface_key` with the mods, consumed
// mods, and unshifted codepoint Ghostty.app would send.
extension Ghostty.SurfaceView {
    // MARK: Local events

    func localEventHandler(_ event: NSEvent) -> NSEvent? {
        switch event.type {
        case .keyUp: localEventKeyUp(event)
        case .leftMouseDown: localEventLeftMouseDown(event)
        default: event
        }
    }

    private func localEventLeftMouseDown(_ event: NSEvent) -> NSEvent? {
        guard let window, event.window != nil, window == event.window else { return event }
        let location = convert(event.locationInWindow, from: nil)
        guard hitTest(location) == self else { return event }

        suppressNextLeftMouseUp = false
        guard window.firstResponder !== self else { return event }

        // Already key: the click only moves focus back to the terminal, so it
        // is consumed rather than reported.
        if NSApp.isActive && window.isKeyWindow {
            window.makeFirstResponder(self)
            suppressNextLeftMouseUp = true
            return nil
        }

        // Otherwise AppKit must still see it to make the window key.
        window.makeFirstResponder(self)
        return event
    }

    private func localEventKeyUp(_ event: NSEvent) -> NSEvent? {
        // Command key-ups never reach the responder chain.
        guard event.modifierFlags.contains(.command), focused else { return event }
        keyUp(with: event)
        return nil
    }

    // MARK: Keyboard

    override func keyDown(with event: NSEvent) {
        guard let surface else {
            interpretKeyEvents([event])
            return
        }

        // The surface may change the translating modifiers (option-as-alt).
        let translationModsGhostty = Ghostty.eventModifierFlags(
            mods: ghostty_surface_key_translation_mods(surface, Ghostty.ghosttyMods(event.modifierFlags))
        )

        // Hidden bits in the event matter for some dead keys, so only the four
        // translating flags are rewritten.
        var translationMods = event.modifierFlags
        for flag in [NSEvent.ModifierFlags.shift, .control, .option, .command] {
            if translationModsGhostty.contains(flag) {
                translationMods.insert(flag)
            } else {
                translationMods.remove(flag)
            }
        }

        // The original event must be reused when nothing changed: input
        // methods such as Korean depend on its identity.
        let translationEvent: NSEvent
        if translationMods == event.modifierFlags {
            translationEvent = event
        } else {
            translationEvent = NSEvent.keyEvent(
                with: event.type,
                location: event.locationInWindow,
                modifierFlags: translationMods,
                timestamp: event.timestamp,
                windowNumber: event.windowNumber,
                context: nil,
                characters: event.characters(byApplyingModifiers: translationMods) ?? "",
                charactersIgnoringModifiers: event.charactersIgnoringModifiers ?? "",
                isARepeat: event.isARepeat,
                keyCode: event.keyCode
            ) ?? event
        }

        let action = event.isARepeat ? GHOSTTY_ACTION_REPEAT : GHOSTTY_ACTION_PRESS

        keyTextAccumulator = []
        defer { keyTextAccumulator = nil }

        let markedTextBefore = markedText.length > 0
        let keyboardIdBefore: String? = markedTextBefore ? nil : KeyboardLayout.id

        // We are in keyDown, so a key equivalent handed back to AppKit has
        // arrived and must not be redispatched.
        lastPerformKeyEvent = nil

        interpretKeyEvents([translationEvent])

        // An input method used the key to switch layouts.
        if !markedTextBefore && keyboardIdBefore != KeyboardLayout.id {
            return
        }

        syncPreedit(clearIfNeeded: markedTextBefore)

        if let list = keyTextAccumulator, !list.isEmpty {
            // Composed text (dead keys, IME commits) is never itself composing.
            for text in list {
                _ = keyAction(action, event: event, translationEvent: translationEvent, text: text)
            }
        } else {
            _ = keyAction(
                action,
                event: event,
                translationEvent: translationEvent,
                text: translationEvent.ghosttyCharacters,
                // A key that just ended a composition (Backspace cancelling a
                // preedit) is composing too, so it is not encoded.
                composing: markedText.length > 0 || markedTextBefore
            )
        }
    }

    override func keyUp(with event: NSEvent) {
        _ = keyAction(GHOSTTY_ACTION_RELEASE, event: event)
    }

    /// Cmd and Ctrl chords reach here before `keyDown`. A Ghostty binding
    /// (Cmd+C, Cmd+V, Cmd+Q, ...) runs the main menu item owning the same
    /// shortcut, else is performed by the surface; Ctrl+Return and
    /// Ctrl+/ are encoded rather than left to AppKit's context menu and beep;
    /// any other chord goes back to AppKit once, so the main menu can claim
    /// it, and if nothing does `doCommand` sends it round to `keyDown`.
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard event.type == .keyDown else { return false }
        // AppKit sends some equivalents (Ctrl+/) to the first view in the
        // hierarchy rather than the first responder.
        guard focused, let surface else { return false }

        var keyEvent = event.ghosttyKeyEvent(GHOSTTY_ACTION_PRESS)
        var flags = ghostty_binding_flags_e(0)
        let isBinding = (event.characters ?? "").withCString { ptr in
            keyEvent.text = ptr
            return ghostty_surface_key_is_binding(surface, keyEvent, &flags)
        }
        if isBinding {
            // A consumed binding the main menu also owns is the menu's, as
            // upstream arbitrates it: Cmd+, is Ghostty's open_config and tt's
            // Preferences. `all` and `performable` bindings stay the surface's.
            let consumed = flags.rawValue & GHOSTTY_BINDING_FLAGS_CONSUMED.rawValue != 0
            let surfaceOwned = flags.rawValue
                & (GHOSTTY_BINDING_FLAGS_ALL.rawValue | GHOSTTY_BINDING_FLAGS_PERFORMABLE.rawValue) != 0
            if consumed, !surfaceOwned,
               let menu = NSApp.mainMenu,
               MainMenu.performShortcut(for: event, in: menu) {
                return true
            }
            keyDown(with: event)
            return true
        }

        let equivalent: String
        switch event.charactersIgnoringModifiers {
        case "\r":
            guard event.modifierFlags.contains(.control) else { return false }
            equivalent = "\r"

        case "/":
            // Ctrl+/ is Ctrl+_, which AppKit would otherwise beep at.
            guard event.modifierFlags.contains(.control),
                  event.modifierFlags.isDisjoint(with: [.shift, .command, .option])
            else { return false }
            equivalent = "_"

        default:
            // AppKit synthesizes zero-timestamp events (Cmd+. as cancel's
            // Escape); those are never ours.
            if event.timestamp == 0 { return false }

            if !event.modifierFlags.contains(.command) && !event.modifierFlags.contains(.control) {
                lastPerformKeyEvent = nil
                return false
            }

            // The same event coming back after AppKit declined it is encoded.
            if let last = lastPerformKeyEvent {
                lastPerformKeyEvent = nil
                if last == event.timestamp {
                    equivalent = event.characters ?? ""
                    break
                }
            }

            lastPerformKeyEvent = event.timestamp
            return false
        }

        guard let finalEvent = NSEvent.keyEvent(
            with: .keyDown,
            location: event.locationInWindow,
            modifierFlags: event.modifierFlags,
            timestamp: event.timestamp,
            windowNumber: event.windowNumber,
            context: nil,
            characters: equivalent,
            charactersIgnoringModifiers: equivalent,
            isARepeat: event.isARepeat,
            keyCode: event.keyCode
        ) else { return false }
        keyDown(with: finalEvent)
        return true
    }

    override func flagsChanged(with event: NSEvent) {
        guard let action = Ghostty.modifierKeyAction(keyCode: event.keyCode, flags: event.modifierFlags) else {
            return
        }
        // Mid-preedit, modifiers belong to the input method.
        if hasMarkedText() { return }
        _ = keyAction(action, event: event)
    }

    @discardableResult
    func keyAction(
        _ action: ghostty_input_action_e,
        event: NSEvent,
        translationEvent: NSEvent? = nil,
        text: String? = nil,
        composing: Bool = false
    ) -> Bool {
        guard let surface else { return false }

        var keyEvent = event.ghosttyKeyEvent(action, translationMods: translationEvent?.modifierFlags)
        keyEvent.composing = composing

        // A lone control character is Ghostty's to encode; sending it as text
        // is what would turn ctrl+enter into plain Enter.
        if let text, !text.isEmpty, let first = text.utf8.first, first >= 0x20 {
            return text.withCString { ptr in
                keyEvent.text = ptr
                return ghostty_surface_key(surface, keyEvent)
            }
        }
        return ghostty_surface_key(surface, keyEvent)
    }

    // MARK: Mouse

    override func updateTrackingAreas() {
        trackingAreas.forEach { removeTrackingArea($0) }
        addTrackingArea(NSTrackingArea(
            rect: frame,
            // Mouse reports continue while the window is not key.
            options: [.mouseEnteredAndExited, .mouseMoved, .inVisibleRect, .activeAlways],
            owner: self,
            userInfo: nil))
        super.updateTrackingAreas()
    }

    override func resetCursorRects() {
        addCursorRect(bounds, cursor: pointerCursor)
    }

    func setCursorShape(_ shape: ghostty_action_mouse_shape_e) {
        guard let cursor = Ghostty.cursor(for: shape), cursor != pointerCursor else { return }
        pointerCursor = cursor
        window?.invalidateCursorRects(for: self)
    }

    /// Only mouse-hide-while-typing asks for this, so the pointer returns on
    /// its own at the next move.
    func setCursorVisibility(_ visible: Bool) {
        NSCursor.setHiddenUntilMouseMoves(!visible)
    }

    override func mouseDown(with event: NSEvent) {
        guard let surface else { return }
        ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_LEFT, Ghostty.ghosttyMods(event.modifierFlags))
    }

    override func mouseUp(with event: NSEvent) {
        if suppressNextLeftMouseUp {
            suppressNextLeftMouseUp = false
            return
        }
        guard let surface else { return }
        ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_LEFT, Ghostty.ghosttyMods(event.modifierFlags))
        ghostty_surface_mouse_pressure(surface, 0, 0)
    }

    override func otherMouseDown(with event: NSEvent) {
        guard let surface else { return }
        let button = Ghostty.mouseButton(fromNSEventButtonNumber: event.buttonNumber)
        ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_PRESS, button, Ghostty.ghosttyMods(event.modifierFlags))
    }

    override func otherMouseUp(with event: NSEvent) {
        guard let surface else { return }
        let button = Ghostty.mouseButton(fromNSEventButtonNumber: event.buttonNumber)
        ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_RELEASE, button, Ghostty.ghosttyMods(event.modifierFlags))
    }

    override func rightMouseDown(with event: NSEvent) {
        guard let surface,
              ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_RIGHT, Ghostty.ghosttyMods(event.modifierFlags))
        else { return super.rightMouseDown(with: event) }
    }

    override func rightMouseUp(with event: NSEvent) {
        guard let surface,
              ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_RIGHT, Ghostty.ghosttyMods(event.modifierFlags))
        else { return super.rightMouseUp(with: event) }
    }

    override func mouseEntered(with event: NSEvent) {
        super.mouseEntered(with: event)
        // Exiting parks the position at -1,-1; mouse reporting depends on it
        // being back inside the viewport.
        sendMousePosition(event)
    }

    override func mouseExited(with event: NSEvent) {
        guard let surface else { return }
        // A drag keeps reporting past the edge through mouseDragged.
        if NSEvent.pressedMouseButtons != 0 { return }
        ghostty_surface_mouse_pos(surface, -1, -1, Ghostty.ghosttyMods(event.modifierFlags))
    }

    override func mouseMoved(with event: NSEvent) {
        sendMousePosition(event)
    }

    override func mouseDragged(with event: NSEvent) {
        sendMousePosition(event)
    }

    override func rightMouseDragged(with event: NSEvent) {
        sendMousePosition(event)
    }

    override func otherMouseDragged(with event: NSEvent) {
        sendMousePosition(event)
    }

    /// Ghostty's origin is the top left, in points.
    private func sendMousePosition(_ event: NSEvent) {
        guard let surface else { return }
        let pos = convert(event.locationInWindow, from: nil)
        ghostty_surface_mouse_pos(surface, pos.x, frame.height - pos.y, Ghostty.ghosttyMods(event.modifierFlags))
    }

    override func scrollWheel(with event: NSEvent) {
        guard let surface else { return }
        var x = event.scrollingDeltaX
        var y = event.scrollingDeltaY
        let precision = event.hasPreciseScrollingDeltas
        if precision {
            // Upstream's subjective 2x for trackpads.
            x *= 2
            y *= 2
        }
        ghostty_surface_mouse_scroll(surface, x, y, Ghostty.scrollMods(precision: precision, momentum: event.momentumPhase))
    }

    override func pressureChange(with event: NSEvent) {
        guard let surface else { return }
        ghostty_surface_mouse_pressure(surface, UInt32(event.stage), Double(event.pressure))
    }

    // MARK: Edit menu

    @IBAction func copy(_ sender: Any?) {
        performBindingAction("copy_to_clipboard")
    }

    @IBAction func paste(_ sender: Any?) {
        performBindingAction("paste_from_clipboard")
    }

    @IBAction override func selectAll(_ sender: Any?) {
        performBindingAction("select_all")
    }

    // MARK: View menu

    /// Zooms the running session only; the default zoom is Preferences'.
    @IBAction func zoomIn(_ sender: Any?) {
        performBindingAction("increase_font_size:1")
    }

    @IBAction func zoomOut(_ sender: Any?) {
        performBindingAction("decrease_font_size:1")
    }

    /// Back to the default zoom.
    @IBAction func resetZoom(_ sender: Any?) {
        performBindingAction("reset_font_size")
    }

    /// Runs a Ghostty keybind action by name, as the menu's items do.
    @discardableResult
    func performBindingAction(_ action: String) -> Bool {
        guard let surface else { return false }
        let performed = ghostty_surface_binding_action(surface, action, UInt(action.utf8.count))
        if !performed {
            Ghostty.logger.warning("binding action failed: \(action, privacy: .public)")
        }
        return performed
    }
}

extension Ghostty.SurfaceView: NSMenuItemValidation {
    func validateMenuItem(_ item: NSMenuItem) -> Bool {
        switch item.action {
        case #selector(copy(_:)):
            guard let surface else { return false }
            return ghostty_surface_has_selection(surface)
        default:
            return true
        }
    }
}

// MARK: - NSTextInputClient

extension Ghostty.SurfaceView: NSTextInputClient {
    func hasMarkedText() -> Bool {
        markedText.length > 0
    }

    func markedRange() -> NSRange {
        guard markedText.length > 0 else { return NSRange() }
        return NSRange(location: 0, length: markedText.length)
    }

    func selectedRange() -> NSRange {
        guard let surface else { return NSRange() }
        var text = ghostty_text_s()
        guard ghostty_surface_read_selection(surface, &text) else { return NSRange() }
        defer { ghostty_surface_free_text(surface, &text) }
        return NSRange(location: Int(text.offset_start), length: Int(text.offset_len))
    }

    func setMarkedText(_ string: Any, selectedRange: NSRange, replacementRange: NSRange) {
        switch string {
        case let value as NSAttributedString:
            markedText = NSMutableAttributedString(attributedString: value)
        case let value as String:
            markedText = NSMutableAttributedString(string: value)
        default:
            return
        }

        // Outside keyDown (a layout switch mid dead key) the preedit is synced
        // here, since no key event will.
        if keyTextAccumulator == nil {
            syncPreedit()
        }
    }

    func unmarkText() {
        guard markedText.length > 0 else { return }
        markedText.mutableString.setString("")
        syncPreedit()
    }

    func validAttributesForMarkedText() -> [NSAttributedString.Key] {
        []
    }

    func attributedSubstring(forProposedRange range: NSRange, actualRange: NSRangePointer?) -> NSAttributedString? {
        guard let surface, range.length > 0 else { return nil }
        // System callers ask for ranges that match nothing; upstream answers
        // every one with the selection.
        var text = ghostty_text_s()
        guard ghostty_surface_read_selection(surface, &text) else { return nil }
        defer { ghostty_surface_free_text(surface, &text) }
        return NSAttributedString(string: String(cString: text.text))
    }

    func characterIndex(for point: NSPoint) -> Int {
        0
    }

    /// Where the input method draws its candidate window: Ghostty's cursor.
    func firstRect(forCharacterRange range: NSRange, actualRange: NSRangePointer?) -> NSRect {
        guard let surface else {
            return NSRect(x: frame.origin.x, y: frame.origin.y, width: 0, height: 0)
        }

        let cell = cellSize
        var x: Double = 0
        var y: Double = 0
        var width: Double = cell.width
        var height: Double = cell.height
        ghostty_surface_ime_point(surface, &x, &y, &width, &height)
        if range.length == 0, width > 0 {
            // Dictation's indicator wants a zero-width rect at the insertion point.
            width = 0
            x += cell.width * Double(range.location + range.length)
        }

        let viewRect = NSRect(x: x, y: frame.size.height - y, width: width, height: max(height, cell.height))
        let windowRect = convert(viewRect, to: nil)
        guard let window else { return windowRect }
        return window.convertToScreen(windowRect)
    }

    func insertText(_ string: Any, replacementRange: NSRange) {
        guard NSApp.currentEvent != nil, let surface else { return }

        let chars: String
        switch string {
        case let value as NSAttributedString: chars = value.string
        case let value as String: chars = value
        default: return
        }

        // A commit ends the composition.
        unmarkText()

        // Inside keyDown the text rides the key event.
        if keyTextAccumulator != nil {
            keyTextAccumulator?.append(chars)
            return
        }

        let len = chars.utf8CString.count
        guard len > 1 else { return }
        chars.withCString { ptr in
            ghostty_surface_text(surface, ptr, UInt(len - 1))
        }
    }

    /// Unimplemented text commands would beep; a Cmd chord AppKit declined
    /// comes back here and is redispatched so `keyDown` encodes it.
    override func doCommand(by selector: Selector) {
        if let last = lastPerformKeyEvent,
           let current = NSApp.currentEvent,
           last == current.timestamp {
            NSApp.sendEvent(current)
            return
        }

        switch selector {
        case #selector(moveToBeginningOfDocument(_:)):
            performBindingAction("scroll_to_top")
        case #selector(moveToEndOfDocument(_:)):
            performBindingAction("scroll_to_bottom")
        default:
            break
        }
    }

    /// Mirrors `markedText` to the surface's preedit.
    func syncPreedit(clearIfNeeded: Bool = true) {
        guard let surface else { return }
        if markedText.length > 0 {
            let str = markedText.string
            let len = str.utf8CString.count
            str.withCString { ptr in
                ghostty_surface_preedit(surface, ptr, UInt(len - 1))
            }
        } else if clearIfNeeded {
            ghostty_surface_preedit(surface, nil, 0)
        }
    }

    /// One cell in points, from the surface's pixel metrics.
    var cellSize: NSSize {
        guard let surface else { return .zero }
        let size = ghostty_surface_size(surface)
        let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 1
        return NSSize(width: Double(size.cell_width_px) / scale, height: Double(size.cell_height_px) / scale)
    }
}
