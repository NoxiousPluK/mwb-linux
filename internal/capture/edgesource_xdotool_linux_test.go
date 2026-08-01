//go:build linux

package capture

import "testing"

func TestShouldArm_LeftEdge(t *testing.T) {
	if shouldArm("left", 0, 2560) {
		t.Error("should not arm at x=0 (the edge)")
	}
	if shouldArm("left", 20, 2560) {
		t.Error("should not arm exactly at the edgeZone boundary")
	}
	if !shouldArm("left", 100, 2560) {
		t.Error("should arm once the cursor is well inside the edge zone")
	}
}

func TestShouldArm_RightEdge(t *testing.T) {
	if shouldArm("right", 2559, 2560) {
		t.Error("should not arm at the right edge")
	}
	if !shouldArm("right", 100, 2560) {
		t.Error("should arm once the cursor is well inside the edge zone")
	}
}

func TestCrossedEdge_LeftEdge(t *testing.T) {
	if !crossedEdge("left", 0, 2560) {
		t.Error("x=0 should count as crossing the left edge")
	}
	if !crossedEdge("left", 1, 2560) {
		t.Error("x=1 should count as crossing the left edge")
	}
	if crossedEdge("left", 2, 2560) {
		t.Error("x=2 should not yet count as crossing the left edge")
	}
}

func TestCrossedEdge_RightEdge(t *testing.T) {
	if !crossedEdge("right", 2559, 2560) {
		t.Error("x=width-1 should count as crossing the right edge")
	}
	if crossedEdge("right", 2000, 2560) {
		t.Error("x well inside the screen should not count as crossing the right edge")
	}
}

func TestNewXdotoolEdgeSource_AllowsFirstSwitchImmediately(t *testing.T) {
	s := newXdotoolEdgeSource(ScreenInfo{Width: 2560, Height: 1440}, "left")
	if !s.canSwitch.Load() {
		t.Error("a freshly constructed xdotoolEdgeSource should allow the first switch immediately")
	}
}

func TestXdotoolEdgeSource_NotifyActivatedDisarms(t *testing.T) {
	s := newXdotoolEdgeSource(ScreenInfo{Width: 2560, Height: 1440}, "left")
	s.NotifyActivated()
	if s.canSwitch.Load() {
		t.Error("NotifyActivated should disarm canSwitch so a stale edge position doesn't immediately re-trigger")
	}
}
