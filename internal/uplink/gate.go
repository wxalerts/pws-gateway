package uplink

import (
	"context"
	"errors"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// timeSynced asks the platform whether the clock can be trusted. A platform
// that cannot tell (ErrUnsupported, a developer laptop) counts as synced,
// matching the collector; any other error counts as unsynced.
func (u *Uplink) timeSynced(ctx context.Context) bool {
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ts, err := u.opts.Platform.TimeSync(tctx)
	switch {
	case err == nil:
		return ts.Synced
	case errors.Is(err, platform.ErrUnsupported):
		return true
	default:
		u.log.Warn("time sync check failed", "err", err)
		return false
	}
}

// checkTimeGate tracks how long the clock has been unsynced and publishes one
// time_unsynced event once it has been more than TimeUnsyncedAfter since
// start (or since the clock last lost sync). It reports whether the clock is
// synced, which is the obs gate.
func (u *Uplink) checkTimeGate(ctx context.Context) bool {
	synced := u.timeSynced(ctx)
	now := u.now()
	u.gateMu.Lock()
	var emit bool
	switch {
	case synced:
		u.unsyncedEvent = false
		u.wasSynced = true
	default:
		if u.wasSynced {
			// Fresh loss of sync: restart the 10 minute window.
			u.unsyncedSince = now
			u.wasSynced = false
		}
		if !u.unsyncedEvent && now.Sub(u.unsyncedSince) > TimeUnsyncedAfter {
			u.unsyncedEvent = true
			emit = true
		}
	}
	u.gateMu.Unlock()
	if emit {
		u.log.Warn("clock still unsynced, observations are held back")
		u.Emit(ctx, contract.EventTimeUnsynced, nil)
	}
	return synced
}
