package auth

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func newClock() *clock                   { return &clock{t: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)} }
func (c *clock) Now() time.Time          { return c.t }
func (c *clock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery", TestParams)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Verify("correct horse battery") {
		t.Fatal("correct password rejected")
	}
	if h.Verify("correct horse batterz") || h.Verify("") {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery", TestParams)
	if bytes.Equal(h.Salt, h2.Salt) || bytes.Equal(h.Hash, h2.Hash) {
		t.Fatal("salt must differ per hash")
	}
	if !strings.HasPrefix(h.PHC(), "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("PHC = %q", h.PHC())
	}
	if (Hash{}).Verify("x") {
		t.Fatal("empty hash must not verify")
	}
	if _, err := HashPassword("x", Params{}); err == nil {
		t.Fatal("zero params must be rejected")
	}
}

func TestPasswordPolicy(t *testing.T) {
	cases := []struct {
		pw   string
		want error
	}{
		{"short", ErrPasswordShort},
		{"elevenchars", ErrPasswordShort},
		{"twelve chars", nil},
		{"éééééééééééé", nil}, // 12 code points, 24 bytes
		{string([]byte{0xff, 0xfe, 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j'}), ErrPasswordInvalid},
		{strings.Repeat("a", MaxPasswordBytes+1), ErrPasswordLong},
		{strings.Repeat("a", MaxPasswordBytes), nil},
	}
	for _, c := range cases {
		if err := CheckPassword(c.pw); !errors.Is(err, c.want) {
			t.Errorf("CheckPassword(%.20q) = %v, want %v", c.pw, err, c.want)
		}
	}
	for _, u := range []string{"admin", "a.b_c-d@e", strings.Repeat("a", 64)} {
		if err := CheckUsername(u); err != nil {
			t.Errorf("CheckUsername(%q) = %v", u, err)
		}
	}
	for _, u := range []string{"", "has space", "semi;colon", strings.Repeat("a", 65)} {
		if !errors.Is(CheckUsername(u), ErrUsernameInvalid) {
			t.Errorf("CheckUsername(%q) accepted", u)
		}
	}
}

func TestStoreCreateVerifyPersist(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, WithParams(TestParams))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if s.Exists() || s.Username() != "" {
		t.Fatal("fresh store must have no account")
	}
	if s.Verify("admin", "whatever long password") {
		t.Fatal("verify without account must fail")
	}
	if err := s.Create("admin", "short"); !errors.Is(err, ErrPasswordShort) {
		t.Fatalf("short password err = %v", err)
	}
	if s.Exists() {
		t.Fatal("rejected password must not create an account")
	}
	if err := s.Create("admin", "a long enough password"); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("other", "another long password"); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("second create err = %v", err)
	}
	if !s.Verify("admin", "a long enough password") {
		t.Fatal("valid login rejected")
	}
	if s.Verify("admin", "a long enough passwore") || s.Verify("Admin", "a long enough password") {
		t.Fatal("mismatch accepted")
	}
	if s.Verify("admin", strings.Repeat("x", MaxPasswordBytes+10)) {
		t.Fatal("oversized password accepted")
	}

	fi, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("auth.json mode = %v", fi.Mode().Perm())
	}
	raw, _ := os.ReadFile(filepath.Join(dir, FileName))
	if bytes.Contains(raw, []byte("a long enough password")) {
		t.Fatal("plaintext password on disk")
	}

	s2, _ := NewStore(dir, WithParams(TestParams))
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if s2.Username() != "admin" || !s2.Verify("admin", "a long enough password") {
		t.Fatal("account did not survive reload")
	}

	if err := s2.SetPassword("admin", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if s2.Verify("admin", "a long enough password") || !s2.Verify("admin", "a brand new password") {
		t.Fatal("password change not effective")
	}
}

func TestStoreLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := NewStore(dir, WithParams(TestParams))
	if err := s.Load(); err == nil {
		t.Fatal("corrupt file must error")
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"username":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(); err == nil {
		t.Fatal("incomplete file must error")
	}
}

func TestSessionLifecycle(t *testing.T) {
	clk := newClock()
	dir := t.TempDir()
	ss := NewSessions(dir, SessionsOptions{Now: clk.Now, MaxLifetime: 10 * time.Hour, IdleTimeout: time.Hour})

	if _, ok := ss.Get(""); ok {
		t.Fatal("empty id accepted")
	}
	if _, ok := ss.Get("nope"); ok {
		t.Fatal("unknown id accepted")
	}
	id, err := ss.Create("admin")
	if err != nil {
		t.Fatal(err)
	}
	if sess, ok := ss.Get(id); !ok || sess.Username != "admin" {
		t.Fatalf("Get = %+v %v", sess, ok)
	}

	// Activity inside the idle window keeps the session alive past it.
	for i := 0; i < 4; i++ {
		clk.Advance(50 * time.Minute)
		if _, ok := ss.Get(id); !ok {
			t.Fatalf("session dropped during activity at step %d", i)
		}
	}
	// Idle timeout.
	clk.Advance(61 * time.Minute)
	if _, ok := ss.Get(id); ok {
		t.Fatal("idle session still valid")
	}
	if ss.Count() != 0 {
		t.Fatalf("expired session not removed, count %d", ss.Count())
	}

	// Absolute lifetime, even with constant activity.
	id, _ = ss.Create("admin")
	for i := 0; i < 11; i++ {
		clk.Advance(55 * time.Minute)
		if _, ok := ss.Get(id); !ok {
			if i < 10 {
				t.Fatalf("expired too early at step %d", i)
			}
			return
		}
	}
	t.Fatal("session outlived its maximum lifetime")
}

func TestSessionDeleteAndPrune(t *testing.T) {
	clk := newClock()
	ss := NewSessions(t.TempDir(), SessionsOptions{Now: clk.Now, IdleTimeout: time.Hour})
	a, _ := ss.Create("admin")
	b, _ := ss.Create("admin")
	if err := ss.Delete(a); err != nil {
		t.Fatal(err)
	}
	if _, ok := ss.Get(a); ok {
		t.Fatal("deleted session valid")
	}
	if err := ss.Delete("missing"); err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * time.Hour)
	if n := ss.Prune(); n != 1 {
		t.Fatalf("Prune = %d", n)
	}
	c, _ := ss.Create("admin")
	if err := ss.DeleteAll(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{b, c} {
		if _, ok := ss.Get(id); ok {
			t.Fatal("session survived DeleteAll")
		}
	}
}

func TestSessionPersistence(t *testing.T) {
	clk := newClock()
	dir := t.TempDir()
	opts := SessionsOptions{Now: clk.Now, IdleTimeout: time.Hour}
	ss := NewSessions(dir, opts)
	live, _ := ss.Create("admin")

	raw, err := os.ReadFile(filepath.Join(dir, SessionsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(live)) {
		t.Fatal("raw session id found in persisted file")
	}

	ss2 := NewSessions(dir, opts)
	if err := ss2.Load(); err != nil {
		t.Fatal(err)
	}
	if sess, ok := ss2.Get(live); !ok || sess.Username != "admin" {
		t.Fatal("session lost across reload")
	}

	// Sessions that expired while the daemon was down are dropped on load.
	clk.Advance(3 * time.Hour)
	ss3 := NewSessions(dir, opts)
	if err := ss3.Load(); err != nil {
		t.Fatal(err)
	}
	if ss3.Count() != 0 {
		t.Fatalf("expired sessions loaded: %d", ss3.Count())
	}

	// A missing file is fine; a corrupt one is reported.
	if err := NewSessions(t.TempDir(), opts).Load(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SessionsFileName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewSessions(dir, opts).Load(); err == nil {
		t.Fatal("corrupt sessions file must error")
	}
}

func TestLimiterLockout(t *testing.T) {
	clk := newClock()
	l := NewLimiter(LimiterOptions{Threshold: 3, BaseLockout: 10 * time.Second, MaxLockout: 40 * time.Second, Now: clk.Now})
	const addr = "192.0.2.1"

	if ok, _ := l.Check(addr); !ok {
		t.Fatal("fresh address blocked")
	}
	for i := 0; i < 2; i++ {
		if locked, _ := l.Failure(addr); locked {
			t.Fatalf("locked after %d failures", i+1)
		}
	}
	locked, d := l.Failure(addr)
	if !locked || d != 10*time.Second {
		t.Fatalf("third failure: locked=%v d=%v", locked, d)
	}
	if ok, retry := l.Check(addr); ok || retry != 10*time.Second {
		t.Fatalf("Check during lockout = %v %v", ok, retry)
	}
	if ok, _ := l.Check("192.0.2.2"); !ok {
		t.Fatal("lockout leaked to another address")
	}

	clk.Advance(10 * time.Second)
	if ok, _ := l.Check(addr); !ok {
		t.Fatal("lockout did not expire")
	}
	// Doubling and the cap.
	want := []time.Duration{20 * time.Second, 40 * time.Second, 40 * time.Second}
	for i, w := range want {
		if _, d := l.Failure(addr); d != w {
			t.Fatalf("failure %d lockout = %v, want %v", i, d, w)
		}
		clk.Advance(w)
	}
	if l.Failures(addr) != 6 {
		t.Fatalf("Failures = %d", l.Failures(addr))
	}
	l.Success(addr)
	if l.Failures(addr) != 0 {
		t.Fatal("Success did not clear history")
	}
	if locked, _ := l.Failure(addr); locked {
		t.Fatal("history not reset")
	}
}

func TestLimiterForgetsQuietAddresses(t *testing.T) {
	clk := newClock()
	l := NewLimiter(LimiterOptions{ForgetAfter: time.Hour, Now: clk.Now})
	l.Failure("a")
	clk.Advance(2 * time.Hour)
	l.Failure("b") // triggers pruning
	if l.Failures("a") != 0 {
		t.Fatal("quiet address not forgotten")
	}
}

func TestAuditLines(t *testing.T) {
	var buf bytes.Buffer
	a := Auditor{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	a.SetupCreated("admin", "192.0.2.1")
	a.LoginSuccess("admin", "192.0.2.1")
	a.LoginFailure("admin", "192.0.2.1", 2)
	a.LoginLocked("admin", "192.0.2.1", "30s")
	a.Logout("admin", "192.0.2.1")
	a.PasswordChanged("admin", "192.0.2.1")
	a.PasswordChangeFailed("admin", "192.0.2.1")
	out := buf.String()
	for _, ev := range []string{"setup_created", "login_success", "login_failure", "login_locked", "logout", "password_changed", "password_change_failed"} {
		if !strings.Contains(out, "event="+ev) {
			t.Errorf("missing audit event %s in:\n%s", ev, out)
		}
	}
	if !strings.Contains(out, "audit=true") || !strings.Contains(out, "remote=192.0.2.1") || !strings.Contains(out, "failures=2") {
		t.Errorf("audit attributes missing:\n%s", out)
	}
	if strings.Count(out, "level=WARN") != 3 {
		t.Errorf("expected 3 WARN lines:\n%s", out)
	}
}
