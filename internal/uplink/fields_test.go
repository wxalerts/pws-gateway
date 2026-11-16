package uplink

import (
	"context"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/config"
)

// TestObsHonoursFieldSelection selects two stations from the real capture:
// the 5-in-1 with wind direction and rain unticked and raw passthrough turned
// off, and the 609TXC on its model default (temperature and humidity, no
// battery flag). A deselected field must not appear in f, and raw must be
// absent unless include_raw is on. The obs is validated against the schema by
// decodeObs.
func TestObsHonoursFieldSelection(t *testing.T) {
	const key609 = "Acurite-609TXC:224:"
	b := newBroker(t)
	r := newRig(t, t0)
	p := r.replayCapture()
	off := false
	if _, err := r.cfg.Update(func(c *config.Config) error {
		c.Stations[stationKey] = config.Station{
			Selected: true, IncludeRaw: &off,
			Fields: map[string]bool{"wind_dir_deg": false, "rain_mm": false},
		}
		c.Stations[key609] = config.Station{Selected: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.clock.Set(time.Date(2026, 11, 16, 17, 6, 30, 0, time.UTC))
	u := r.connected(b, func(o *Options) { o.Collector = p })
	if n, err := u.obsCycle(context.Background()); err != nil || n != 1 {
		t.Fatalf("obsCycle = %d, %v", n, err)
	}
	waitFor(t, 5*time.Second, "obs at the subscriber", func() bool { return b.count(obsTopic()) == 1 })
	o := decodeObs(t, b.messages(obsTopic())[0].Payload)

	seen5n1, seen609 := false, false
	for _, bk := range o.Buckets {
		for _, st := range bk.Stations {
			// The 609TXC never set include_raw, so selecting it turned raw on.
			if wantRaw := st.Key == key609; (st.Raw != nil) != wantRaw {
				t.Errorf("%s: raw present = %v, want %v", st.Key, st.Raw != nil, wantRaw)
			}
			switch st.Key {
			case stationKey:
				seen5n1 = true
				if st.F.WindDirDeg != nil || st.F.RainMM != nil {
					t.Errorf("deselected fields leaked: %+v", st.F)
				}
				if st.F.TempC == nil {
					t.Errorf("default field temp_c missing")
				}
			case key609:
				seen609 = true
				if st.F.TempC == nil {
					t.Errorf("609 temp_c missing")
				}
				if st.F.BatteryOK != nil {
					t.Errorf("609 battery_ok is not a default field")
				}
			}
		}
	}
	if !seen5n1 || !seen609 {
		t.Fatalf("both selected stations should be uplinked: 5n1=%v 609=%v", seen5n1, seen609)
	}
}
