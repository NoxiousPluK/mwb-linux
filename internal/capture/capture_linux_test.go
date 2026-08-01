//go:build linux

package capture

import (
	"context"
	"testing"
	"time"
)

// fakeEdgeSource is a test double for EdgeSource: records calls instead of
// touching real hardware or a display server.
type fakeEdgeSource struct {
	activatedCount int
	reenterCalls   [][2]int32
	reenterErr     error
}

func (f *fakeEdgeSource) Run(ctx context.Context, _ func(y int32)) error {
	<-ctx.Done()
	return nil
}

func (f *fakeEdgeSource) Reenter(x, y int32) error {
	f.reenterCalls = append(f.reenterCalls, [2]int32{x, y})
	return f.reenterErr
}

func (f *fakeEdgeSource) NotifyActivated() { f.activatedCount++ }

func (f *fakeEdgeSource) Close() error { return nil }

// --- applyAcceleration ---

func TestApplyAcceleration_ZeroDelta(t *testing.T) {
	if got := applyAcceleration(0, defaultAccelMultiplier); got != 0 {
		t.Errorf("applyAcceleration(0) = %d, want 0", got)
	}
}

func TestApplyAcceleration_SmallPositive(t *testing.T) {
	// Values < 1 after scaling should be clamped to 1
	if got := applyAcceleration(1, defaultAccelMultiplier); got < 1 {
		t.Errorf("applyAcceleration(1) = %d, should be >= 1", got)
	}
}

func TestApplyAcceleration_SmallNegative(t *testing.T) {
	if got := applyAcceleration(-1, defaultAccelMultiplier); got > -1 {
		t.Errorf("applyAcceleration(-1) = %d, should be <= -1", got)
	}
}

func TestApplyAcceleration_Symmetry(t *testing.T) {
	for _, delta := range []int32{1, 5, 10, 100} {
		pos := applyAcceleration(delta, defaultAccelMultiplier)
		neg := applyAcceleration(-delta, defaultAccelMultiplier)
		if pos != -neg {
			t.Errorf("acceleration not symmetric: applyAcceleration(%d)=%d, applyAcceleration(%d)=%d",
				delta, pos, -delta, neg)
		}
	}
}

func TestApplyAcceleration_Multiplier(t *testing.T) {
	// Multiplier scales linearly above the sub-pixel clamp.
	if got := applyAcceleration(10, 1.0); got != 10 {
		t.Errorf("applyAcceleration(10, 1.0) = %d, want 10", got)
	}
	if got := applyAcceleration(10, 3.0); got != 30 {
		t.Errorf("applyAcceleration(10, 3.0) = %d, want 30", got)
	}
	// A fractional multiplier still moves at least 1px for a unit delta.
	if got := applyAcceleration(1, 0.5); got != 1 {
		t.Errorf("applyAcceleration(1, 0.5) = %d, want 1 (sub-pixel clamp)", got)
	}
}

// --- SafeEntryPosition ---

func TestSafeEntryPosition_LeftEdge(t *testing.T) {
	c := &Capturer{screen: ScreenInfo{Width: 2560, Height: 1440}, edgeSide: "left"}
	x, y := c.SafeEntryPosition()
	// Must be 100px from left edge — not at x=0 which immediately re-triggers switch
	if x < 50 {
		t.Errorf("left edge: x=%d too close to edge, cursor will re-trigger switch", x)
	}
	// Y should be somewhere reasonable (not 0, not at edge)
	if y <= 0 || y >= 1440 {
		t.Errorf("left edge: y=%d out of screen bounds", y)
	}
}

func TestSafeEntryPosition_RightEdge(t *testing.T) {
	c := &Capturer{screen: ScreenInfo{Width: 2560, Height: 1440}, edgeSide: "right"}
	x, y := c.SafeEntryPosition()
	// Must be 100px from right edge
	if x > 2560-50 {
		t.Errorf("right edge: x=%d too close to right edge, cursor will re-trigger switch", x)
	}
	if y <= 0 || y >= 1440 {
		t.Errorf("right edge: y=%d out of screen bounds", y)
	}
}

