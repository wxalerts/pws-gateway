package rtl433

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// Replay feeds recorded rtl_433 lines into the same Line channel the
// Supervisor would, so everything downstream is exercised without a radio.
type Replay struct {
	// Radio is the radio id stamped on every emitted line.
	Radio string
	// Speed paces emission by the embedded timestamps: 1 is real time, 10 is
	// ten times faster, 0 (the default) emits as fast as the consumer reads.
	Speed float64
	// RewriteTime shifts every embedded timestamp so the first line lands at
	// Now() and later lines keep their original spacing. Use it for demos
	// where the aggregator runs on the real clock. Tests that drive a fake
	// clock leave it off and use the recorded times.
	RewriteTime bool
	// Location is used to read zone-less timestamps, as in Decoder.
	Location *time.Location

	// Test hooks. Nil means the real thing.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

var timeField = regexp.MustCompile(`"time"\s*:\s*"[^"]*"`)

// RunFile replays a .jsonl file.
func (r *Replay) RunFile(ctx context.Context, path string, out chan<- Line) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return r.Run(ctx, f, out)
}

// Run replays lines from src into out. Blank lines and lines starting with
// "#" are skipped so recordings can carry comments. It returns when src is
// exhausted or ctx ends. It does not close out.
func (r *Replay) Run(ctx context.Context, src io.Reader, out chan<- Line) error {
	now := r.Now
	if now == nil {
		now = time.Now
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}

	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var prev time.Time
	var offset time.Duration
	offsetSet := false
	lineNo := 0
	for sc.Scan() {
		lineNo++
		text := bytes.TrimSpace(sc.Bytes())
		if len(text) == 0 || text[0] == '#' {
			continue
		}
		cp := make([]byte, len(text))
		copy(cp, text)

		at := now()
		emb, hasTime := embeddedTime(cp, r.Location)
		if hasTime {
			if r.Speed > 0 && !prev.IsZero() {
				gap := emb.Sub(prev)
				if gap > 0 {
					if err := sleep(ctx, time.Duration(float64(gap)/r.Speed)); err != nil {
						return err
					}
				}
			}
			prev = emb
			if r.RewriteTime {
				if !offsetSet {
					offset = now().Sub(emb)
					offsetSet = true
				}
				emb = emb.Add(offset)
				cp = timeField.ReplaceAll(cp, []byte(`"time" : "`+FormatTime(emb)+`"`))
			}
			at = emb
		}
		select {
		case out <- Line{Radio: r.Radio, At: at, Text: cp}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("replay: line %d: %w", lineNo, err)
	}
	return nil
}

// ReadLines loads a recording into memory, skipping blanks and comments.
// Tests use it to count expected messages.
func ReadLines(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, []byte(l))
	}
	return out, nil
}

// embeddedTime extracts the time field without a full decode so replay
// stays cheap for large recordings.
func embeddedTime(line []byte, loc *time.Location) (time.Time, bool) {
	m := timeField.Find(line)
	if m == nil {
		return time.Time{}, false
	}
	// m looks like `"time" : "2024-..."`; take the last quoted value.
	q := bytes.LastIndexByte(m[:len(m)-1], '"')
	if q < 0 {
		return time.Time{}, false
	}
	t, err := ParseTime(string(m[q+1:len(m)-1]), loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
