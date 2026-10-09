// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import Carbon
import GhosttyKit

extension Ghostty {
    /// Translates AppKit modifier flags to libghostty's, sided bits included.
    static func ghosttyMods(_ flags: NSEvent.ModifierFlags) -> ghostty_input_mods_e {
        var mods: UInt32 = GHOSTTY_MODS_NONE.rawValue

        if flags.contains(.shift) { mods |= GHOSTTY_MODS_SHIFT.rawValue }
        if flags.contains(.control) { mods |= GHOSTTY_MODS_CTRL.rawValue }
        if flags.contains(.option) { mods |= GHOSTTY_MODS_ALT.rawValue }
        if flags.contains(.command) { mods |= GHOSTTY_MODS_SUPER.rawValue }
        if flags.contains(.capsLock) { mods |= GHOSTTY_MODS_CAPS.rawValue }

        // Ghostty cannot represent both sides held at once; it never needs to.
        let rawFlags = flags.rawValue
        if rawFlags & UInt(NX_DEVICERSHIFTKEYMASK) != 0 { mods |= GHOSTTY_MODS_SHIFT_RIGHT.rawValue }
        if rawFlags & UInt(NX_DEVICERCTLKEYMASK) != 0 { mods |= GHOSTTY_MODS_CTRL_RIGHT.rawValue }
        if rawFlags & UInt(NX_DEVICERALTKEYMASK) != 0 { mods |= GHOSTTY_MODS_ALT_RIGHT.rawValue }
        if rawFlags & UInt(NX_DEVICERCMDKEYMASK) != 0 { mods |= GHOSTTY_MODS_SUPER_RIGHT.rawValue }

        return ghostty_input_mods_e(mods)
    }

    /// The inverse of `ghosttyMods` for the four translating modifiers.
    static func eventModifierFlags(mods: ghostty_input_mods_e) -> NSEvent.ModifierFlags {
        var flags = NSEvent.ModifierFlags(rawValue: 0)
        if mods.rawValue & GHOSTTY_MODS_SHIFT.rawValue != 0 { flags.insert(.shift) }
        if mods.rawValue & GHOSTTY_MODS_CTRL.rawValue != 0 { flags.insert(.control) }
        if mods.rawValue & GHOSTTY_MODS_ALT.rawValue != 0 { flags.insert(.option) }
        if mods.rawValue & GHOSTTY_MODS_SUPER.rawValue != 0 { flags.insert(.command) }
        return flags
    }

    /// The press or release a `flagsChanged` event stands for, or nil when its
    /// key is not a modifier. Pressing one side while the other is already
    /// held reads as a release of the side that moved, as upstream decides it.
    static func modifierKeyAction(
        keyCode: UInt16,
        flags: NSEvent.ModifierFlags
    ) -> ghostty_input_action_e? {
        let mod: UInt32
        switch keyCode {
        case 0x39: mod = GHOSTTY_MODS_CAPS.rawValue
        case 0x38, 0x3C: mod = GHOSTTY_MODS_SHIFT.rawValue
        case 0x3B, 0x3E: mod = GHOSTTY_MODS_CTRL.rawValue
        case 0x3A, 0x3D: mod = GHOSTTY_MODS_ALT.rawValue
        case 0x37, 0x36: mod = GHOSTTY_MODS_SUPER.rawValue
        default: return nil
        }

        guard ghosttyMods(flags).rawValue & mod != 0 else { return GHOSTTY_ACTION_RELEASE }

        let raw = flags.rawValue
        let sidePressed: Bool
        switch keyCode {
        case 0x3C: sidePressed = raw & UInt(NX_DEVICERSHIFTKEYMASK) != 0
        case 0x3E: sidePressed = raw & UInt(NX_DEVICERCTLKEYMASK) != 0
        case 0x3D: sidePressed = raw & UInt(NX_DEVICERALTKEYMASK) != 0
        case 0x36: sidePressed = raw & UInt(NX_DEVICERCMDKEYMASK) != 0
        default: sidePressed = true
        }
        return sidePressed ? GHOSTTY_ACTION_PRESS : GHOSTTY_ACTION_RELEASE
    }

    /// libghostty's button for `NSEvent.buttonNumber`: 3 and 4 are the back
    /// and forward buttons, which X11 numbering calls eight and nine.
    static func mouseButton(fromNSEventButtonNumber buttonNumber: Int) -> ghostty_input_mouse_button_e {
        switch buttonNumber {
        case 0: GHOSTTY_MOUSE_LEFT
        case 1: GHOSTTY_MOUSE_RIGHT
        case 2: GHOSTTY_MOUSE_MIDDLE
        case 3: GHOSTTY_MOUSE_EIGHT
        case 4: GHOSTTY_MOUSE_NINE
        case 5: GHOSTTY_MOUSE_SIX
        case 6: GHOSTTY_MOUSE_SEVEN
        case 7: GHOSTTY_MOUSE_FOUR
        case 8: GHOSTTY_MOUSE_FIVE
        case 9: GHOSTTY_MOUSE_TEN
        case 10: GHOSTTY_MOUSE_ELEVEN
        default: GHOSTTY_MOUSE_UNKNOWN
        }
    }

