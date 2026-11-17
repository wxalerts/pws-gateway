package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/aggregate"
	"github.com/wxalerts/pws-gateway/internal/atomicfile"
	"github.com/wxalerts/pws-gateway/internal/relay"
	"github.com/wxalerts/pws-gateway/internal/relay/cwop"
	"github.com/wxalerts/pws-gateway/internal/uplink"
	"github.com/wxalerts/pws-gateway/internal/version"
	"github.com/wxalerts/pws-gateway/internal/web"
)

// relaySettle keeps the newest closed bucket out of a relay until the writer
// has stored it, like the uplink does.
const relaySettle = time.Minute

func (a *App) setupRelay() {
	a.cwop = cwop.NewRunner(cwop.RunnerOptions{
		Config:       func() cwop.Config { return a.deps.Config.Get().Relay.CWOP },
		Source:       relaySource{a: a},
		Sender:       cwop.TCPSender{},
		LoadPasscode: func(c cwop.Config) (string, error) { return a.readPasscode(c) },
		TimeSynced:   a.timeSynced(),
		Version:      version.Version,
		Now:          a.now,
		Logger:       a.log,
	})
}

func (a *App) passcodePath(c cwop.Config) string {
	name := c.CredentialFile
	if name == "" {
		name = "cwop.credential"
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(a.deps.DataDir, name)
}

func (a *App) readPasscode(c cwop.Config) (string, error) {
	b, err := os.ReadFile(a.passcodePath(c))
	return strings.TrimSpace(string(b)), err
}

// relaySource adapts the rollup store to cwop.Source.
type relaySource struct{ a *App }

// Latest returns the newest closed bucket of the station and the rolling rain
// totals, or ok=false when there is no data yet.
func (r relaySource) Latest(ctx context.Context, key string, now time.Time) (relay.Observation, bool, error) {
	a := r.a
	to := aggregate.Align(now.UTC().Add(-relaySettle), uplink.BucketWidth)
	rolls, err := a.store.Rollups(ctx, key, to.Add(-uplink.BucketWidth), to)
	if err != nil || len(rolls) == 0 {
		return relay.Observation{}, false, err
	}
	ann := a.deps.Config.Get().Stations[key]
	obs := relay.Observation{
		Start: rolls[0].Start, IntervalS: int(uplink.BucketWidth / time.Second),
		Station: uplink.StationFromRollup(&rolls[0], ann),
	}
	day, err := a.store.Rollups(ctx, key, to.Add(-24*time.Hour), to)
	if err != nil {
		return relay.Observation{}, false, err
	}
	y, m, d := now.In(time.Local).Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	var tot relay.RainTotals
	seen := false
	for i := range day {
		agg, ok := day[i].Fields["rain_mm"]
		if !ok {
			continue
		}
		seen = true
		tot.Last24hMM += agg.Sum
		if !day[i].Start.Before(to.Add(-time.Hour)) {
			tot.Last1hMM += agg.Sum
		}
		if !day[i].Start.Before(midnight) {
			tot.SinceMidnightMM += agg.Sum
		}
	}
	if seen {
		obs.Rain = &tot
	}
	return obs, true, nil
}

// Relay returns the web.RelaySource.
func (a *App) Relay() web.RelaySource { return relayAdapter{a: a} }

type relayAdapter struct{ a *App }

func (r relayAdapter) CWOP(context.Context) cwop.Status {
	c := r.a.deps.Config.Get().Relay.CWOP
	pass, err := r.a.readPasscode(c)
	return r.a.cwop.Status(err == nil && pass != "")
}

func (r relayAdapter) SetCWOPPasscode(pass string) error {
	pass = strings.TrimSpace(pass)
	if pass == "" || strings.ContainsAny(pass, "\r\n\x00") || len(pass) > 64 {
		return errors.New("passcode must be 1 to 64 characters on one line")
	}
	c := r.a.deps.Config.Get().Relay.CWOP
	return atomicfile.WriteFile(r.a.passcodePath(c), []byte(pass+"\n"), 0o600)
}

func (r relayAdapter) ClearCWOPPasscode() error {
	c := r.a.deps.Config.Get().Relay.CWOP
	err := os.Remove(r.a.passcodePath(c))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
