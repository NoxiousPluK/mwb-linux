package capture

import (
	"testing"

	"github.com/lucky-verma/mwb-linux/internal/input"
	"github.com/lucky-verma/mwb-linux/internal/protocol"
)

func TestMouseButtonMessageXButtons(t *testing.T) {
	// BTN_SIDE/BTN_EXTRA were dropped outbound by a 0x110..0x112 range check.
	// The id travels in WheelDelta, which PowerToys feeds to mouseData.
	tests := []struct {
		name     string
		code     uint16
		value    int32
		flags    int32
		buttonID int32
	}{
		{"side down", input.BTN_SIDE, 1, protocol.WM_XBUTTONDOWN, 1},
		{"side up", input.BTN_SIDE, 0, protocol.WM_XBUTTONUP, 1},
		{"extra down", input.BTN_EXTRA, 1, protocol.WM_XBUTTONDOWN, 2},
		{"extra up", input.BTN_EXTRA, 0, protocol.WM_XBUTTONUP, 2},
		{"left down", input.BTN_LEFT, 1, protocol.WM_LBUTTONDOWN, 0},
		{"right up", input.BTN_RIGHT, 0, protocol.WM_RBUTTONUP, 0},
		{"middle down", input.BTN_MIDDLE, 1, protocol.WM_MBUTTONDOWN, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, id, ok := mouseButtonMessage(tt.code, tt.value)
			if !ok {
				t.Fatalf("mouseButtonMessage(%#x, %d) not ok", tt.code, tt.value)
			}
			if flags != tt.flags || id != tt.buttonID {
				t.Errorf("got flags=%#x id=%d, want flags=%#x id=%d", flags, id, tt.flags, tt.buttonID)
			}
		})
	}
}

func TestMouseButtonMessageIgnoresAutorepeat(t *testing.T) {
	// evdev value 2 is auto-repeat; a button has no meaningful repeat.
	if _, _, ok := mouseButtonMessage(input.BTN_LEFT, 2); ok {
		t.Error("autorepeat should not produce a button message")
	}
}

func TestMouseButtonMessageRejectsUnknownCode(t *testing.T) {
	if _, _, ok := mouseButtonMessage(0x115, 1); ok {
		t.Error("BTN_FORWARD is not mapped; want ok=false")
	}
}
