package discovery

import (
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

var t0 = time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)

func rd(at time.Time, id string, fields map[string]float64) model.Reading {
	return model.Reading{Model: "Acurite-5n1", ID: id, Channel: "A", Time: at, Radio: "radio0", Fields: fields,
		Signal: model.Signal{RSSI: -7, SNR: 20, Noise: -27, HasLevel: true}}
}

type recorder struct{ events []Event }

func (r *recorder) add(e Event) { r.events = append(r.events, e) }

func TestPromotionAfterThreeConsistentSightings(t *testing.T) {
	var rec recorder
	tr := New(Config{Events: rec.add})
	for i := 0; i < 2; i++ {
		res := tr.Observe(rd(t0.Add(time.Duration(i)*18*time.Second), "1", map[string]float64{model.FieldTempC: 20 + float64(i)*0.1}))
		if res.Discovered || res.Promoted || res.Rejected != "" {
			t.Fatalf("sighting %d = %+v", i, res)
		}
		if len(tr.Stations(t0)) != 0 {
			t.Fatalf("station listed after %d sightings", i+1)
		}
	}
	if tr.CandidateCount() != 1 {
		t.Fatalf("candidates = %d", tr.CandidateCount())
	}
	res := tr.Observe(rd(t0.Add(36*time.Second), "1", map[string]float64{model.FieldTempC: 20.2}))
	if !res.Discovered || !res.Promoted {
		t.Fatalf("third sighting = %+v", res)
	}
	st := tr.Stations(t0.Add(36 * time.Second))
	if len(st) != 1 || st[0].Key != "Acurite-5n1:1:A" || st[0].Sightings != 3 {
		t.Fatalf("stations = %+v", st)
	}
	if !st[0].FirstSeen.Equal(t0) || !st[0].LastSeen.Equal(t0.Add(36*time.Second)) {
		t.Fatalf("seen = %v %v", st[0].FirstSeen, st[0].LastSeen)
	}
	if st[0].DecodeRate != 3.0/15 {
		t.Fatalf("rate = %v", st[0].DecodeRate)
	}
	if len(st[0].Fields) != 1 || st[0].Fields[0] != model.FieldTempC || st[0].LastSignal.RSSI != -7 || st[0].Radio != "radio0" {
		t.Fatalf("station = %+v", st[0])
	}
	if len(rec.events) != 1 || rec.events[0].Type != EventSensorFound {
		t.Fatalf("events = %+v", rec.events)
	}
	if tr.CandidateCount() != 0 || !tr.IsDiscovered("Acurite-5n1:1:A") {
		t.Fatal("candidate should have moved to stations")
	}
	// Later readings keep counting.
	tr.Observe(rd(t0.Add(54*time.Second), "1", map[string]float64{model.FieldTempC: 20.3}))
	if got, _ := tr.Lookup("Acurite-5n1:1:A", t0.Add(time.Minute)); got.Sightings != 4 {
		t.Fatalf("sightings = %d", got.Sightings)
	}
}

func TestImplausibleValuesNeverCountAndResetCandidates(t *testing.T) {
	tr := New(Config{})
	tr.Observe(rd(t0, "9", map[string]float64{model.FieldTempC: 20}))
	tr.Observe(rd(t0.Add(time.Second), "9", map[string]float64{model.FieldTempC: 20}))
	res := tr.Observe(rd(t0.Add(2*time.Second), "9", map[string]float64{model.FieldTempC: 300}))
	if res.Rejected != model.FieldTempC || res.Discovered {
		t.Fatalf("implausible = %+v", res)
	}
	// Two more good ones are not enough: the counter restarted.
	tr.Observe(rd(t0.Add(3*time.Second), "9", map[string]float64{model.FieldTempC: 20}))
	res = tr.Observe(rd(t0.Add(4*time.Second), "9", map[string]float64{model.FieldTempC: 20}))
	if res.Discovered {
		t.Fatal("promoted too early after an implausible sighting")
	}
	res = tr.Observe(rd(t0.Add(5*time.Second), "9", map[string]float64{model.FieldTempC: 20}))
	if !res.Promoted {
		t.Fatalf("should promote on third consistent sighting after reset: %+v", res)
	}
}

func TestGaugeJumpAndCounterDecreaseAreInconsistent(t *testing.T) {
	tr := New(Config{})
	tr.Observe(rd(t0, "2", map[string]float64{model.FieldTempC: 20, model.FieldRainMM: 10}))
	res := tr.Observe(rd(t0.Add(time.Second), "2", map[string]float64{model.FieldTempC: 45}))
	if res.Rejected != model.FieldTempC+" jumped" {
		t.Fatalf("jump = %+v", res)
	}
	tr.Observe(rd(t0.Add(2*time.Second), "2", map[string]float64{model.FieldRainMM: 10.5}))
	res = tr.Observe(rd(t0.Add(3*time.Second), "2", map[string]float64{model.FieldRainMM: 9.9}))
	if res.Rejected != model.FieldRainMM+" decreased" {
		t.Fatalf("decrease = %+v", res)
	}
	// A wrap to near zero is a reset and is fine.
	res = tr.Observe(rd(t0.Add(4*time.Second), "2", map[string]float64{model.FieldRainMM: 0.25}))
	if res.Rejected != "" {
		t.Fatalf("reset rejected: %+v", res)
	}
}

