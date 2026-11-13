package input

// extendedKeys are the evdev codes whose Windows counterpart is an "extended"
// key, i.e. one whose real scan code is prefixed with 0xE0.
//
// This has to be keyed on the evdev code rather than the VK code. Several
// extended keys share a VK with a non-extended twin — the arrows report the
// same VK as their numeric-keypad positions, and right Ctrl/Alt share a VK
// with the left ones — so the VK alone cannot tell them apart.
var extendedKeys = map[uint16]bool{
	// Navigation cluster. Without the extended bit these resolve to their
	// numeric-keypad twins, where Shift suppresses selection instead of
	// extending it.
	KEY_UP:       true,
	KEY_DOWN:     true,
	KEY_LEFT:     true,
	KEY_RIGHT:    true,
	KEY_HOME:     true,
	KEY_END:      true,
	KEY_PAGEUP:   true,
	KEY_PAGEDOWN: true,
	KEY_INSERT:   true,
	KEY_DELETE:   true,

	// Right-hand modifiers. The receive path already uses the extended bit to
	// pick right over left, so setting it here makes the two directions agree.
	KEY_RIGHTCTRL: true,
	KEY_RIGHTALT:  true,

	// Keypad keys that are themselves extended, plus the Meta/menu keys.
	KEY_KPENTER:   true,
	KEY_KPSLASH:   true,
	KEY_NUMLOCK:   true,
	KEY_LEFTMETA:  true,
	KEY_RIGHTMETA: true,
	KEY_COMPOSE:   true,
}

// IsExtendedKey reports whether an evdev key code must be sent to Windows with
// LLKHF_EXTENDED set.
func IsExtendedKey(code uint16) bool {
	return extendedKeys[code]
}
