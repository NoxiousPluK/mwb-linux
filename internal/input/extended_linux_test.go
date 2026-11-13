package input

import "testing"

func TestIsExtendedKeyNavigationCluster(t *testing.T) {
	// These are the keys whose absent extended bit made Shift+Left navigate
	// instead of selecting: Windows resolved them as their keypad twins.
	for _, code := range []uint16{
		KEY_UP, KEY_DOWN, KEY_LEFT, KEY_RIGHT,
		KEY_HOME, KEY_END, KEY_PAGEUP, KEY_PAGEDOWN,
		KEY_INSERT, KEY_DELETE,
	} {
		if !IsExtendedKey(code) {
			t.Errorf("IsExtendedKey(%d) = false, want true", code)
		}
	}
}

func TestIsExtendedKeyRightModifiers(t *testing.T) {
	// The receive path distinguishes right from left by this bit, so the send
	// path has to set it for the two directions to agree.
	for _, code := range []uint16{KEY_RIGHTCTRL, KEY_RIGHTALT} {
		if !IsExtendedKey(code) {
			t.Errorf("IsExtendedKey(%d) = false, want true", code)
		}
	}
}

func TestIsExtendedKeyExcludesKeypadTwins(t *testing.T) {
	// The keypad digits share a VK with the arrows; marking them extended
	// would reintroduce the same ambiguity from the other side. Ordinary
	// character and left-modifier keys are not extended either.
	for _, code := range []uint16{
		KEY_KP4, KEY_KP8, KEY_KP6, KEY_KP2, KEY_KP0,
		KEY_KP7, KEY_KP1, KEY_KP9, KEY_KP3,
		KEY_A, KEY_LEFTCTRL, KEY_LEFTALT, KEY_LEFTSHIFT, KEY_ENTER,
	} {
		if IsExtendedKey(code) {
			t.Errorf("IsExtendedKey(%d) = true, want false", code)
		}
	}
}
