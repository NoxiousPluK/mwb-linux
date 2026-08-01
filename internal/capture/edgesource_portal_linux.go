//go:build linux && wayland_portal

package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

// portalEdgeSource detects screen-edge crossings via the freedesktop
// InputCapture portal (org.freedesktop.portal.InputCapture), backed by the
// compositor's own barrier-crossing detection instead of a cursor-position
// poll. Requires a compositor that implements the portal (KWin 6.1+,
// GNOME 46+, wlroots compositors with EIS support). Only compiled in with
// the wayland_portal build tag — see selectEdgeSource and `make
// build-wayland`.
type portalEdgeSource struct {
	conn     *dbus.Conn
	session  dbus.ObjectPath
	screen   ScreenInfo
	edgeSide string
	drain    *eisDrain

	mu             sync.Mutex
	zoneSet        uint32
	activationID   uint32
	haveActivation bool

	sigCh  chan *dbus.Signal
	stopCh chan struct{}
	doneCh chan struct{}
}

const (
	portalBusName    = "org.freedesktop.portal.Desktop"
	portalObjectPath = "/org/freedesktop/portal/desktop"
	portalIface      = "org.freedesktop.portal.InputCapture"
	requestIface     = "org.freedesktop.portal.Request"
	sessionIface     = "org.freedesktop.portal.Session"

	// capICKeyboard, capICPointer, capICTouch are the InputCapture portal's
	// own capability bitmask (distinct from libei's ei_device_capability
	// bits). We only ever request pointer capture.
	capICPointer = uint32(2)
)

var requestSerial atomic.Uint64

// portalAvailable reports whether org.freedesktop.portal.InputCapture is
// reachable on the session bus, used by "auto" backend selection.
func portalAvailable() bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	obj := conn.Object(portalBusName, dbus.ObjectPath(portalObjectPath))
	var version dbus.Variant
	err = obj.Call("org.freedesktop.DBus.Properties.Get", 0, portalIface, "version").Store(&version)
	return err == nil
}

func newPortalEdgeSource(screen ScreenInfo, edgeSide string) (src *portalEdgeSource, err error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect session bus: %w", err)
	}
	p := &portalEdgeSource{
		conn:     conn,
		screen:   screen,
		edgeSide: edgeSide,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	defer func() {
		if err != nil {
			p.teardown()
		}
	}()

	if p.session, err = p.createSession(); err != nil {
		return nil, err
	}
	if err = p.start(); err != nil {
		return nil, err
	}
	fd, err := p.connectToEIS()
	if err != nil {
		return nil, err
	}
	if p.drain, err = newEISDrain(fd); err != nil {
		return nil, fmt.Errorf("start EIS drain: %w", err)
	}
	if err = p.setBarriers(); err != nil {
		return nil, err
	}
	if err = p.call("Enable"); err != nil {
		return nil, fmt.Errorf("Enable: %w", err)
	}

	p.sigCh = make(chan *dbus.Signal, 8)
	conn.Signal(p.sigCh)
	if err = conn.AddMatchSignal(
		dbus.WithMatchInterface(portalIface),
		dbus.WithMatchObjectPath(portalObjectPath),
	); err != nil {
		return nil, fmt.Errorf("subscribe to InputCapture signals: %w", err)
	}

	return p, nil
}

// --- EdgeSource interface ---

func (p *portalEdgeSource) Run(ctx context.Context, onCross func(y int32)) error {
	defer close(p.doneCh)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.stopCh:
			return nil
		case sig, ok := <-p.sigCh:
			if !ok {
				return nil
			}
			p.handleSignal(sig, onCross)
		}
	}
}

