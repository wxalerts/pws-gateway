package web

import (
	"errors"
	"net/http"
)

// The claim endpoints. They sit behind the session and the CSRF header like
// every other mutating route. None of them returns or logs the device code or
// the MQTT password: the claim package keeps those on disk only.

func (s *Server) handleClaim(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.opts.Claim.Claim())
}

func (s *Server) handleClaimStart(w http.ResponseWriter, r *http.Request) {
	st, err := s.opts.Claim.ClaimStart(r.Context())
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, st)
	case errors.Is(err, ErrClaimLinked):
		writeErr(w, http.StatusConflict, "already_linked", "This gateway is already linked. Unlink it first to start over.")
	case errors.Is(err, ErrClaimTaken):
		writeErr(w, http.StatusConflict, "linked_on_server", "WxAlerts still lists this gateway on an account. Remove it there, then try again.")
	case errors.Is(err, ErrClaimBusy):
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "WxAlerts asked the gateway to wait. Try again in a minute.")
	default:
		s.log.Warn("claim start failed", "err", err)
		writeErr(w, http.StatusBadGateway, "unreachable", "Could not reach WxAlerts. Check the internet connection and try again.")
	}
}

func (s *Server) handleClaimCancel(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.opts.Claim.ClaimCancel())
}

func (s *Server) handleClaimUnlink(w http.ResponseWriter, _ *http.Request) {
	if err := s.opts.Claim.ClaimUnlink(); err != nil {
		s.log.Error("unlink failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "unlink_failed", "Could not remove the link. Check the logs.")
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Claim.Claim())
}
