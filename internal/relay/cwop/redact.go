package cwop

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

// Redacted replaces a secret in text.
const Redacted = "[redacted]"

var rePass = regexp.MustCompile(`(?i)(\bpass(?:code)?[ =:]+)\S+`)

// Redactor removes the passcode from text. It knows the exact secrets and also
// masks the value after "pass" in any login line, so a credential that was
// changed since still cannot leak through an old error string.
type Redactor struct{ secrets []string }

// NewRedactor builds a Redactor for the given secrets. Empty strings and the
// public non-ham constant are ignored.
func NewRedactor(secrets ...string) *Redactor {
	r := &Redactor{}
	for _, s := range secrets {
		if s = strings.TrimSpace(s); s != "" && s != NonHamPasscode {
			r.secrets = append(r.secrets, s)
		}
	}
	return r
}

// Redact returns s with secrets and login passcodes masked.
func (r *Redactor) Redact(s string) string {
	for _, sec := range r.secrets {
		s = strings.ReplaceAll(s, sec, Redacted)
	}
	return rePass.ReplaceAllString(s, "${1}"+Redacted)
}

// redactingHandler masks secrets in every message and string attribute.
type redactingHandler struct {
	next slog.Handler
	r    *Redactor
}

// RedactingLogger wraps l so that nothing it writes, to stderr or to the log
// ring served by the web UI, can contain the passcode.
func RedactingLogger(l *slog.Logger, r *Redactor) *slog.Logger {
	return slog.New(&redactingHandler{next: l.Handler(), r: r})
}

func (h *redactingHandler) Enabled(ctx context.Context, lv slog.Level) bool {
	return h.next.Enabled(ctx, lv)
}

func (h *redactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.Redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *redactingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(as))
	for i, a := range as {
		red[i] = h.attr(a)
	}
	return &redactingHandler{next: h.next.WithAttrs(red), r: h.r}
}

func (h *redactingHandler) WithGroup(n string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(n), r: h.r}
}

func (h *redactingHandler) attr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.r.Redact(a.Value.String()))
	case slog.KindGroup:
		g := a.Value.Group()
		red := make([]slog.Attr, len(g))
		for i, x := range g {
			red[i] = h.attr(x)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(red...)}
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			return slog.String(a.Key, h.r.Redact(err.Error()))
		}
		return a
	}
	return a
}