func TestAcceptsReclaim_LeftEdgeOnlyAcceptsLocalLeftLanding(t *testing.T) {
	c := &Capturer{edgeSide: "left"}
	if !c.AcceptsReclaim(1200) {
		t.Fatal("left-edge setup should accept a NextMachine landing near local left edge")
	}
	if c.AcceptsReclaim(65000) {
		t.Fatal("left-edge setup must reject far-right landing from the remote's far-left edge")
	}
}

func TestAcceptsReclaim_RightEdgeOnlyAcceptsLocalRightLanding(t *testing.T) {
	c := &Capturer{edgeSide: "right"}
	if !c.AcceptsReclaim(65000) {
		t.Fatal("right-edge setup should accept a NextMachine landing near local right edge")
	}
	if c.AcceptsReclaim(1200) {
		t.Fatal("right-edge setup must reject far-left landing from the remote's far-right edge")
	}
}

func TestAcceptsActivation_LeftEdgeOnlyAcceptsRemoteRightEdge(t *testing.T) {
	c := &Capturer{active: false, edgeSide: "left", remoteW: 1920, remoteX: 1919}
	if !c.AcceptsActivation() {
		t.Fatal("left-edge setup should accept MachineSwitched from remote right/shared edge")
	}
	c.remoteX = 1720
	if c.AcceptsActivation() {
		t.Fatal("left-edge setup must reject MachineSwitched at the 200px remote entry offset")
	}
	c.remoteX = 0
	if c.AcceptsActivation() {
		t.Fatal("left-edge setup must reject MachineSwitched from remote far-left edge")
	}
}

func TestAcceptsActivation_RightEdgeOnlyAcceptsRemoteLeftEdge(t *testing.T) {
	c := &Capturer{active: false, edgeSide: "right", remoteW: 1920, remoteX: 0}
	if !c.AcceptsActivation() {
		t.Fatal("right-edge setup should accept MachineSwitched from remote left/shared edge")
	}
	c.remoteX = 200
	if c.AcceptsActivation() {
		t.Fatal("right-edge setup must reject MachineSwitched at the 200px remote entry offset")
	}
	c.remoteX = 1919
	if c.AcceptsActivation() {
		t.Fatal("right-edge setup must reject MachineSwitched from remote far-right edge")
	}
}

func TestAcceptsActivation_AcceptsAfterRetryTimeout(t *testing.T) {
	c := &Capturer{active: false, edgeSide: "left", remoteW: 1920, remoteX: 0}
	if c.AcceptsActivation() {
		t.Fatal("first rejection should still reject (protects against a same-instant spurious bounce)")
	}
	if c.activationRejectedSince.IsZero() {
		t.Fatal("a rejection should start the retry-timeout clock")
	}

	// Still within the timeout window and the shadow position hasn't moved:
	// must keep rejecting.
	if c.AcceptsActivation() {
		t.Fatal("should keep rejecting while within activationRetryTimeout")
	}

	// Simulate the timeout having elapsed (Windows kept retrying past
	// activationRetryTimeout — a stuck return, not a spurious one).
	c.activationRejectedSince = time.Now().Add(-activationRetryTimeout - time.Second)
	if !c.AcceptsActivation() {
		t.Fatal("should accept once the remote has been retrying past activationRetryTimeout")
	}
	if !c.activationRejectedSince.IsZero() {
		t.Fatal("accepting should reset the rejection-streak clock")
	}
}

