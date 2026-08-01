//go:build linux && wayland_portal

package capture

import "log/slog"

// selectEdgeSource resolves the configured capture_backend for builds with
// wayland_portal support: "xdotool" and "portal" force a specific backend;
// "auto" (and anything else) prefers the portal when it's reachable on the
// session bus, falling back to xdotool otherwise.
func selectEdgeSource(name string, screen ScreenInfo, edgeSide string) EdgeSource {
	switch name {
	case "xdotool":
		return newXdotoolEdgeSource(screen, edgeSide)
	case "portal":
		src, err := newPortalEdgeSource(screen, edgeSide)
		if err != nil {
			slog.Error("portal capture backend init failed, falling back to xdotool", "err", err)
			return newXdotoolEdgeSource(screen, edgeSide)
		}
		return src
	default: // "auto"
		if portalAvailable() {
			src, err := newPortalEdgeSource(screen, edgeSide)
			if err == nil {
				slog.Info("capture backend: portal (auto-detected)")
				return src
			}
			slog.Warn("portal backend probe failed, falling back to xdotool", "err", err)
		}
		return newXdotoolEdgeSource(screen, edgeSide)
	}
}