func (p *portalEdgeSource) handleSignal(sig *dbus.Signal, onCross func(y int32)) {
	if len(sig.Body) != 2 {
		return
	}
	sessionHandle, ok := sig.Body[0].(dbus.ObjectPath)
	if !ok || sessionHandle != p.session {
		return
	}
	options, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return
	}

	switch {
	case strings.HasSuffix(sig.Name, ".Activated"):
		id, _ := variantUint32(options["activation_id"])
		p.mu.Lock()
		p.activationID = id
		p.haveActivation = true
		p.mu.Unlock()

		y := p.screen.Height / 2
		if pos, ok := options["cursor_position"]; ok {
			if x, yy, err := variantPoint(pos); err == nil {
				_ = x
				y = int32(yy)
			} else {
				slog.Warn("portal Activated: could not parse cursor_position, using screen center", "err", err)
			}
		}
		slog.Info("portal capture activated", "activation_id", id, "y", y)
		onCross(y)

	case strings.HasSuffix(sig.Name, ".Deactivated"):
		slog.Debug("portal capture deactivated")

	case strings.HasSuffix(sig.Name, ".Disabled"):
		slog.Warn("portal capture disabled by compositor; re-enabling")
		if err := p.call("Enable"); err != nil {
			slog.Error("re-enable after Disabled failed", "err", err)
		}

	case strings.HasSuffix(sig.Name, ".ZonesChanged"):
		slog.Info("portal zones changed, re-registering barriers")
		if err := p.setBarriers(); err != nil {
			slog.Error("re-register barriers after ZonesChanged failed", "err", err)
		}
	}
}

// Reenter releases the active capture, asking the compositor to place the
// cursor at (x, y), then re-enables capture for the next crossing.
func (p *portalEdgeSource) Reenter(x, y int32) error {
	p.mu.Lock()
	id := p.activationID
	have := p.haveActivation
	p.haveActivation = false
	p.mu.Unlock()

	options := map[string]dbus.Variant{
		"cursor_position": dbus.MakeVariant(cursorPos{X: float64(x), Y: float64(y)}),
	}
	if have {
		options["activation_id"] = dbus.MakeVariant(id)
	}
	obj := p.conn.Object(portalBusName, dbus.ObjectPath(portalObjectPath))
	if err := obj.Call(portalIface+".Release", 0, p.session, options).Err; err != nil {
		return fmt.Errorf("Release: %w", err)
	}
	// Rearm for the next crossing.
	if err := p.call("Enable"); err != nil {
		return fmt.Errorf("Enable (rearm): %w", err)
	}
	return nil
}

// NotifyActivated is a no-op here: the portal's own Enable/Release cycle
// (driven from Reenter) is what rearms capture, so there's no separate
// poll-debounce state to reset.
func (p *portalEdgeSource) NotifyActivated() {}

func (p *portalEdgeSource) Close() error {
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}
	<-p.doneCh
	p.teardown()
	return nil
}

func (p *portalEdgeSource) teardown() {
	if p.drain != nil {
		p.drain.Close()
	}
	if p.session != "" {
		obj := p.conn.Object(portalBusName, p.session)
		_ = obj.Call(sessionIface+".Close", 0).Err
	}
	_ = p.conn.Close()
}

// --- portal session setup ---

func (p *portalEdgeSource) createSession() (dbus.ObjectPath, error) {
	obj := p.conn.Object(portalBusName, dbus.ObjectPath(portalObjectPath))
	token := fmt.Sprintf("mwbsession%d", requestSerial.Add(1))
	options := map[string]dbus.Variant{
		"session_handle_token": dbus.MakeVariant(token),
	}
	var results map[string]dbus.Variant
	if err := obj.Call(portalIface+".CreateSession2", 0, options).Store(&results); err != nil {
		return "", fmt.Errorf("CreateSession2: %w", err)
	}
	sh, ok := results["session_handle"]
	if !ok {
		return "", fmt.Errorf("CreateSession2: no session_handle in results")
	}
	path, ok := sh.Value().(dbus.ObjectPath)
	if !ok {
		return "", fmt.Errorf("CreateSession2: session_handle has unexpected type %T", sh.Value())
	}
	return path, nil
}

func (p *portalEdgeSource) start() error {
	options := map[string]dbus.Variant{
		"capabilities": dbus.MakeVariant(capICPointer),
		"persist_mode": dbus.MakeVariant(uint32(2)), // persist until explicitly revoked
	}
	if token := loadRestoreToken(); token != "" {
		options["restore_token"] = dbus.MakeVariant(token)
	}
	results, err := p.doRequest("Start", func(_ string) []interface{} {
		return []interface{}{p.session, "", options}
	})
	if err != nil {
		return err
	}
	if rt, ok := results["restore_token"]; ok {
		if s, ok := rt.Value().(string); ok && s != "" {
			saveRestoreToken(s)
		}
	}
	return nil
}