func TestDiscoveredStationIgnoresBadDecodeButStaysListed(t *testing.T) {
	tr := New(Config{})
	for i := 0; i < 3; i++ {
		tr.Observe(rd(t0.Add(time.Duration(i)*time.Second), "3", map[string]float64{model.FieldHumidityPct: 50}))
	}
	res := tr.Observe(rd(t0.Add(5*time.Second), "3", map[string]float64{model.FieldHumidityPct: 101}))
	if !res.Discovered || res.Rejected != model.FieldHumidityPct {
		t.Fatalf("bad decode = %+v", res)
	}
	st, ok := tr.Lookup("Acurite-5n1:3:A", t0.Add(5*time.Second))
	if !ok || st.Sightings != 3 || !st.LastSeen.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("station = %+v %v", st, ok)
	}
}

func TestLostAndFound(t *testing.T) {
	var rec recorder
	tr := New(Config{QuietPeriod: 10 * time.Minute, Events: rec.add})
	for i := 0; i < 3; i++ {
		tr.Observe(rd(t0.Add(time.Duration(i)*time.Second), "4", map[string]float64{model.FieldTempC: 20}))
	}
	tr.Tick(t0.Add(5 * time.Minute))
	if len(rec.events) != 1 {
		t.Fatalf("lost too early: %+v", rec.events)
	}
	tr.Tick(t0.Add(11 * time.Minute))
	if len(rec.events) != 2 || rec.events[1].Type != EventSensorLost || !rec.events[1].Station.Lost {
		t.Fatalf("events = %+v", rec.events)
	}
	tr.Tick(t0.Add(12 * time.Minute))
	if len(rec.events) != 2 {
		t.Fatal("lost should fire once")
	}
	st := tr.Stations(t0.Add(12 * time.Minute))
	if !st[0].Lost {
		t.Fatalf("station = %+v", st[0])
	}
	// The rate window is still counting the three sightings; it decays to
	// zero on its own once they age out.
	if st[0].DecodeRate != 3.0/15 {
		t.Fatalf("rate = %v", st[0].DecodeRate)
	}
	if later := tr.Stations(t0.Add(20 * time.Minute)); later[0].DecodeRate != 0 {
		t.Fatalf("rate after window = %v", later[0].DecodeRate)
	}
	tr.Observe(rd(t0.Add(13*time.Minute), "4", map[string]float64{model.FieldTempC: 21}))
	if len(rec.events) != 3 || rec.events[2].Type != EventSensorFound || rec.events[2].Station.Lost {
		t.Fatalf("events = %+v", rec.events)
	}
}

func TestCandidatesExpireAndEvict(t *testing.T) {
	tr := New(Config{QuietPeriod: time.Minute, MaxCandidates: 2})
	tr.Observe(rd(t0, "a", map[string]float64{model.FieldTempC: 1}))
	tr.Observe(rd(t0.Add(time.Second), "b", map[string]float64{model.FieldTempC: 1}))
	tr.Observe(rd(t0.Add(2*time.Second), "c", map[string]float64{model.FieldTempC: 1}))
	if tr.CandidateCount() != 2 {
		t.Fatalf("candidates = %d", tr.CandidateCount())
	}
	tr.Tick(t0.Add(2 * time.Minute))
	if tr.CandidateCount() != 0 {
		t.Fatalf("candidates after quiet = %d", tr.CandidateCount())
	}
}

func TestRawOnlyStationsAreDiscoverable(t *testing.T) {
	tr := New(Config{})
	for i := 0; i < 3; i++ {
		r := model.Reading{Model: "Unknown-Thing", ID: "7", Time: t0.Add(time.Duration(i) * time.Second), Raw: map[string]any{"foo": 1.0}}
		res := tr.Observe(r)
		if i == 2 && !res.Promoted {
			t.Fatalf("raw only not promoted: %+v", res)
		}
	}
	st := tr.Stations(t0)
	if len(st) != 1 || len(st[0].Fields) != 0 || len(st[0].RawFields) != 1 || st[0].RawFields[0] != "foo" {
		t.Fatalf("stations = %+v", st)
	}
}

func TestUnstampedIgnored(t *testing.T) {
	tr := New(Config{})
	res := tr.Observe(model.Reading{Model: "X", ID: "1"})
	if res.Rejected != "unstamped" || tr.CandidateCount() != 0 {
		t.Fatalf("unstamped = %+v", res)
	}
}

func TestRestoreAndRemove(t *testing.T) {
	var rec recorder
	tr := New(Config{Events: rec.add})
	tr.Restore([]model.Station{{Key: "X:1:", Model: "X", ID: "1", Fields: []string{model.FieldTempC}, Sightings: 50}})
	if len(rec.events) != 0 {
		t.Fatal("restore should not emit")
	}
	st := tr.Stations(t0)
	if len(st) != 1 || !st[0].Lost || st[0].Sightings != 50 {
		t.Fatalf("restored = %+v", st)
	}
	res := tr.Observe(model.Reading{Model: "X", ID: "1", Time: t0, Fields: map[string]float64{model.FieldTempC: 5}})
	if !res.Discovered || len(rec.events) != 1 || rec.events[0].Type != EventSensorFound {
		t.Fatalf("restored station heard = %+v %+v", res, rec.events)
	}
	if !tr.Remove("X:1:") || tr.IsDiscovered("X:1:") || tr.Remove("X:1:") {
		t.Fatal("remove")
	}
}

func TestBatteryFlagTracked(t *testing.T) {
	tr := New(Config{})
	for i := 0; i < 3; i++ {
		tr.Observe(model.Reading{Model: "X", ID: "1", Time: t0.Add(time.Duration(i) * time.Second), Flags: map[string]bool{model.FieldBatteryOK: i != 2}})
	}
	st, _ := tr.Lookup("X:1:", t0)
	if st.BatteryOK == nil || *st.BatteryOK {
		t.Fatalf("battery = %v", st.BatteryOK)
	}
	if len(st.Fields) != 1 || st.Fields[0] != model.FieldBatteryOK {
		t.Fatalf("fields = %v", st.Fields)
	}
}
