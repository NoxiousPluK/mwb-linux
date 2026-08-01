# Contributing to MWB Linux

Thank you for your interest in contributing! Here's how to get started.

## Development Setup

```bash
# Clone the repo
git clone https://github.com/lucky-verma/mwb-linux.git
cd mwb-linux

# Install Go 1.25+ (see go.mod for the exact version)
# https://go.dev/dl/

# Install dev dependencies
sudo apt install xdotool xclip

# Build
make build

# Build with the Wayland InputCapture portal backend (optional, needs a C
# toolchain and libei-1.0 dev headers — e.g. `libei-dev` on Debian/Ubuntu,
# `libei` on Arch/CachyOS):
make build-wayland

# Run tests
make test

# Run tests including the wayland_portal-tagged files (still skips the live
# manual test below unless you opt in):
CGO_ENABLED=1 go test -tags wayland_portal ./...

# Run linter (install golangci-lint first)
make lint

# Run all checks before committing
make check
```

## Submitting Changes

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Make your changes
4. Run `make check` to ensure everything passes
5. Commit with a descriptive message
6. Push and open a Pull Request — the PR template will guide you through the
   required checklist (build, race detector, lint, and manual input-isolation safety checks)

> **Before touching `internal/capture/`**: read the **Critical Invariants**
> section in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Violations of the
> mutex, floating slave, and cursor position invariants have caused real
> production bugs that were hard to diagnose.

## Code Style

- Follow standard Go conventions (`gofmt`)
- Use `slog` for structured logging
- Add debug-level logs for protocol details, info-level for user-visible events
- Keep functions focused and under 50 lines where possible

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for detailed protocol documentation.

## Areas for Contribution

- **Wayland input forwarding via EIS** — The `wayland_portal`-tagged backend (`internal/capture/edgesource_portal_linux.go`) currently only replaces edge *detection* and cursor *reentry* with the `org.freedesktop.portal.InputCapture` portal; forwarding raw input while the cursor is on the remote machine still goes through `EVIOCGRAB` + evdev (`internal/capture/capture_linux.go`), same as the xdotool backend. That path is already Wayland-native, so this isn't required, but the captured EIS connection (`internal/capture/eisdrain_linux.go`) already receives pointer/keyboard events it currently discards — using them directly would remove the `EVIOCGRAB` dependency for the outbound direction entirely.
- **GNOME/wlroots testing** — The portal backend was built and validated against real hardware on KDE Plasma 6.7.3/KWin only. GNOME 46+ and wlroots compositors with EIS support are supposed to implement the same portal, but the barrier-coordinate convention in `outerEdgeBarrier` (`edgesource_portal_linux.go`) was reverse-engineered empirically against KWin — a subtly different convention on another compositor would silently misplace the barrier. Needs testing (and likely a compositor-specific code path) on GNOME/Mutter and a wlroots compositor.
- **Multi-monitor barrier edge cases** — `outerEdgeBarrier` computes one barrier along the outermost edge of the whole zone arrangement, assuming monitors are laid out in a horizontal row. Vertically-stacked-at-the-edge layouts, or layouts where the outermost X doesn't correspond to a single contiguous Y-range, aren't handled specially.
- **XInput2 events** — Replace 100 forks/sec xdotool polling with `XI_RawMotion` event subscription (only relevant to the xdotool backend; the portal backend has no polling loop at all)
- **Virtual cursor correction** — Wire `UpdateRemoteScreen()` to incoming absolute coords to fix drift
- **Multi-monitor** — Proper screen geometry for `xrandr` multi-output setups
- **GUI** — System tray app with GTK
- **Auto-discovery** — Find MWB servers on the network
- **Packaging** — Flatpak, Snap, AUR packages

## Reporting Issues

Please include:
- Your Linux distribution and version
- Go version (`go version`)
- PowerToys version on Windows
- Debug logs (`mwb -debug`)
- Steps to reproduce