func (p *portalEdgeSource) connectToEIS() (int, error) {
	obj := p.conn.Object(portalBusName, dbus.ObjectPath(portalObjectPath))
	var fd dbus.UnixFD
	if err := obj.Call(portalIface+".ConnectToEIS", 0, p.session, map[string]dbus.Variant{}).Store(&fd); err != nil {
		return -1, fmt.Errorf("ConnectToEIS: %w", err)
	}
	return int(fd), nil
}

type zoneRect struct {
	Width, Height uint32
	X, Y          int32
}

// barrierPosition and cursorPos are named Go struct types so godbus marshals
// them as D-Bus structs ("(iiii)" and "(dd)" respectively) — a Go array or
// slice would marshal as a D-Bus array instead, which the portal silently
// rejects rather than erroring outright.
type barrierPosition struct {
	X1, Y1, X2, Y2 int32
}

type cursorPos struct {
	X, Y float64
}

func (p *portalEdgeSource) getZones() ([]zoneRect, uint32, error) {
	results, err := p.doRequest("GetZones", func(_ string) []interface{} {
		return []interface{}{p.session, map[string]dbus.Variant{}}
	})
	if err != nil {
		return nil, 0, err
	}
	zoneSet, err := variantUint32(results["zone_set"])
	if err != nil {
		return nil, 0, fmt.Errorf("GetZones: decode zone_set: %w", err)
	}
	zv, ok := results["zones"]
	if !ok {
		return nil, zoneSet, fmt.Errorf("GetZones: no zones in results")
	}
	raw, ok := zv.Value().([][]interface{})
	if !ok {
		// godbus may decode a(uuii) as []struct via reflection instead;
		// fall back to a generic slice-of-interfaces walk.
		return nil, zoneSet, fmt.Errorf("GetZones: unexpected zones type %T", zv.Value())
	}
	zones := make([]zoneRect, 0, len(raw))
	for _, z := range raw {
		if len(z) != 4 {
			continue
		}
		zones = append(zones, zoneRect{
			Width:  toUint32(z[0]),
			Height: toUint32(z[1]),
			X:      toInt32(z[2]),
			Y:      toInt32(z[3]),
		})
	}
	return zones, zoneSet, nil
}

func (p *portalEdgeSource) setBarriers() error {
	zones, zoneSet, err := p.getZones()
	if err != nil {
		return err
	}
	x1, y1, x2, y2, err := outerEdgeBarrier(zones, p.edgeSide)
	if err != nil {
		return err
	}
	slog.Debug("computed pointer barrier", "zones", zones, "zone_set", zoneSet, "edge", p.edgeSide,
		"x1", x1, "y1", y1, "x2", x2, "y2", y2)

	barrier := map[string]dbus.Variant{
		"barrier_id": dbus.MakeVariant(uint32(1)),
		"position":   dbus.MakeVariant(barrierPosition{X1: x1, Y1: y1, X2: x2, Y2: y2}),
	}
	results, err := p.doRequest("SetPointerBarriers", func(_ string) []interface{} {
		return []interface{}{p.session, map[string]dbus.Variant{}, []map[string]dbus.Variant{barrier}, zoneSet}
	})
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.zoneSet = zoneSet
	p.mu.Unlock()
	if fb, ok := results["failed_barriers"]; ok {
		if list, ok := fb.Value().([]uint32); ok && len(list) > 0 {
			return fmt.Errorf("SetPointerBarriers: barrier(s) rejected: %v", list)
		}
	}
	return nil
}

// outerEdgeBarrier computes a single vertical barrier along the outermost
// edge of the whole zone arrangement (leftmost x for "left", rightmost x+w
// for "right"), spanning the y-range of whichever zone(s) sit on that edge.
// This matches how the xdotool backend already treats "the edge" — the
// outer boundary of the combined virtual screen, not a boundary between the
// user's own monitors. Vertically-stacked-at-the-edge layouts aren't
// handled specially; documented as a known limitation.
//
// The X coordinate's convention was confirmed empirically against a live
// KWin 6.7.3 (docs don't specify it precisely, and it's asymmetric): the
// left edge is inclusive — barrier X equals zone.X exactly — while the
// right edge is exclusive — barrier X equals zone.X+zone.Width, one past
// the last pixel column, not zone.X+zone.Width-1. The Y span is
// inclusive-pixel on both edges (see edgeYRange).
func outerEdgeBarrier(zones []zoneRect, edgeSide string) (x1, y1, x2, y2 int32, err error) {
	if len(zones) == 0 {
		return 0, 0, 0, 0, fmt.Errorf("no zones reported")
	}
	switch edgeSide {
	case "left":
		minX := zones[0].X
		for _, z := range zones {
			if z.X < minX {
				minX = z.X
			}
		}
		top, bottom := edgeYRange(zones, minX, false)
		return minX, top, minX, bottom, nil
	case "right":
		maxX := zones[0].X + int32(zones[0].Width)
		for _, z := range zones {
			if r := z.X + int32(z.Width); r > maxX {
				maxX = r
			}
		}
		top, bottom := edgeYRange(zones, maxX, true)
		return maxX, top, maxX, bottom, nil
	default:
		return 0, 0, 0, 0, fmt.Errorf("unknown edge side %q", edgeSide)
	}
}

