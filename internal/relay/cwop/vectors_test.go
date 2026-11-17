package cwop

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/relay"
)

func vector(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "relay", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestSeaLevelPressureVectors(t *testing.T) {
	var fx struct {
		Tolerance float64 `json:"tolerance_hpa"`
		Cases     []struct {
			Name     string   `json:"name"`
			PHPA     *float64 `json:"p_hpa"`
			TC       *float64 `json:"t_c"`
			AltM     *float64 `json:"alt_m"`
			Expected *float64 `json:"expected_hpa"`
		} `json:"cases"`
	}
	vector(t, "slp.json", &fx)
	if len(fx.Cases) < 5 || fx.Tolerance <= 0 {
		t.Fatal("vector file looks empty")
	}
	for _, c := range fx.Cases {
		var got float64
		ok := false
		if c.PHPA != nil && c.TC != nil && c.AltM != nil {
			got, ok = SeaLevelPressure(*c.PHPA, *c.TC, *c.AltM)
		}
		switch {
		case c.Expected == nil && ok:
			t.Errorf("%s: want absent, got %v", c.Name, got)
		case c.Expected != nil && !ok:
			t.Errorf("%s: want %v, got absent", c.Name, *c.Expected)
		case ok && math.Abs(got-*c.Expected) > fx.Tolerance:
			t.Errorf("%s: got %v want %v", c.Name, got, *c.Expected)
		}
	}
}

func TestWind10mVectors(t *testing.T) {
	var fx struct {
		Tolerance float64 `json:"tolerance_ms"`
		Cases     []struct {
			Name     string   `json:"name"`
			Speed    float64  `json:"speed_ms"`
			Height   *float64 `json:"height_m"`
			Expected *float64 `json:"expected_ms"`
		} `json:"cases"`
	}
	vector(t, "wind10m.json", &fx)
	if len(fx.Cases) < 5 {
		t.Fatal("vector file looks empty")
	}
	for _, c := range fx.Cases {
		var got float64
		ok := false
		if c.Height != nil {
			got, ok = WindAt10m(c.Speed, *c.Height)
		}
		switch {
		case c.Expected == nil && ok:
			t.Errorf("%s: want absent, got %v", c.Name, got)
		case c.Expected != nil && !ok:
			t.Errorf("%s: want %v, got absent", c.Name, *c.Expected)
		case ok && math.Abs(got-*c.Expected) > fx.Tolerance:
			t.Errorf("%s: got %v want %v", c.Name, got, *c.Expected)
		}
	}
}

type packetFixture struct {
	SoftwareTag string `json:"software_tag"`
	LoginRaw    struct {
		ID       string `json:"id"`
		Software string `json:"software"`
		Version  string `json:"version"`
		Line     string `json:"expected_line"`
	} `json:"login"`
	Cases []struct {
		Name   string          `json:"name"`
		Obs    json.RawMessage `json:"obs"`
		Config struct {
			ID  string  `json:"id"`
			Lat float64 `json:"lat"`
			Lon float64 `json:"lon"`
		} `json:"config"`
		Rain     *relay.RainTotals `json:"rain"`
		Expected string            `json:"expected_packet"`
	} `json:"cases"`
	Errors []struct {
		Name     string `json:"name"`
		ObsN     *int   `json:"obs_n"`
		OptIn    *bool  `json:"position_opt_in"`
		ID       string `json:"id"`
		Expected string `json:"expected_error"`
	} `json:"errors"`
}

func observation(t *testing.T, raw json.RawMessage, rain *relay.RainTotals) relay.Observation {
	t.Helper()
	var obs contract.Obs
	if err := json.Unmarshal(raw, &obs); err != nil {
		t.Fatal(err)
	}
	b := obs.Buckets[0]
	return relay.Observation{Start: b.Start, IntervalS: obs.IntervalS, Station: b.Stations[0], Rain: rain}
}

func TestPacketVectors(t *testing.T) {
	var fx packetFixture
	vector(t, "packet.json", &fx)
	if fx.SoftwareTag != SoftwareTag {
		t.Fatalf("software tag %q, fixture %q", SoftwareTag, fx.SoftwareTag)
	}
	if len(fx.Cases) < 4 {
		t.Fatal("vector file looks empty")
	}
	for _, c := range fx.Cases {
		obs := observation(t, c.Obs, c.Rain)
		got, err := FormatPacket(c.Config.ID, c.Config.Lat, c.Config.Lon, obs)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if got != c.Expected {
			t.Errorf("%s:\n got  %s\n want %s", c.Name, got, c.Expected)
		}
	}
}

func TestLoginVector(t *testing.T) {
	var fx packetFixture
	vector(t, "packet.json", &fx)
	p := New(PluginOptions{Config: Config{ID: fx.LoginRaw.ID}, Version: fx.LoginRaw.Version})
	if got := p.Login(); got != fx.LoginRaw.Line {
		t.Errorf("login %q, want %q", got, fx.LoginRaw.Line)
	}
}

func TestPacketErrorVectors(t *testing.T) {
	var fx packetFixture
	vector(t, "packet.json", &fx)
	base := fx.Cases[0]
	for _, e := range fx.Errors {
		cfg := Config{Owner: "device", Station: "x", ID: base.Config.ID, PositionOptIn: true, Lat: base.Config.Lat, Lon: base.Config.Lon}
		obs := observation(t, base.Obs, base.Rain)
		if e.ObsN != nil {
			obs.Station.N = *e.ObsN
		}
		if e.OptIn != nil {
			cfg.PositionOptIn = *e.OptIn
		}
		if e.ID != "" {
			cfg.ID = e.ID
		}
		_, err := New(PluginOptions{Config: cfg}).Map(obs)
		if err == nil || relay.CodeOf(err) != e.Expected {
			t.Errorf("%s: got %v, want code %s", e.Name, err, e.Expected)
		}
	}
}

func TestAcceptanceValues(t *testing.T) {
	if got, ok := SeaLevelPressure(1008.3, 18.2, 31); !ok || math.Abs(got-1012.0) > 0.1 {
		t.Errorf("slp %v %v", got, ok)
	}
	if got, ok := WindAt10m(2.1, 4.5); !ok || math.Abs(got-2.43) > 0.01 {
		t.Errorf("wind %v %v", got, ok)
	}
}
