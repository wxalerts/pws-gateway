package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/wxalerts/pws-gateway/internal/auth"
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"needs_setup": !s.opts.Auth.Exists()})
}

func (s *Server) handleSetupCreate(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decodeBody(w, r, &in) {
		return
	}
	if s.opts.Auth.Exists() {
		writeErr(w, http.StatusConflict, "already_setup", "setup has already been completed")
		return
	}
	if err := s.opts.Auth.Create(in.Username, in.Password); err != nil {
		switch {
		case errors.Is(err, auth.ErrAccountExists):
			writeErr(w, http.StatusConflict, "already_setup", "setup has already been completed")
		case errors.Is(err, auth.ErrUsernameInvalid):
			writeErr(w, http.StatusBadRequest, "invalid_username", err.Error())
		case errors.Is(err, auth.ErrPasswordShort), errors.Is(err, auth.ErrPasswordLong), errors.Is(err, auth.ErrPasswordInvalid):
			writeErr(w, http.StatusBadRequest, "weak_password", err.Error())
		default:
			s.log.Error("web: create account", "err", err)
			writeErr(w, http.StatusInternalServerError, "internal", "could not save the account")
		}
		return
	}
	s.opts.Auditor.SetupCreated(in.Username, remoteAddr(r))
	if !s.startSession(w, in.Username) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": in.Username})
}

func (s *Server) startSession(w http.ResponseWriter, username string) bool {
	id, err := s.opts.Sessions.Create(username)
	if err != nil {
		s.log.Error("web: create session", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not create a session")
		return false
	}
	s.setSessionCookie(w, id)
	return true
}

func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	if sess, ok := s.currentSession(r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": sess.Username})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false, "username": ""})
}

// rejectLocked answers 429 when addr is locked out and reports whether it did.
func (s *Server) rejectLocked(w http.ResponseWriter, user, addr string) bool {
	ok, retry := s.opts.Limiter.Check(addr)
	if ok {
		return false
	}
	secs := int(retry.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	s.opts.Auditor.LoginLocked(user, addr, retry.Round(time.Second).String())
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts, retry in "+strconv.Itoa(secs)+" s")
	return true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decodeBody(w, r, &in) {
		return
	}
	addr := remoteAddr(r)
	if s.rejectLocked(w, in.Username, addr) {
		return
	}
	if !s.opts.Auth.Exists() {
		writeErr(w, http.StatusConflict, "needs_setup", "create the admin account first")
		return
	}
	if !s.opts.Auth.Verify(in.Username, in.Password) {
		locked, d := s.opts.Limiter.Failure(addr)
		s.opts.Auditor.LoginFailure(in.Username, addr, s.opts.Limiter.Failures(addr))
		if locked {
			s.opts.Auditor.LoginLocked(in.Username, addr, d.String())
		}
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "username or password is wrong")
		return
	}
	s.opts.Limiter.Success(addr)
	if !s.startSession(w, in.Username) {
		return
	}
	s.opts.Auditor.LoginSuccess(in.Username, addr)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": in.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		if sess, ok := s.opts.Sessions.Get(c.Value); ok {
			s.opts.Auditor.Logout(sess.Username, remoteAddr(r))
		}
		if err := s.opts.Sessions.Delete(c.Value); err != nil {
			s.log.Warn("web: delete session", "err", err)
		}
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	user, addr := usernameFrom(r.Context()), remoteAddr(r)
	if s.rejectLocked(w, user, addr) {
		return
	}
	if !s.opts.Auth.Verify(user, in.Current) {
		s.opts.Limiter.Failure(addr)
		s.opts.Auditor.PasswordChangeFailed(user, addr)
		writeErr(w, http.StatusForbidden, "invalid_credentials", "current password is wrong")
		return
	}
	if err := s.opts.Auth.SetPassword(user, in.New); err != nil {
		switch {
		case errors.Is(err, auth.ErrPasswordShort), errors.Is(err, auth.ErrPasswordLong), errors.Is(err, auth.ErrPasswordInvalid):
			writeErr(w, http.StatusBadRequest, "weak_password", err.Error())
		default:
			s.log.Error("web: set password", "err", err)
			writeErr(w, http.StatusInternalServerError, "internal", "could not save the password")
		}
		return
	}
	s.opts.Limiter.Success(addr)
	// Other browsers must log in again with the new password; this one stays in.
	if err := s.opts.Sessions.DeleteAll(); err != nil {
		s.log.Warn("web: clear sessions", "err", err)
	}
	if !s.startSession(w, user) {
		return
	}
	s.opts.Auditor.PasswordChanged(user, addr)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
