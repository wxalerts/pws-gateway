package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

type fakeBaro struct {
	id  string
	hpa float64
	err error
}

func (f fakeBaro) ID() string { return f.id }
func (f fakeBaro) ReadHPa(context.Context) (float64, error) {
	return f.hpa, f.err
}

func TestPressureSourceReportsPressureOnly(t *testing.T) {
	var got []model.Reading
	s := &PressureSource{Sink: func(_ context.Context, r model.Reading, _ time.Time) { got = append(got, r) }, Now: time.Now}
	s.Poll(context.Background(), []PressureSensor{fakeBaro{id: "i2c1-0x76", hpa: 1006.5}, fakeBaro{id: "bad", err: errors.New("io")}})
	if len(got) != 1 {
		t.Fatalf("readings = %d", len(got))
	}
	r := got[0]
	if r.StationKey() != "BMP280:i2c1-0x76:" {
		t.Errorf("key = %q", r.StationKey())
	}
	if len(r.Fields) != 1 || r.Fields[model.FieldPressureHPa] != 1006.5 {
		t.Errorf("fields = %v", r.Fields)
	}
	if _, has := r.Fields[model.FieldTempC]; has {
		t.Error("a BMP280 station must not carry temp_c")
	}
}

// A BMP280 is discovered as its own station by the pipeline and exposes
// pressure_station_hpa and nothing else.
func TestBMP280BecomesAStationThroughThePipeline(t *testing.T) {
	now := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)
	p := New(Config{Now: func() time.Time { return now }, TickEvery: -1})
	src := &PressureSource{Sink: p.IngestReading, Now: func() time.Time { return now }}
	for i := 0; i < 6; i++ {
		now = now.Add(30 * time.Second)
		src.Poll(context.Background(), []PressureSensor{fakeBaro{id: "i2c1-0x76", hpa: 1006.5 + float64(i)*0.01}})
	}
	sts := p.Stations(now)
	if len(sts) != 1 || sts[0].Key != "BMP280:i2c1-0x76:" {
		t.Fatalf("stations = %+v", sts)
	}
	if len(sts[0].Fields) != 1 || sts[0].Fields[0] != model.FieldPressureHPa {
		t.Errorf("fields = %v", sts[0].Fields)
	}
	rs := p.Readings(sts[0].Key, time.Time{})
	if len(rs) == 0 {
		t.Fatal("no readings")
	}
	for _, r := range rs {
		if _, has := r.Fields[model.FieldTempC]; has {
			t.Fatal("temp_c produced")
		}
	}
}

func TestPressureSourceFindsLateSensor(t *testing.T) {
	var n int
	var found bool
	s := &PressureSource{
		Every: 10 * time.Millisecond, FindEvery: time.Millisecond,
		Find: func(context.Context) []PressureSensor {
			n++
			if n < 3 {
				return nil
			}
			return []PressureSensor{fakeBaro{id: "i2c1-0x77", hpa: 1000}}
		},
		Sink: func(context.Context, model.Reading, time.Time) { found = true },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	s.Run(ctx)
	if !found || n < 3 {
		t.Fatalf("found=%v finds=%d", found, n)
	}
}
