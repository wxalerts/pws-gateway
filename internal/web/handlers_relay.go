package web

import (
	"net/http"

	"github.com/wxalerts/pws-gateway/internal/relay/cwop"
)

// handleRelay returns the CWOP relay settings and delivery state. The
// passcode is never in the answer, only whether one is stored.
func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
	cfg := s.opts.Config.Get().Relay.CWOP
	writeJSON(w, http.StatusOK, map[string]any{
		"cwop": map[string]any{
			"config": map[string]any{
				"owner": cfg.Owner, "station": cfg.Station, "id": cfg.ID,
				"server": cfg.Server, "position_opt_in": cfg.PositionOptIn,
				"lat": cfg.Lat, "lon": cfg.Lon,
			},
			"needs_passcode": cwop.ClassifyID(cfg.ID) == cwop.IDHam,
			"status":         s.opts.Relay.CWOP(r.Context()),
		},
	})
}

func (s *Server) handleCWOPPasscodePut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Passcode string `json:"passcode"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if err := s.opts.Relay.SetCWOPPasscode(in.Passcode); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passcode_set": true})
}

func (s *Server) handleCWOPPasscodeDelete(w http.ResponseWriter, _ *http.Request) {
	if err := s.opts.Relay.ClearCWOPPasscode(); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passcode_set": false})
}
