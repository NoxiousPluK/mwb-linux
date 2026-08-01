//go:build linux && wayland_portal

package capture

import "testing"

// threeMonitorZones mirrors the real layout this backend was validated
// against live: a 1920x1080 output flush-right of two 2560x1440 outputs,
// no gaps — C[0,2559] B[2560,5119] A[5120,7039] horizontally.
func threeMonitorZones() []zoneRect {
	return []zoneRect{
		{Width: 1920, Height: 1080, X: 5120, Y: 0}, // A, rightmost, shorter
		{Width: 2560, Height: 1440, X: 2560, Y: 0}, // B
		{Width: 2560, Height: 1440, X: 0, Y: 0},    // C, leftmost, tallest
	}
}

func TestOuterEdgeBarrier_Left(t *testing.T) {
	x1, y1, x2, y2, err := outerEdgeBarrier(threeMonitorZones(), "left")
	if err != nil {
		t.Fatalf("outerEdgeBarrier: %v", err)
	}
	// Left edge: inclusive at zone.X (0), spanning the leftmost zone's own
	// inclusive-pixel height (0 to 1439), confirmed live against KWin 6.7.3.
	if x1 != 0 || x2 != 0 || y1 != 0 || y2 != 1439 {
		t.Errorf("left barrier = (%d,%d)-(%d,%d), want (0,0)-(0,1439)", x1, y1, x2, y2)
	}
}

func TestOuterEdgeBarrier_Right(t *testing.T) {
	x1, y1, x2, y2, err := outerEdgeBarrier(threeMonitorZones(), "right")
	if err != nil {
		t.Fatalf("outerEdgeBarrier: %v", err)
	}
	// Right edge: exclusive at zone.X+Width (7040, one past the last pixel
	// column), spanning the rightmost zone's own inclusive-pixel height (0
	// to 1079) — confirmed live. This asymmetry (inclusive left, exclusive
	// right) is not documented anywhere; it was reverse-engineered by
	// testing both edges against a real compositor.
	if x1 != 7040 || x2 != 7040 || y1 != 0 || y2 != 1079 {
		t.Errorf("right barrier = (%d,%d)-(%d,%d), want (7040,0)-(7040,1079)", x1, y1, x2, y2)
	}
}

func TestOuterEdgeBarrier_NoZones(t *testing.T) {
	if _, _, _, _, err := outerEdgeBarrier(nil, "left"); err == nil {
		t.Error("expected an error with no zones reported")
	}
}

func TestOuterEdgeBarrier_UnknownSide(t *testing.T) {
	if _, _, _, _, err := outerEdgeBarrier(threeMonitorZones(), "up"); err == nil {
		t.Error("expected an error for an unrecognized edge side")
	}
}

func TestOuterEdgeBarrier_SingleMonitor(t *testing.T) {
	zones := []zoneRect{{Width: 1920, Height: 1080, X: 0, Y: 0}}
	x1, y1, x2, y2, err := outerEdgeBarrier(zones, "left")
	if err != nil {
		t.Fatalf("outerEdgeBarrier: %v", err)
	}
	if x1 != 0 || x2 != 0 || y1 != 0 || y2 != 1079 {
		t.Errorf("single-monitor left barrier = (%d,%d)-(%d,%d), want (0,0)-(0,1079)", x1, y1, x2, y2)
	}

	x1, y1, x2, y2, err = outerEdgeBarrier(zones, "right")
	if err != nil {
		t.Fatalf("outerEdgeBarrier: %v", err)
	}
	if x1 != 1920 || x2 != 1920 || y1 != 0 || y2 != 1079 {
		t.Errorf("single-monitor right barrier = (%d,%d)-(%d,%d), want (1920,0)-(1920,1079)", x1, y1, x2, y2)
	}
}