// edgeYRange returns the inclusive [top, bottom] pixel span (a 1440px-tall
// zone uses bottom=1439, not 1440) of whichever zone(s) sit on edgeX. edgeX
// is compared in the same convention outerEdgeBarrier computed it in
// (exclusive on the right side, matching zone.X+zone.Width; inclusive,
// zone.X itself, on the left).
func edgeYRange(zones []zoneRect, edgeX int32, right bool) (top, bottom int32) {
	first := true
	for _, z := range zones {
		x := z.X
		if right {
			x = z.X + int32(z.Width)
		}
		if x != edgeX {
			continue
		}
		if first || z.Y < top {
			top = z.Y
		}
		if first || z.Y+int32(z.Height)-1 > bottom {
			bottom = z.Y + int32(z.Height) - 1
		}
		first = false
	}
	return top, bottom
}

// --- generic Request-pattern + call helpers ---

// call invokes a fire-and-forget InputCapture method (Enable/Disable) that
// has no OUT handle and no Request/Response round trip.
func (p *portalEdgeSource) call(method string) error {
	obj := p.conn.Object(portalBusName, dbus.ObjectPath(portalObjectPath))
	return obj.Call(portalIface+"."+method, 0, p.session, map[string]dbus.Variant{}).Err
}

// doRequest calls a portal method that returns an OUT handle o and delivers
// its real result asynchronously via a Response signal on that handle's
// object path (the standard xdg-desktop-portal request pattern). buildArgs
// receives the generated handle_token so the caller can place it in the
// right position in that method's options map (position varies by method).
func (p *portalEdgeSource) doRequest(method string, buildArgs func(token string) []interface{}) (map[string]dbus.Variant, error) {
	token := fmt.Sprintf("mwbreq%d", requestSerial.Add(1))
	sender := strings.TrimPrefix(p.conn.Names()[0], ":")
	sender = strings.ReplaceAll(sender, ".", "_")
	reqPath := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/portal/desktop/request/%s/%s", sender, token))

	sigCh := make(chan *dbus.Signal, 1)
	p.conn.Signal(sigCh)
	defer p.conn.RemoveSignal(sigCh)
	if err := p.conn.AddMatchSignal(
		dbus.WithMatchInterface(requestIface),
		dbus.WithMatchObjectPath(reqPath),
	); err != nil {
		return nil, fmt.Errorf("%s: add match: %w", method, err)
	}
	defer func() {
		_ = p.conn.RemoveMatchSignal(
			dbus.WithMatchInterface(requestIface),
			dbus.WithMatchObjectPath(reqPath),
		)
	}()

	// buildArgs is called with the request's handle_token already baked
	// into the options map by the caller (see each call site) — we only
	// pass it through here for methods that need it echoed elsewhere.
	args := buildArgsWithToken(buildArgs, token)

	obj := p.conn.Object(portalBusName, dbus.ObjectPath(portalObjectPath))
	var handle dbus.ObjectPath
	if err := obj.Call(portalIface+"."+method, 0, args...).Store(&handle); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}

	select {
	case sig := <-sigCh:
		if sig.Path != reqPath {
			return nil, fmt.Errorf("%s: response from unexpected path %s", method, sig.Path)
		}
		if len(sig.Body) != 2 {
			return nil, fmt.Errorf("%s: malformed Response signal", method)
		}
		response, _ := variantOrRawUint32(sig.Body[0])
		results, _ := sig.Body[1].(map[string]dbus.Variant)
		if response != 0 {
			return nil, fmt.Errorf("%s: request denied or cancelled (code %d)", method, response)
		}
		return results, nil
	case <-time.After(120 * time.Second):
		// Long enough to cover Start's human-in-the-loop consent dialog;
		// the other Request-pattern calls (GetZones, SetPointerBarriers)
		// resolve near-instantly so this ceiling doesn't cost them anything.
		return nil, fmt.Errorf("%s: timed out waiting for portal response", method)
	}
}

