//go:build linux && wayland_portal

package capture

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestPortalEdgeSource_Manual drives the real InputCapture portal against
// whatever compositor is running this test. It's not part of the normal
// suite — it needs a live graphical session and a human moving the mouse —
// so it's opt-in via MWB_LIVE_PORTAL_TEST=1.
//
// Run it with: MWB_LIVE_PORTAL_TEST=1 go test -tags wayland_portal -run
// TestPortalEdgeSource_Manual ./internal/capture -v -timeout 5m
func TestPortalEdgeSource_Manual(t *testing.T) {
	if os.Getenv("MWB_LIVE_PORTAL_TEST") == "" {
		t.Skip("set MWB_LIVE_PORTAL_TEST=1 to run against a real compositor")
	}

	edgeSide := os.Getenv("MWB_LIVE_PORTAL_EDGE")
	if edgeSide == "" {
		edgeSide = "left"
	}

	t.Logf("creating portal session (edge=%s) — watch for KDE's permission dialog and approve it", edgeSide)
	src, err := newPortalEdgeSource(ScreenInfo{Width: 1920, Height: 1080}, edgeSide)
	if err != nil {
		t.Fatalf("newPortalEdgeSource: %v", err)
	}
	defer src.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	crossed := make(chan int32, 8)
	go func() {
		if err := src.Run(ctx, func(y int32) { crossed <- y }); err != nil {
			t.Logf("Run returned: %v", err)
		}
	}()

	t.Logf("move your cursor across the %s edge of the screen now (60s)...", edgeSide)
	select {
	case y := <-crossed:
		t.Logf("SUCCESS: barrier crossing detected at y=%d", y)
	case <-time.After(60 * time.Second):
		t.Fatal("no Activated signal received within 60s — did you cross the configured edge?")
	}

	t.Log("reentering at (1500, 800) and rearming — WATCH YOUR CURSOR NOW, don't touch the mouse for 5s...")
	if err := src.Reenter(1500, 800); err != nil {
		t.Fatalf("Reenter: %v", err)
	}
	time.Sleep(5 * time.Second)
	t.Log("(if the cursor visibly jumped to roughly the middle of a monitor just now, the warp works)")

	t.Logf("now cross the %s edge again to confirm rearm works (60s)...", edgeSide)
	select {
	case y := <-crossed:
		t.Logf("SUCCESS: second crossing detected at y=%d after rearm", y)
	case <-time.After(60 * time.Second):
		t.Fatal("no second Activated signal — rearm (Enable after Release) may not be working")
	}
}
