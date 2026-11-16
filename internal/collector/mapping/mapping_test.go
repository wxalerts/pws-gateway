package mapping

import (
	"math"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/model"
)

func decode(t *testing.T, line string) *rtl433.Message {
	t.Helper()
	msg, err := (&rtl433.Decoder{}).Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestAcurite5n1Type56(t *testing.T) {
	line := `{"time" : "2024-05-01T12:00:00.000000Z", "protocol" : 40, "model" : "Acurite-5n1", "message_type" : 56, "id" : 2342, "channel" : "A", "sequence_num" : 0, "battery_ok" : 1, "wind_avg_km_h" : 3.6, "temperature_F" : 71.6, "humidity" : 48, "mic" : "CHECKSUM", "mod" : "ASK", "freq" : 433.937, "rssi" : -7.1, "snr" : 22.3, "noise" : -29.4}`
	r := New().Map(decode(t, line), "radio0")
	if r.StationKey() != "Acurite-5n1:2342:A" {
		t.Fatalf("key = %q", r.StationKey())
	}
	if !near(r.Fields[model.FieldTempC], 22) {
		t.Fatalf("temp = %v", r.Fields[model.FieldTempC])
	}
	if !near(r.Fields[model.FieldWindSpeedMS], 1) {
		t.Fatalf("wind = %v", r.Fields[model.FieldWindSpeedMS])
	}
	if r.Fields[model.FieldHumidityPct] != 48 {
		t.Fatalf("humidity = %v", r.Fields[model.FieldHumidityPct])
	}
	if ok, present := r.Flags[model.FieldBatteryOK]; !present || !ok {
		t.Fatalf("battery = %v %v", ok, present)
	}
	if r.Protocol != 40 || r.Radio != "radio0" {
		t.Fatalf("protocol/radio = %d %q", r.Protocol, r.Radio)
	}
	if !r.Signal.HasLevel || r.Signal.RSSI != -7.1 || r.Signal.SNR != 22.3 || r.Signal.Noise != -29.4 || r.Signal.FreqMHz != 433.937 {
		t.Fatalf("signal = %+v", r.Signal)
	}
	for _, k := range []string{"message_type", "sequence_num", "mic"} {
		if _, ok := r.Raw[k]; !ok {
			t.Errorf("raw missing %s: %v", k, r.Raw)
		}
	}
	for _, k := range []string{"model", "id", "channel", "rssi", "freq", "mod", "protocol", "temperature_F", "humidity", "battery_ok", "wind_avg_km_h"} {
		if _, ok := r.Raw[k]; ok {
			t.Errorf("raw should not contain %s", k)
		}
	}
	if !r.Time.Equal(time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("time = %v", r.Time)
	}
}

func TestAcurite5n1Type49Rain(t *testing.T) {
	line := `{"model" : "Acurite-5n1", "message_type" : 49, "id" : 2342, "channel" : "A", "battery_ok" : 0, "wind_avg_km_h" : 0, "wind_dir_deg" : 202.5, "rain_in" : 1.25}`
	r := New().Map(decode(t, line), "")
	if !near(r.Fields[model.FieldRainMM], 31.75) {
		t.Fatalf("rain = %v", r.Fields[model.FieldRainMM])
	}
	if r.Fields[model.FieldWindDirDeg] != 202.5 {
		t.Fatalf("dir = %v", r.Fields[model.FieldWindDirDeg])
	}
	if r.Flags[model.FieldBatteryOK] {
		t.Fatal("battery should be low")
	}
	if _, ok := r.Fields[model.FieldTempC]; ok {
		t.Fatal("type 49 has no temperature")
	}
	if r.Time.IsZero() == false {
		t.Fatal("no time in line should give zero Time")
	}
	if r.Signal.HasLevel {
		t.Fatal("no level metadata should leave HasLevel false")
	}
}

func TestFineoffsetWH65B(t *testing.T) {
	line := `{"model" : "Fineoffset-WH65B", "id" : 77, "battery_ok" : 1, "temperature_C" : 18.4, "humidity" : 61, "wind_dir_deg" : 270, "wind_avg_m_s" : 2.1, "wind_max_m_s" : 4.3, "rain_mm" : 123.9, "uv" : 820, "uvi" : 3, "light_lux" : 42000.0, "mic" : "CRC"}`
	r := New().Map(decode(t, line), "")
	if r.StationKey() != "Fineoffset-WH65B:77:" {
		t.Fatalf("key = %q", r.StationKey())
	}
	want := map[string]float64{
		model.FieldTempC: 18.4, model.FieldHumidityPct: 61, model.FieldWindDirDeg: 270,
		model.FieldWindSpeedMS: 2.1, model.FieldWindGustMS: 4.3, model.FieldRainMM: 123.9,
		model.FieldUVIndex: 3, model.FieldLightLux: 42000,
	}
	for k, v := range want {
		if !near(r.Fields[k], v) {
			t.Errorf("%s = %v want %v", k, r.Fields[k], v)
		}
	}
	if len(r.Fields) != len(want) {
		t.Fatalf("fields = %v", r.Fields)
	}
	if r.Raw["uv"] != float64(820) {
		t.Fatalf("raw uv should pass through: %v", r.Raw)
	}
}

func TestBresserUVQuirkAndKlx(t *testing.T) {
	line := `{"model" : "Bresser-7in1", "id" : 1, "uv" : 4.5, "light_klx" : 12.5, "wind_avg_m_s" : 1}`
	r := New().Map(decode(t, line), "")
	if r.Fields[model.FieldUVIndex] != 4.5 {
		t.Fatalf("bresser uv = %v", r.Fields[model.FieldUVIndex])
	}
	if r.Fields[model.FieldLightLux] != 12500 {
		t.Fatalf("klx = %v", r.Fields[model.FieldLightLux])
	}
	if _, ok := r.Raw["uv"]; ok {
		t.Fatal("quirk consumed uv, should not be raw")
	}
}

func TestAcuriteLightningMiles(t *testing.T) {
	line := `{"model" : "Acurite-6045M", "id" : 5, "channel" : "B", "strike_count" : 12, "storm_dist" : 10, "active" : 1}`
	r := New().Map(decode(t, line), "")
	if r.Fields[model.FieldLightningCount] != 12 {
		t.Fatalf("count = %v", r.Fields[model.FieldLightningCount])
	}
	if !near(r.Fields[model.FieldLightningKM], 16.09344) {
		t.Fatalf("km = %v", r.Fields[model.FieldLightningKM])
	}
	wh31l := `{"model" : "Fineoffset-WH31L", "id" : 9, "strike_count" : 3, "storm_dist_km" : 7}`
	r = New().Map(decode(t, wh31l), "")
	if r.Fields[model.FieldLightningKM] != 7 {
		t.Fatalf("storm_dist_km = %v", r.Fields[model.FieldLightningKM])
	}
}

func TestUnitConversions(t *testing.T) {
	cases := []struct {
		line  string
		field string
		want  float64
	}{
		{`{"model":"X","id":1,"pressure_kPa":101.3}`, model.FieldPressureHPa, 1013},
		{`{"model":"X","id":1,"pressure_PSI":14.6959}`, model.FieldPressureHPa, 1013.2466},
		{`{"model":"X","id":1,"pressure_inHg":29.92}`, model.FieldPressureHPa, 1013.2075},
		{`{"model":"X","id":1,"wind_avg_mi_h":10}`, model.FieldWindSpeedMS, 4.4704},
		{`{"model":"X","id":1,"wind_max_km_h":36}`, model.FieldWindGustMS, 10},
		{`{"model":"X","id":1,"wind_speed_km_h":18}`, model.FieldWindSpeedMS, 5},
		{`{"model":"X","id":1,"gust_speed_km_h":72}`, model.FieldWindGustMS, 20},
		{`{"model":"X","id":1,"temperature_1_F":212}`, model.FieldTempC, 100},
		{`{"model":"X","id":1,"lux":500}`, model.FieldLightLux, 500},
	}
	for _, c := range cases {
		r := New().Map(decode(t, c.line), "")
		got, ok := r.Fields[c.field]
		if !ok || math.Abs(got-c.want) > 1e-3 {
			t.Errorf("%s: %s = %v (%v) want %v", c.line, c.field, got, ok, c.want)
		}
	}
}

func TestFirstRuleWinsAndLoserGoesRaw(t *testing.T) {
	line := `{"model":"X","id":1,"temperature_C":20,"temperature_F":212,"temperature_1_C":5}`
	r := New().Map(decode(t, line), "")
	if r.Fields[model.FieldTempC] != 20 {
		t.Fatalf("temp = %v", r.Fields[model.FieldTempC])
	}
	if r.Raw["temperature_F"] != float64(212) || r.Raw["temperature_1_C"] != float64(5) {
		t.Fatalf("losers should pass through raw: %v", r.Raw)
	}
}

func TestRainRateStaysRawAndNonNumericIgnored(t *testing.T) {
	line := `{"model":"X","id":1,"rain_rate_mm_h":2.5,"humidity":"--","test":"Yes"}`
	r := New().Map(decode(t, line), "")
	if _, ok := r.Fields[model.FieldHumidityPct]; ok {
		t.Fatal("string humidity should not map")
	}
	if r.Raw["rain_rate_mm_h"] != 2.5 || r.Raw["humidity"] != "--" || r.Raw["test"] != "Yes" {
		t.Fatalf("raw = %v", r.Raw)
	}
}

func TestBatteryLevelThreshold(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want bool
	}{{1, true}, {0, false}, {0.49, false}, {0.5, true}} {
		line := `{"model":"EcoWitt-WH40","id":1,"battery_ok":` + fmtFloat(c.v) + `}`
		r := New().Map(decode(t, line), "")
		if r.Flags[model.FieldBatteryOK] != c.want {
			t.Errorf("battery %v = %v", c.v, r.Flags[model.FieldBatteryOK])
		}
	}
}