func TestAcceptsActivation_OnSharedEdgeResetsRejectionStreak(t *testing.T) {
	c := &Capturer{active: false, edgeSide: "left", remoteW: 1920, remoteX: 0}
	if c.AcceptsActivation() {
		t.Fatal("expected initial rejection")
	}
	if c.activationRejectedSince.IsZero() {
		t.Fatal("expected a rejection streak to have started")
	}

	// The shadow position now legitimately reaches the shared edge — should
	// accept immediately and clear the streak, not wait out the timeout.
	c.remoteX = 1919
	if !c.AcceptsActivation() {
		t.Fatal("expected acceptance once remoteX reaches the shared edge")
	}
	if !c.activationRejectedSince.IsZero() {
		t.Fatal("accepting on a shared-edge match should reset the rejection streak")
	}
}

// --- SetActive mutex invariant ---

// SetActive must NOT hold c.mu when calling applyIsolation.
// applyIsolation acquires c.mu internally, so holding it in SetActive causes deadlock.
// This test catches that regression by running SetActive with a timeout.
func TestSetActive_NoDeadlockOnActivate(t *testing.T) {
	c := &Capturer{
		active:   false,
		stopCh:   make(chan struct{}),
		remoteW:  1920,
		remoteH:  1080,
		edgeSide: "left",
	}
	c.screen = ScreenInfo{Width: 1920, Height: 1080}

	done := make(chan struct{})
	go func() {
		c.SetActive(true)
		close(done)
	}()

	select {
	case <-done:
		// pass — no deadlock
	case <-time.After(3 * time.Second):
		t.Fatal("SetActive deadlocked — check that applyIsolation() is called AFTER c.mu.Unlock()")
	}
}

func TestSetActive_ResetsGatesOnActivate(t *testing.T) {
	edge := &fakeEdgeSource{}
	c := &Capturer{
		active:     false,
		canReturn:  true,
		stopCh:     make(chan struct{}),
		remoteW:    1920,
		remoteH:    1080,
		edgeSource: edge,
	}
	c.SetActive(true)

	c.mu.Lock()
	cr := c.canReturn
	c.mu.Unlock()

	// canReturn must be reset on activation — prevents ghost bounce on
	// reconnect. The edge-switch re-arm gate now lives inside the
	// EdgeSource; SetActive notifies it via NotifyActivated instead of
	// touching a shared field directly, so the switch-out debounce doesn't
	// race the caller's cursor-reentry warp.
	if cr {
		t.Error("canReturn must be false after SetActive(true) — prevents ghost bounce on reconnect")
	}
	if edge.activatedCount != 1 {
		t.Errorf("edgeSource.NotifyActivated called %d times, want 1", edge.activatedCount)
	}
}

func TestSetActive_NoOpWhenAlreadyActive(t *testing.T) {
	c := &Capturer{
		active:  true,
		stopCh:  make(chan struct{}),
		remoteW: 1920,
		remoteH: 1080,
	}
	// Should not deadlock, should not panic
	done := make(chan struct{})
	go func() {
		c.SetActive(true) // already active — should be a no-op
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SetActive(true) on already-active Capturer deadlocked")
	}
}

// --- Reenter passthrough ---

func TestReenter_NoEdgeSourceConfigured(t *testing.T) {
	c := &Capturer{}
	if err := c.Reenter(100, 100); err == nil {
		t.Error("Reenter should error when Run hasn't set up an edgeSource yet")
	}
}

func TestReenter_DelegatesToEdgeSource(t *testing.T) {
	edge := &fakeEdgeSource{}
	c := &Capturer{edgeSource: edge}
	if err := c.Reenter(42, 7); err != nil {
		t.Fatalf("Reenter returned error: %v", err)
	}
	if len(edge.reenterCalls) != 1 || edge.reenterCalls[0] != [2]int32{42, 7} {
		t.Errorf("edgeSource.Reenter calls = %v, want [[42 7]]", edge.reenterCalls)
	}
}

// --- canReturn gate ---
//
// The canSwitch/edgeZone re-arm gate now lives in xdotoolEdgeSource (see
// edgesource_xdotool_linux_test.go for shouldArm/crossedEdge coverage).