    /// The packed `ScrollMods`: bit 0 precision, bits 1-3 the momentum phase.
    static func scrollMods(precision: Bool, momentum: NSEvent.Phase) -> ghostty_input_scroll_mods_t {
        let phase: ghostty_input_mouse_momentum_e
        switch momentum {
        case .began: phase = GHOSTTY_MOUSE_MOMENTUM_BEGAN
        case .stationary: phase = GHOSTTY_MOUSE_MOMENTUM_STATIONARY
        case .changed: phase = GHOSTTY_MOUSE_MOMENTUM_CHANGED
        case .ended: phase = GHOSTTY_MOUSE_MOMENTUM_ENDED
        case .cancelled: phase = GHOSTTY_MOUSE_MOMENTUM_CANCELLED
        case .mayBegin: phase = GHOSTTY_MOUSE_MOMENTUM_MAY_BEGIN
        default: phase = GHOSTTY_MOUSE_MOMENTUM_NONE
        }
        var value: Int32 = precision ? 0b1 : 0
        value |= Int32(phase.rawValue) << 1
        return value
    }

    /// The AppKit pointer for a `GHOSTTY_ACTION_MOUSE_SHAPE`, or nil for a
    /// shape this pin does not know, which leaves the current one in place.
    static func cursor(for shape: ghostty_action_mouse_shape_e) -> NSCursor? {
        switch shape {
        case GHOSTTY_MOUSE_SHAPE_DEFAULT: .arrow
        case GHOSTTY_MOUSE_SHAPE_TEXT: .iBeam
        case GHOSTTY_MOUSE_SHAPE_VERTICAL_TEXT: .iBeamCursorForVerticalLayout
        case GHOSTTY_MOUSE_SHAPE_GRAB: .openHand
        case GHOSTTY_MOUSE_SHAPE_GRABBING: .closedHand
        case GHOSTTY_MOUSE_SHAPE_POINTER: .pointingHand
        case GHOSTTY_MOUSE_SHAPE_W_RESIZE: .resizeLeft
        case GHOSTTY_MOUSE_SHAPE_E_RESIZE: .resizeRight
        case GHOSTTY_MOUSE_SHAPE_N_RESIZE: .resizeUp
        case GHOSTTY_MOUSE_SHAPE_S_RESIZE: .resizeDown
        case GHOSTTY_MOUSE_SHAPE_NS_RESIZE: .resizeUpDown
        case GHOSTTY_MOUSE_SHAPE_EW_RESIZE: .resizeLeftRight
        case GHOSTTY_MOUSE_SHAPE_CONTEXT_MENU: .contextualMenu
        case GHOSTTY_MOUSE_SHAPE_CROSSHAIR: .crosshair
        case GHOSTTY_MOUSE_SHAPE_NOT_ALLOWED: .operationNotAllowed
        default: nil
        }
    }
}

extension NSEvent {
    /// A libghostty key event for this event, without `text` or `composing`,
    /// whose lifetimes the caller owns.
    ///
    /// `translationMods` are the modifiers that actually translated the text
    /// when the surface altered them (option-as-alt).
    func ghosttyKeyEvent(
        _ action: ghostty_input_action_e,
        translationMods: NSEvent.ModifierFlags? = nil
    ) -> ghostty_input_key_s {
        var keyEvent = ghostty_input_key_s()
        keyEvent.action = action
        keyEvent.keycode = UInt32(keyCode)
        keyEvent.text = nil
        keyEvent.composing = false

        // macOS reports no consumed modifiers. Upstream's heuristic: control
        // and command never translate text, and everything else did. This is
        // what lets the kitty encoder tell ctrl+m from Enter and ctrl+i from Tab.
        keyEvent.mods = Ghostty.ghosttyMods(modifierFlags)
        keyEvent.consumed_mods = Ghostty.ghosttyMods(
            (translationMods ?? modifierFlags).subtracting([.control, .command]))

        // `byApplyingModifiers: []`, not `charactersIgnoringModifiers`, which
        // changes meaning under control.
        keyEvent.unshifted_codepoint = 0
        if type == .keyDown || type == .keyUp,
           let chars = characters(byApplyingModifiers: []),
           let codepoint = chars.unicodeScalars.first {
            keyEvent.unshifted_codepoint = codepoint.value
        }

        return keyEvent
    }

    /// The text to hand libghostty: a lone control character is replaced by
    /// its uncontrolled key, since Ghostty's encoder applies control itself,
    /// and function keys' private-use characters are withheld.
    var ghosttyCharacters: String? {
        guard let characters else { return nil }

        if characters.count == 1, let scalar = characters.unicodeScalars.first {
            if scalar.value < 0x20 {
                return self.characters(byApplyingModifiers: modifierFlags.subtracting(.control))
            }
            if scalar.value >= 0xF700 && scalar.value <= 0xF8FF {
                return nil
            }
        }

        return characters
    }
}

enum KeyboardLayout {
    /// The current keyboard input source's ID; a change across one keyDown
    /// means an input method consumed the key to switch layouts.
    static var id: String? {
        guard let source = TISCopyCurrentKeyboardInputSource()?.takeRetainedValue(),
              let pointer = TISGetInputSourceProperty(source, kTISPropertyInputSourceID)
        else { return nil }
        return Unmanaged<CFString>.fromOpaque(pointer).takeUnretainedValue() as String
    }
}
