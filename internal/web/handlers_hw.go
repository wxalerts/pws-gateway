package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/wxalerts/pws-gateway/internal/bootchange"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// BootChange is the commit-confirm machine for boot configuration changes
// (internal/bootchange.Manager). nil means the feature is unavailable.
type BootChange interface {
	Enable(enabled bool) (time.Time, error)
	Confirm() error
	Rollback() error
	Expire() (bool, error)
	State() config.State[bootchange.Settings]
}

// ---- modem and GPS --------------------------------------------------------

func (s *Server) handleModem(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"modem": nil, "gps": nil, "link": "unknown"}
	if m, err := s.opts.Platform.Modem(r.Context()); err == nil && m != nil {
		out["modem"] = m
	}
	if g, err := s.opts.Platform.GPS(r.Context()); err == nil && g != nil {
		out["gps"] = g
	}
	if name, err := s.opts.Platform.DefaultRouteInterface(r.Context()); err == nil {
		out["link"] = linkFromInterface(name)
	}
	writeJSON(w, http.StatusOK, out)
}

// linkFromInterface mirrors uplink.LinkFromInterface for the UI.
func linkFromInterface(name string) string {
	switch {
	case hasPrefix(name, "wwan", "ppp", "rmnet", "usb"):
		return "lte"
	case hasPrefix(name, "wlan", "wlp", "wlx"):
		return "wifi"
	case hasPrefix(name, "eth", "en"):
		return "ethernet"
	}
	return "unknown"
}

func hasPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if len(s) >= len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}

// ---- I2C ------------------------------------------------------------------

type i2cChange struct {
	Phase     string  `json:"phase"`
	Candidate *bool   `json:"candidate"`
	Deadline  *string `json:"deadline"`
}

func (s *Server) i2cChange() i2cChange {
	c := i2cChange{Phase: string(config.PhaseIdle)}
	if s.opts.Boot == nil {
		return c
	}
	if _, err := s.opts.Boot.Expire(); err != nil {
		s.log.Warn("web: expire boot change", "err", err)
	}
	st := s.opts.Boot.State()
	c.Phase = string(st.Phase)
	if st.Phase != config.PhaseIdle {
		v := st.Active.I2C
		if st.Pending != nil {
			v = st.Pending.I2C
		}
		c.Candidate = &v
	}
	if st.Deadline != nil {
		d := st.Deadline.UTC().Format(time.RFC3339)
		c.Deadline = &d
	}
	return c
}

func (s *Server) handleI2C(w http.ResponseWriter, r *http.Request) {
	st, err := s.opts.Platform.I2C(r.Context())
	if err != nil && !errors.Is(err, platform.ErrUnsupported) {
		s.log.Warn("web: i2c scan", "err", err)
	}
	if st.Devices == nil {
		st.Devices = []platform.I2CDevice{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": st.Enabled, "bus_present": st.BusPresent,
		// The bus device appears or disappears at the next boot, so a
		// mismatch with the boot configuration means a reboot is pending.
		"reboot_required": st.Enabled != st.BusPresent,
		"devices":         st.Devices,
		"change":          s.i2cChange(),
	})
}

func (s *Server) handleI2CEnable(w http.ResponseWriter, r *http.Request) {
	if s.opts.Boot == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "boot configuration changes are not available on this host")
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "enabled is required")
		return
	}
	s.i2cChange() // expire a stale change first
	deadline, err := s.opts.Boot.Enable(*body.Enabled)
	switch {
	case errors.Is(err, config.ErrAlreadyApplied):
		writeErr(w, http.StatusConflict, "already_applied", "a change is already awaiting confirmation")
	case err != nil:
		s.log.Error("web: enable i2c", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not change the boot configuration")
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"applied": true, "reboot_required": true, "confirm_deadline": deadline.UTC().Format(time.RFC3339),
		})
	}
}

func (s *Server) handleI2CConfirm(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Boot == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "boot configuration changes are not available on this host")
		return
	}
	s.i2cChange()
	switch err := s.opts.Boot.Confirm(); {
	case errors.Is(err, config.ErrNothingApplied):
		writeErr(w, http.StatusConflict, "nothing_pending", "there is no change waiting for confirmation")
	case errors.Is(err, config.ErrDeadlinePassed):
		writeErr(w, http.StatusConflict, "deadline_passed", "the confirmation window passed and the change was rolled back")
	case err != nil:
		s.log.Error("web: confirm boot change", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not confirm")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"confirmed": true})
	}
}

func (s *Server) handleI2CRollback(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Boot == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "boot configuration changes are not available on this host")
		return
	}
	if err := s.opts.Boot.Rollback(); err != nil {
		s.log.Error("web: roll back boot change", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not roll back")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"rolled_back": true})
}

// ---- time -----------------------------------------------------------------

func (s *Server) handleTime(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{
		"synced": false, "source": "none", "offset_ms": nil, "last_sync": nil,
		"rtc": nil, "fake_hwclock_disabled": nil,
		"system_time": s.now().UTC().Format(time.RFC3339),
	}
	rtc, err := s.opts.Platform.RTC(ctx)
	if err != nil {
		rtc = nil
	}
	if rtc != nil {
		out["rtc"] = map[string]any{
			"time": rtc.Time.UTC().Format(time.RFC3339), "oscillator_stopped": rtc.OscillatorStopped, "temp_c": rtc.TempC,
		}
		if off, err := s.opts.Platform.FakeHwclockDisabled(ctx); err == nil {
			out["fake_hwclock_disabled"] = off
		}
	}
	if ts, err := s.opts.Platform.TimeSync(ctx); err == nil {
		out["synced"] = ts.Synced
		out["source"] = platform.TimeSource(ts, rtc)
		if ts.OffsetKnown {
			out["offset_ms"] = float64(ts.Offset) / float64(time.Millisecond)
		}
		if !ts.LastSync.IsZero() {
			out["last_sync"] = ts.LastSync.UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
