package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/netcfg"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/update"
)

// NetworkSource is the network manager as the web layer needs it.
// *netcfg.Manager implements it.
type NetworkSource interface {
	State(ctx context.Context) (netcfg.State, error)
	Apply(ctx context.Context, ch netcfg.Change) (netcfg.Pending, error)
	Confirm() error
	Revert() error
	Delete(ctx context.Context, name string) error
	ScanWifi(ctx context.Context) ([]platform.AccessPoint, error)
}

// UpdateSource is the updater as the web layer needs it.
type UpdateSource interface {
	Status() update.State
	// StartCheck begins a check in the background and returns at once.
	// force is "install now": it skips the update window. ErrUpdateBusy when
	// one is running.
	StartCheck(force bool) error
}

// Errors an UpdateSource returns from StartCheck.
var (
	ErrUpdateBusy = errors.New("web: an update check is already running")
	ErrUpdateOff  = errors.New("web: self-update is off on this build")
)

// UpdateView is /api/update: the contract fields plus what the page needs.
type UpdateView struct {
	UpdateStatus
	Detail     string `json:"detail,omitempty"`
	DeferKind  string `json:"defer_kind,omitempty"`
	Code       string `json:"code,omitempty"`
	Progress   int    `json:"progress,omitempty"`
	OSDeferred bool   `json:"os_deferred,omitempty"`
	LastCheck  string `json:"last_check,omitempty"`
	Window     string `json:"window"`
	Channel    string `json:"channel"`
	// Enabled is false on a development build or a host with no pinned keys.
	Enabled bool `json:"enabled"`
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	cfg := s.opts.Config.Get().Updates
	if s.opts.Updates == nil {
		v := UpdateView{UpdateStatus: s.opts.Status.Update(r.Context()), Window: cfg.Window, Channel: cfg.Channel}
		writeJSON(w, http.StatusOK, v)
		return
	}
	st := s.opts.Updates.Status()
	v := UpdateView{
		UpdateStatus: UpdateStatus{
			Current: st.Current, Target: st.Target, State: st.State, Error: st.Error, TS: st.TS.UTC().Format(time.RFC3339),
		},
		Detail: st.Detail, DeferKind: st.DeferKind, Code: st.Code, Progress: st.Progress, OSDeferred: st.OSDeferred,
		Window: cfg.Window, Channel: cfg.Channel, Enabled: true,
	}
	if !st.LastCheck.IsZero() {
		v.LastCheck = st.LastCheck.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) startCheck(w http.ResponseWriter, force bool) {
	if s.opts.Updates == nil {
		s.handleNotImplemented(w, nil)
		return
	}
	switch err := s.opts.Updates.StartCheck(force); {
	case errors.Is(err, ErrUpdateBusy):
		writeErr(w, http.StatusConflict, "busy", "an update check or install is already running")
	case errors.Is(err, ErrUpdateOff):
		writeErr(w, http.StatusConflict, "update_off", "self-update is off on this build")
	case err != nil:
		s.log.Error("web: start update check", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not start the check")
	default:
		writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
	}
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, _ *http.Request) { s.startCheck(w, false) }
func (s *Server) handleUpdateApply(w http.ResponseWriter, _ *http.Request) { s.startCheck(w, true) }

// ---- network --------------------------------------------------------------

func (s *Server) network(w http.ResponseWriter) NetworkSource {
	if s.opts.Network == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "network management is not available on this host")
		return nil
	}
	return s.opts.Network
}

func (s *Server) networkError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, netcfg.ErrInvalid):
		writeErr(w, http.StatusUnprocessableEntity, "invalid_network", err.Error())
	case errors.Is(err, netcfg.ErrBusy):
		writeErr(w, http.StatusConflict, "commit_in_progress", "a change is already waiting for confirmation")
	case errors.Is(err, netcfg.ErrNoPending):
		writeErr(w, http.StatusConflict, "nothing_pending", "there is no change waiting for confirmation")
	case errors.Is(err, config.ErrDeadlinePassed):
		writeErr(w, http.StatusGone, "deadline_passed", "the confirmation time ran out and the previous settings were restored")
	case errors.Is(err, netcfg.ErrActiveConn), errors.Is(err, netcfg.ErrProtected):
		writeErr(w, http.StatusConflict, "in_use", err.Error())
	case errors.Is(err, platform.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	default:
		s.log.Error("web: network", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "the network manager reported an error")
	}
}

func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	n := s.network(w)
	if n == nil {
		return
	}
	st, err := n.State(r.Context())
	if err != nil {
		s.networkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleNetworkApply(w http.ResponseWriter, r *http.Request) {
	n := s.network(w)
	if n == nil {
		return
	}
	var ch netcfg.Change
	if !decodeBody(w, r, &ch) {
		return
	}
	p, err := n.Apply(r.Context(), ch)
	if err != nil {
		s.networkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"applied": true, "confirm_deadline": p.Deadline.UTC().Format(time.RFC3339), "changed": p.Changed,
	})
}

func (s *Server) handleNetworkConfirm(w http.ResponseWriter, _ *http.Request) {
	n := s.network(w)
	if n == nil {
		return
	}
	if err := n.Confirm(); err != nil {
		s.networkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"confirmed": true})
}

func (s *Server) handleNetworkRevert(w http.ResponseWriter, _ *http.Request) {
	n := s.network(w)
	if n == nil {
		return
	}
	if err := n.Revert(); err != nil {
		s.networkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"reverted": true})
}

func (s *Server) handleNetworkDelete(w http.ResponseWriter, r *http.Request) {
	n := s.network(w)
	if n == nil {
		return
	}
	name, err := url.PathUnescape(r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "bad connection name")
		return
	}
	if err := n.Delete(r.Context(), name); err != nil {
		s.networkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) handleNetworkWifi(w http.ResponseWriter, r *http.Request) {
	n := s.network(w)
	if n == nil {
		return
	}
	aps, err := n.ScanWifi(r.Context())
	if err != nil {
		s.networkError(w, err)
		return
	}
	if aps == nil {
		aps = []platform.AccessPoint{}
	}
	writeJSON(w, http.StatusOK, aps)
}
