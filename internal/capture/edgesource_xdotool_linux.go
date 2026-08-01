//go:build linux

package capture

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

// xdotoolEdgeSource detects screen-edge crossings by polling
// `xdotool getmouselocation` every 10ms over X11/XWayland, and warps the
// cursor back with `xdotool mousemove`. This is the default EdgeSource; it
// requires a live X11 display and does not track the cursor reliably under
// most Wayland compositors, whose XWayland implementations don't forward
// live global pointer position outside XWayland's own surfaces (see the
// README's Wayland limitations).
type xdotoolEdgeSource struct {
	screen    ScreenInfo
	edgeSide  string
	canSwitch atomic.Bool // true once cursor has been away from edge since activation
}

func newXdotoolEdgeSource(screen ScreenInfo, edgeSide string) *xdotoolEdgeSource {
	s := &xdotoolEdgeSource{screen: screen, edgeSide: edgeSide}
	s.canSwitch.Store(true) // allow first switch immediately
	return s
}

// NotifyActivated re-arms the debounce gate whenever this machine regains
// cursor ownership. Without this, the poll loop could sample the cursor's
// stale pre-warp position (still sitting at the edge) and immediately
// re-trigger a switch back out, racing the caller's Reenter warp.
func (s *xdotoolEdgeSource) NotifyActivated() {
	s.canSwitch.Store(false)
}

func (s *xdotoolEdgeSource) Run(ctx context.Context, onCross func(y int32)) error {
	slog.Info("edge polling started", "edge", s.edgeSide, "screenWidth", s.screen.Width)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	errCount := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			x, y, err := getCursorPosition()
			if err != nil {
				errCount++
				if errCount <= 3 {
					slog.Warn("getCursorPosition failed", "err", err, "count", errCount)
				}
				continue
			}
			if errCount > 0 {
				errCount = 0
			}

			// Track whether cursor has been away from the edge since activation.
			// This prevents loops: cursor must move inward first, then back to edge.
			if shouldArm(s.edgeSide, x, s.screen.Width) {
				s.canSwitch.Store(true)
			}

			if s.canSwitch.Load() && crossedEdge(s.edgeSide, x, s.screen.Width) {
				onCross(y)
			}
		}
	}
}

// Reenter warps the local X11/XWayland cursor to (x, y).
func (s *xdotoolEdgeSource) Reenter(x, y int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "xdotool", "mousemove", "--",
		fmt.Sprintf("%d", x),
		fmt.Sprintf("%d", y))
	cmd.Env = append(os.Environ(), "DISPLAY="+getDisplay())
	return cmd.Run()
}

func (s *xdotoolEdgeSource) Close() error { return nil }

// shouldArm reports whether x has moved far enough inward from edgeSide's
// screen edge to re-arm the next switch — must move this far inward to
// re-arm — pixels from edge.
func shouldArm(edgeSide string, x, screenWidth int32) bool {
	const edgeZone = int32(20)
	switch edgeSide {
	case "left":
		return x > edgeZone
	case "right":
		return x < screenWidth-edgeZone
	default:
		return false
	}
}

// crossedEdge reports whether x is at or past edgeSide's screen edge.
func crossedEdge(edgeSide string, x, screenWidth int32) bool {
	switch edgeSide {
	case "left":
		return x <= 1
	case "right":
		return x >= screenWidth-1
	default:
		return false
	}
}

func getCursorPosition() (x, y int32, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "xdotool", "getmouselocation")
	cmd.Env = append(os.Environ(), "DISPLAY="+getDisplay())
	out, err := cmd.Output()
	if err != nil {
		return -1, -1, fmt.Errorf("xdotool: %w", err)
	}
	var ix, iy int
	if _, err = fmt.Sscanf(string(out), "x:%d y:%d", &ix, &iy); err != nil {
		// Return sentinel -1,-1 to distinguish parse failure from cursor at origin (0,0)
		return -1, -1, fmt.Errorf("xdotool parse: %w", err)
	}
	return int32(ix), int32(iy), nil
}
