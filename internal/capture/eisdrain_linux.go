//go:build linux && wayland_portal

package capture

/*
#cgo pkg-config: libei-1.0
#include <libei-1.0/libei.h>
#include <stdlib.h>

// ei_seat_bind_capabilities is a C variadic function (NULL-terminated), which
// cgo cannot call directly. This wraps the fixed set of capabilities a real
// input-capture client binds.
static void ei_bind_all_capabilities(struct ei_seat *seat) {
	ei_seat_bind_capabilities(seat,
		EI_DEVICE_CAP_POINTER,
		EI_DEVICE_CAP_POINTER_ABSOLUTE,
		EI_DEVICE_CAP_BUTTON,
		EI_DEVICE_CAP_KEYBOARD,
		EI_DEVICE_CAP_SCROLL,
		NULL);
}
*/
import "C"

import (
	"fmt"
	"log/slog"
	"time"
)

// eisDrain owns an EIS receiver connection (from the portal's ConnectToEIS
// fd) just long enough to keep it drained.
//
// This backend does not consume pointer/keyboard motion itself — the
// portal's D-Bus Activated/Deactivated signals are what edgesource_portal
// actually acts on — but the connection still has to be pumped: an EIS
// sender (KWin) writing into a socket nobody reads from can block, which
// would stall the whole capture session. Binding seat capabilities on
// EI_EVENT_SEAT_ADDED matches what a real input-capture client does and
// keeps the session protocol-conformant; a client that never binds anything
// is not how EIS senders expect to be talked to.
type eisDrain struct {
	ctx    *C.struct_ei
	stopCh chan struct{}
	doneCh chan struct{}
}

// newEISDrain takes ownership of fd (closed by libei on Close, per
// ei_setup_backend_fd's documented contract) and starts pumping it.
func newEISDrain(fd int) (*eisDrain, error) {
	ctx := C.ei_new_receiver(nil)
	if ctx == nil {
		return nil, fmt.Errorf("ei_new_receiver failed")
	}
	if ret := C.ei_setup_backend_fd(ctx, C.int(fd)); ret != 0 {
		C.ei_unref(ctx)
		return nil, fmt.Errorf("ei_setup_backend_fd: %d", int(ret))
	}
	d := &eisDrain{
		ctx:    ctx,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go d.run()
	return d, nil
}

// run polls ei_dispatch on a short tick rather than blocking on the fd
// directly: wrapping a cgo-owned fd in an os.File would race libei's own
// close of it on teardown (Go's finalizer could later close a reused fd
// number). A tick is simpler and correct at the cost of a small, bounded
// drain latency — irrelevant here since we never act on the drained events'
// content, only on the separate D-Bus signals.
func (d *eisDrain) run() {
	defer close(d.doneCh)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			C.ei_dispatch(d.ctx)
			for {
				ev := C.ei_get_event(d.ctx)
				if ev == nil {
					break
				}
				d.handleEvent(ev)
				C.ei_event_unref(ev)
			}
		}
	}
}

func (d *eisDrain) handleEvent(ev *C.struct_ei_event) {
	switch C.ei_event_get_type(ev) {
	case C.EI_EVENT_SEAT_ADDED:
		seat := C.ei_event_get_seat(ev)
		if seat != nil {
			C.ei_bind_all_capabilities(seat)
		}
	case C.EI_EVENT_DISCONNECT:
		slog.Warn("EIS connection disconnected by compositor")
	}
}

// Close stops draining and releases the EIS context, which also closes the
// underlying fd (ei_setup_backend_fd took ownership of it).
func (d *eisDrain) Close() {
	close(d.stopCh)
	<-d.doneCh
	C.ei_unref(d.ctx)
}
