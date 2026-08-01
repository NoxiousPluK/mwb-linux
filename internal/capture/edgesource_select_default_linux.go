//go:build linux && !wayland_portal

package capture

import "log/slog"

// selectEdgeSource resolves the configured capture_backend for builds
// without wayland_portal support: xdotool is the only backend compiled in.
// Build with `make build-wayland` (tag wayland_portal) to get the portal
// backend for native Wayland compositors.
func selectEdgeSource(name string, screen ScreenInfo, edgeSide string) EdgeSource {
	if name == "portal" {
		slog.Warn("capture_backend=portal requested but this binary was not built with wayland_portal support, falling back to xdotool",
			"hint", "build with: make build-wayland")
	}
	return newXdotoolEdgeSource(screen, edgeSide)
}
