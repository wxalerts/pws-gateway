package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector"
	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/web"
)

// stationSource adapts the pipeline's discovery state to web.StationSource.
type stationSource struct {
	p   *collector.Pipeline
	now func() time.Time
}

func (s stationSource) Stations(context.Context) ([]web.StationInfo, error) {
	sts := s.p.Stations(s.now())
	out := make([]web.StationInfo, 0, len(sts))
	for i := range sts {
		st := &sts[i]
		id := 0
		if n, err := strconv.Atoi(st.ID); err == nil { // hex ids stay 0; the key still identifies them
			id = n
		}
		out = append(out, web.StationInfo{
			Key:     st.Key,
			Model:   st.Model,
			ID:      id,
			Channel: strings.TrimSpace(st.Channel),
			// LastSeen is the pipeline's stamp, never the wall clock here.
			LastSeen: st.LastSeen,
			RSSIdB:   st.LastSignal.RSSI,
			SNRdB:    st.LastSignal.SNR,
			// nil means the device never reported a battery flag.
			BatteryOK:  st.BatteryOK,
			Fields:     st.Fields,
			RawFields:  st.RawFields,
			NSightings: st.Sightings,
		})
	}
	return out, nil
}

// readingsSource adapts the pipeline's recent readings to web.ReadingsSource.
type readingsSource struct{ p *collector.Pipeline }

func (r readingsSource) Readings(_ context.Context, station string, since time.Time) ([]web.Reading, error) {
	rs := r.p.Readings(station, since)
	out := make([]web.Reading, 0, len(rs))
	for i := range rs {
		out = append(out, web.Reading{TS: rs[i].Time.UTC(), F: rs[i].Fields})
	}
	return out, nil
}

// statusSource implements web.StatusSource. Uplink and update state come from
// the uplink when the device is claimed, else from the Nop defaults. The USB reset count is read
// by the web layer straight from the platform.
type statusSource struct {
	web.NopStatus
	app *App
}

func (s statusSource) Outbox(ctx context.Context) web.Outbox {
	st, err := s.app.pipeline.OutboxStatus(ctx)
	if err != nil {
		s.app.log.Warn("outbox status failed", "err", err)
		return web.Outbox{}
	}
	o := web.Outbox{Depth: int(st.Count)}
	if !st.Oldest.IsZero() {
		if age := s.app.now().Sub(st.Oldest); age > 0 {
			o.OldestS = int(age.Seconds())
		}
	}
	return o
}

// Uplink reports the MQTT connection state, empty while the device is unclaimed.
func (s statusSource) Uplink(context.Context) web.UplinkStatus { return s.app.uplinkStatus() }

// Update reports the retained update state the uplink publishes.
func (s statusSource) Update(ctx context.Context) web.UpdateStatus {
	if u, ok := s.app.updateStatus(); ok {
		return u
	}
	return s.NopStatus.Update(ctx)
}

// RadioStatus reports "running", "starting" or "error: <reason>" for a
// supervised dongle and "idle" for one that has no supervisor (disabled, or
// left alone because several dongles are present and none was configured).
func (s statusSource) RadioStatus(_ context.Context, serial string) string {
	sup := s.app.supervisor(serial)
	if sup == nil {
		return "idle"
	}
	return radioState(sup.Status())
}

func radioState(st rtl433.Status) string {
	switch {
	case st.Running:
		return "running"
	case st.LastError != "":
		return "error: " + st.LastError
	default:
		return "starting"
	}
}

// Claim returns the web.ClaimSource backed by the claim manager.
func (a *App) Claim() web.ClaimSource { return web.NewClaimSource(a.claim) }