// buildArgsWithToken injects handle_token into whichever options map in
// args is still missing it. Every call site already builds its options map
// with the other required keys; this fills in the one field that has to
// match the signal subscription path.
func buildArgsWithToken(buildArgs func(token string) []interface{}, token string) []interface{} {
	args := buildArgs(token)
	for _, a := range args {
		if opts, ok := a.(map[string]dbus.Variant); ok {
			if _, has := opts["handle_token"]; !has {
				opts["handle_token"] = dbus.MakeVariant(token)
				return args
			}
		}
	}
	return args
}

// --- dbus.Variant decoding helpers ---
//
// godbus decodes a{sv} values as dbus.Variant wrapping loosely-typed Go
// values (often []interface{} for structs rather than fixed-size arrays),
// so these helpers accept a couple of plausible shapes rather than a single
// rigid type — the exact shape needs confirming against the live portal
// (see the Wayland-portal live-test task), and being permissive here means
// a minor decoding mismatch degrades gracefully instead of crashing.

func variantUint32(v dbus.Variant) (uint32, error) {
	return toUint32Checked(v.Value())
}

func toUint32Checked(v interface{}) (uint32, error) {
	switch n := v.(type) {
	case uint32:
		return n, nil
	case int32:
		return uint32(n), nil
	case uint64:
		return uint32(n), nil
	case float64:
		return uint32(n), nil
	default:
		return 0, fmt.Errorf("unexpected numeric type %T", v)
	}
}

func toUint32(v interface{}) uint32 {
	n, _ := toUint32Checked(v)
	return n
}

func toInt32(v interface{}) int32 {
	switch n := v.(type) {
	case int32:
		return n
	case uint32:
		return int32(n)
	case int64:
		return int32(n)
	case float64:
		return int32(n)
	default:
		return 0
	}
}

func variantOrRawUint32(v interface{}) (uint32, error) {
	if vv, ok := v.(dbus.Variant); ok {
		return toUint32Checked(vv.Value())
	}
	return toUint32Checked(v)
}

// variantPoint decodes a cursor_position value, tried as a [2]float64
// struct first (the documented (dd) signature) and falling back to a
// generic []interface{} of length 2.
func variantPoint(v dbus.Variant) (x, y float64, err error) {
	switch p := v.Value().(type) {
	case [2]float64:
		return p[0], p[1], nil
	case []interface{}:
		if len(p) != 2 {
			return 0, 0, fmt.Errorf("cursor_position: want 2 elements, got %d", len(p))
		}
		xf, xok := toFloat(p[0])
		yf, yok := toFloat(p[1])
		if !xok || !yok {
			return 0, 0, fmt.Errorf("cursor_position: non-numeric elements %v", p)
		}
		return xf, yf, nil
	default:
		return 0, 0, fmt.Errorf("cursor_position: unexpected type %T", v.Value())
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int32:
		return float64(n), true
	case uint32:
		return float64(n), true
	default:
		return 0, false
	}
}

// --- restore_token persistence ---
//
// Stored alongside config.toml, not inside it: it's an opaque portal
// credential, not user-editable config. Persisting it means the systemd
// --user service doesn't re-show KDE's consent dialog on every restart.

type portalState struct {
	RestoreToken string `json:"restore_token"`
}

func portalStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "mwb", "portal-state.json")
}

func loadRestoreToken() string {
	path := portalStatePath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var st portalState
	if err := json.Unmarshal(data, &st); err != nil {
		return ""
	}
	return st.RestoreToken
}

func saveRestoreToken(token string) {
	path := portalStatePath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		slog.Warn("could not create portal state directory", "err", err)
		return
	}
	data, err := json.Marshal(portalState{RestoreToken: token})
	if err != nil {
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		slog.Warn("could not persist portal restore token", "err", err)
	}
}