func fmtFloat(v float64) string {
	switch v {
	case 1:
		return "1"
	case 0:
		return "0"
	case 0.49:
		return "0.49"
	default:
		return "0.5"
	}
}

// TestWxLinkNode uses a line the bench Pi decoded from the real screen node
// with the wxalerts fork (protocol 291). The generic rules carry the three
// canonical fields; the air quality and battery fields stay in Raw.
func TestWxLinkNode(t *testing.T) {
	line := `{"time" : "2026-11-16T20:57:48.000000Z", "protocol" : 291, "model" : "WxAlerts-WxLink", "id" : 1, "seq" : 3542, "temperature_C" : 13.470, "humidity" : 56.090, "pressure_hPa" : 1023.680, "iaq" : 195.900, "iaq_accuracy" : 2, "bvoc_ppm" : 143.190, "co2_ppm" : 3753, "battery_mV" : 1716, "mic" : "CRC", "mod" : "ASK", "freq" : 433.980, "rssi" : 0.102, "snr" : 14.502, "noise" : -14.502}`
	r := New().Map(decode(t, line), "radio0")
	if r.StationKey() != "WxAlerts-WxLink:1:" {
		t.Fatalf("key = %q", r.StationKey())
	}
	if !near(r.Fields[model.FieldTempC], 13.47) || !near(r.Fields[model.FieldHumidityPct], 56.09) || !near(r.Fields[model.FieldPressureHPa], 1023.68) {
		t.Fatalf("fields = %v", r.Fields)
	}
	for _, k := range []string{"iaq", "iaq_accuracy", "bvoc_ppm", "co2_ppm", "battery_mV", "seq", "mic"} {
		if _, ok := r.Raw[k]; !ok {
			t.Errorf("raw missing %s: %v", k, r.Raw)
		}
	}
	for _, k := range []string{"temperature_C", "humidity", "pressure_hPa"} {
		if _, ok := r.Raw[k]; ok {
			t.Errorf("raw should not contain %s", k)
		}
	}
	if r.Protocol != 291 {
		t.Fatalf("protocol = %d", r.Protocol)
	}
}

