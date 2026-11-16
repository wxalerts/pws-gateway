package rtl433

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrBadTime is returned when a time string matches none of the rtl_433
// formats.
var ErrBadTime = errors.New("rtl433: unrecognised time format")

// Time formats rtl_433 can produce, from src/r_api.c time_pos_str and
// src/r_util.c format_time_str / usecs_time_str (checked 2026-11-16):
//
//	default:            "%Y-%m-%d %H:%M:%S"          local time, no zone
//	-M time:iso         "%Y-%m-%dT%H:%M:%S"
//	-M time:usec        appends ".%06ld"
//	-M time:utc         formats in UTC instead of local
//	-M time:tz          appends "%z", with "+0000" rewritten to "Z"
//	-M time:unix        "%s", seconds since the epoch, with ".usec" if usec
//
// Go accepts an optional fractional second when parsing even if the layout
// has none, so one layout per separator and zone style is enough.
var timeLayouts = []struct {
	layout string
	zoned  bool
}{
	{"2006-01-02T15:04:05Z0700", true},
	{"2006-01-02 15:04:05Z0700", true},
	{"2006-01-02T15:04:05Z07:00", true},
	{"2006-01-02T15:04:05", false},
	{"2006-01-02 15:04:05", false},
}

// ParseTime parses an rtl_433 timestamp. Strings without a zone are read in
// loc (nil means time.Local). The result is always in UTC.
func ParseTime(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, ErrBadTime
	}
	if loc == nil {
		loc = time.Local
	}
	if isUnix(s) {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return time.Time{}, ErrBadTime
		}
		sec, frac := math.Modf(f)
		return time.Unix(int64(sec), int64(math.Round(frac*1e6))*1000).UTC(), nil
	}
	for _, l := range timeLayouts {
		var t time.Time
		var err error
		if l.zoned {
			t, err = time.Parse(l.layout, s)
		} else {
			t, err = time.ParseInLocation(l.layout, s, loc)
		}
		if err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, ErrBadTime
}

// FormatTime writes a timestamp the way "-M time:iso:usec:utc:tz" does, which
// is the format the supervisor requests and the format recordings use.
func FormatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000Z")
}

func isUnix(s string) bool {
	if s == "" {
		return false
	}
	dot := false
	for i, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c == '.' && !dot && i > 0:
			dot = true
		default:
			return false
		}
	}
	return true
}
