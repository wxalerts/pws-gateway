package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

// BMP280 station identity. The key is BMP280:i2c1-0x76: so a second sensor
// on 0x77 is a separate station, and the key does not change when the board is
// moved to another Pi.
const (
	// PressureModel is the station model of a BMP280.
	PressureModel = "BMP280"
	// PressureRadio is the source name recorded on its readings.
	PressureRadio = "i2c"

	defaultPressureEvery = 30 * time.Second
	defaultPressureFind  = 5 * time.Minute
)

// PressureSensor is one barometer. Only its pressure is ever read: the
// BMP280 temperature is biased by the Pi's own heat, so the station never
// has a temp_c.
type PressureSensor interface {
	// ID is the station id, for example "i2c1-0x76".
	ID() string
	// ReadHPa returns the station pressure in hectopascals.
	ReadHPa(ctx context.Context) (float64, error)
}

// PressureSource polls attached barometers and feeds the pipeline. It is the
// station source for direct-attached sensors, the counterpart of the rtl_433
// supervisor for radios.
type PressureSource struct {
	// Find lists the sensors currently attached. It is called again every
	// FindEvery while none is found, so a sensor wired in later appears.
	Find func(ctx context.Context) []PressureSensor
	// Sink receives each reading. The app passes Pipeline.IngestReading.
	Sink func(ctx context.Context, r model.Reading, at time.Time)

	Every     time.Duration // poll interval, default 30 s
	FindEvery time.Duration // re-detect interval while none found, default 5 min
	Now       func() time.Time
	Log       *slog.Logger
}

func (s *PressureSource) defaults() {
	if s.Every <= 0 {
		s.Every = defaultPressureEvery
	}
	if s.FindEvery <= 0 {
		s.FindEvery = defaultPressureFind
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
}

// Run polls until ctx ends.
func (s *PressureSource) Run(ctx context.Context) {
	s.defaults()
	var sensors []PressureSensor
	var lastFind time.Time
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		if len(sensors) == 0 && (lastFind.IsZero() || s.Now().Sub(lastFind) >= s.FindEvery) {
			sensors, lastFind = s.Find(ctx), s.Now()
		}
		s.Poll(ctx, sensors)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Poll reads every sensor once and sends the readings to the sink. A sensor
// that fails is skipped and logged; it does not stop the others.
func (s *PressureSource) Poll(ctx context.Context, sensors []PressureSensor) {
	s.defaults()
	for _, sn := range sensors {
		hpa, err := sn.ReadHPa(ctx)
		if err != nil {
			if ctx.Err() == nil {
				s.Log.Warn("pressure sensor read failed", "sensor", sn.ID(), "err", err)
			}
			continue
		}
		at := s.Now()
		s.Sink(ctx, model.Reading{
			Model: PressureModel, ID: sn.ID(), Radio: PressureRadio,
			Fields: map[string]float64{model.FieldPressureHPa: hpa},
		}, at)
	}
}
