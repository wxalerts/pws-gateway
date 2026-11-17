package update_test

import (
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/update"
)

func TestWindow(t *testing.T) {
	loc := time.FixedZone("CST", -6*3600)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 17, h, m, 0, 0, loc) }
	w, err := update.ParseWindow("02:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		h, m int
		in   bool
	}{{1, 59, false}, {2, 0, true}, {4, 59, true}, {5, 0, false}, {12, 0, false}} {
		if got := w.Contains(at(tc.h, tc.m), loc); got != tc.in {
			t.Errorf("%02d:%02d = %v", tc.h, tc.m, got)
		}
	}
	// The same instant seen in UTC is outside: the window is local time.
	if !w.Contains(at(3, 0).UTC(), loc) {
		t.Error("window must use the location, not the instant's zone")
	}
	if got := w.NextStart(at(12, 0), loc); !got.Equal(time.Date(2026, 11, 18, 2, 0, 0, 0, loc)) {
		t.Errorf("next start %v", got)
	}
	if got := w.NextStart(at(1, 0), loc); !got.Equal(at(2, 0)) {
		t.Errorf("next start %v", got)
	}

	wrap, _ := update.ParseWindow("22:00-04:00")
	for _, tc := range []struct {
		h  int
		in bool
	}{{21, false}, {22, true}, {23, true}, {0, true}, {3, true}, {4, false}} {
		if got := wrap.Contains(at(tc.h, 0), loc); got != tc.in {
			t.Errorf("wrap %02d = %v", tc.h, got)
		}
	}
	for _, bad := range []string{"", "2-5", "25:00-26:00", "02:00"} {
		if _, err := update.ParseWindow(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