func TestFreq1FallbackAndEmptyMaps(t *testing.T) {
	line := `{"model":"X","id":1,"freq1":915.1,"freq2":915.3,"mod":"FSK"}`
	r := New().Map(decode(t, line), "")
	if r.Signal.FreqMHz != 915.1 {
		t.Fatalf("freq = %v", r.Signal.FreqMHz)
	}
	if r.Fields != nil || r.Flags != nil || r.Raw != nil {
		t.Fatalf("empty maps should be nil: %+v", r)
	}
}

func TestRulesTargetKnownFields(t *testing.T) {
	check := func(rules []Rule) {
		for _, r := range rules {
			if _, ok := model.Lookup(r.Field); !ok {
				t.Errorf("rule %s targets unknown field %s", r.Source, r.Field)
			}
			if r.Note == "" {
				t.Errorf("rule %s has no verification note", r.Source)
			}
		}
	}
	check(DefaultRules)
	for _, q := range DefaultQuirks {
		check(q.Rules)
	}
	src := Sources()
	if len(src) < 30 {
		t.Fatalf("Sources too short: %v", src)
	}
	for i := 1; i < len(src); i++ {
		if src[i] <= src[i-1] {
			t.Fatalf("Sources not sorted or has duplicates at %d: %v", i, src)
		}
	}
}

func TestStamp(t *testing.T) {
	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	r := Stamp(model.Reading{}, ts)
	if !r.Time.Equal(ts) {
		t.Fatal("stamp did not set time")
	}
}
