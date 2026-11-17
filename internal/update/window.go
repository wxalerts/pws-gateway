package update

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Window is a daily local-time interval in which updates may start. A window
// whose end is before its start crosses midnight. Start equal to end means
// the whole day.
type Window struct {
	start, end int // minutes after local midnight
}

// ParseWindow reads "HH:MM-HH:MM", the format of config updates.window.
func ParseWindow(s string) (Window, error) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok {
		return Window{}, fmt.Errorf("window %q must be HH:MM-HH:MM", s)
	}
	st, err1 := parseHM(a)
	en, err2 := parseHM(b)
	if err1 != nil || err2 != nil {
		return Window{}, fmt.Errorf("window %q must be HH:MM-HH:MM", s)
	}
	return Window{start: st, end: en}, nil
}

func parseHM(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, fmt.Errorf("bad time %q", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	return hh*60 + mm, nil
}

// Contains reports whether t, in loc, lies inside the window.
func (w Window) Contains(t time.Time, loc *time.Location) bool {
	lt := t.In(loc)
	m := lt.Hour()*60 + lt.Minute()
	switch {
	case w.start == w.end:
		return true
	case w.start < w.end:
		return m >= w.start && m < w.end
	default:
		return m >= w.start || m < w.end
	}
}

// NextStart returns the next instant at or after t when the window opens. It
// returns t itself when the window is open now.
func (w Window) NextStart(t time.Time, loc *time.Location) time.Time {
	if w.Contains(t, loc) {
		return t
	}
	lt := t.In(loc)
	open := time.Date(lt.Year(), lt.Month(), lt.Day(), w.start/60, w.start%60, 0, 0, loc)
	if !open.After(lt) {
		open = open.AddDate(0, 0, 1)
	}
	return open
}
