package auth

import (
	"context"
	"log/slog"
)

// Auditor writes one structured slog line per authentication event. Every
// line carries the attribute audit=true so operators can filter the ring
// buffer or journald for them. Passwords and session ids are never logged.
type Auditor struct {
	Logger *slog.Logger
}

func (a Auditor) log(event, user, addr string, level slog.Level, extra ...any) {
	l := a.Logger
	if l == nil {
		l = slog.Default()
	}
	args := append([]any{"audit", true, "event", event, "user", user, "remote", addr}, extra...)
	l.Log(context.Background(), level, "auth event", args...)
}

// SetupCreated records the first-run account creation.
func (a Auditor) SetupCreated(user, addr string) { a.log("setup_created", user, addr, slog.LevelInfo) }

// LoginSuccess records a successful login.
func (a Auditor) LoginSuccess(user, addr string) { a.log("login_success", user, addr, slog.LevelInfo) }

// LoginFailure records a wrong username or password.
func (a Auditor) LoginFailure(user, addr string, failures int) {
	a.log("login_failure", user, addr, slog.LevelWarn, "failures", failures)
}

// LoginLocked records an attempt rejected (or a failure that triggered) a
// lockout.
func (a Auditor) LoginLocked(user, addr string, lockout string) {
	a.log("login_locked", user, addr, slog.LevelWarn, "lockout", lockout)
}

// Logout records an explicit logout.
func (a Auditor) Logout(user, addr string) { a.log("logout", user, addr, slog.LevelInfo) }

// PasswordChanged records a successful password change.
func (a Auditor) PasswordChanged(user, addr string) {
	a.log("password_changed", user, addr, slog.LevelInfo)
}

// PasswordChangeFailed records a password change refused because the current
// password was wrong.
func (a Auditor) PasswordChangeFailed(user, addr string) {
	a.log("password_change_failed", user, addr, slog.LevelWarn)
}
