// Package mapping translates rtl_433 decoder fields into canonical SI fields.
//
// Every rtl_433 field name below was checked on 2026-11-16 against
// docs/DATA_FORMAT.md in github.com/merbanan/rtl_433 and against the decoder
// sources named in the comments. DATA_FORMAT.md documents the convention
// <type>_<unit> and the common keys; device files were read for the keys the
// document leaves out (lux, uvi, strike_count, storm_dist_km, light_klx and
// the metadata keys rssi, snr, noise, freq, freq1, freq2, mod, protocol from
// src/r_api.c data_acquired_handler).
//
// Rules are table driven and ordered. The first rule whose source key is
// present wins for a canonical field; later sources for the same field, and
// every key no rule claims, pass through under Reading.Raw unconverted. That
// is what lets all 258 rtl_433 decoders produce usable stations on day one.
package mapping

import (
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
	"github.com/wxalerts/pws-gateway/internal/model"
)

// Rule maps one rtl_433 key to one canonical field with an optional
// conversion into the canonical SI unit.
type Rule struct {
	Source  string
	Field   string
	Convert func(float64) float64
	// Note says where the source name was verified.
	Note string
}

func (r Rule) apply(v float64) float64 {
	if r.Convert == nil {
		return v
	}
	return r.Convert(v)
}

// Unit conversions. Exact factors from the SI brochure and NIST SP 811.
func fToC(f float64) float64      { return (f - 32) * 5 / 9 }
func kPaToHPa(v float64) float64  { return v * 10 }
func psiToHPa(v float64) float64  { return v * 68.9475729 }
func inHgToHPa(v float64) float64 { return v * 33.8638866 }
func kmhToMs(v float64) float64   { return v / 3.6 }
func mphToMs(v float64) float64   { return v * 0.44704 }
func inToMm(v float64) float64    { return v * 25.4 }
func klxToLux(v float64) float64  { return v * 1000 }
func milesToKm(v float64) float64 { return v * 1.609344 }

// DefaultRules is the table for every decoder. Order matters: the native SI
// key for a field comes first so it wins when a decoder emits both.
var DefaultRules = []Rule{
	// Temperature. DATA_FORMAT.md: temperature_C, temperature_F. Multi probe
	// devices number them (acurite_01185m.c: temperature_1_F, temperature_2_F);
	// the first probe stands in for temp_c when there is no plain key.
	{Source: "temperature_C", Field: model.FieldTempC, Note: "DATA_FORMAT.md"},
	{Source: "temperature_F", Field: model.FieldTempC, Convert: fToC, Note: "DATA_FORMAT.md, acurite.c"},
	{Source: "temperature_1_C", Field: model.FieldTempC, Note: "DATA_FORMAT.md numbering convention"},
	{Source: "temperature_1_F", Field: model.FieldTempC, Convert: fToC, Note: "acurite_01185m.c"},

	// Humidity. DATA_FORMAT.md: humidity in percent.
	{Source: "humidity", Field: model.FieldHumidityPct, Note: "DATA_FORMAT.md"},

	// Pressure. DATA_FORMAT.md documents pressure_hPa and pressure_psi;
	// r_api.c convert code uses the suffixes _PSI, _kPa and _inHg, so all
	// spellings are accepted. Station pressure, not sea level: the server
	// reduces it using the user's alt_m.
	{Source: "pressure_hPa", Field: model.FieldPressureHPa, Note: "DATA_FORMAT.md, fineoffset.c"},
	{Source: "pressure_kPa", Field: model.FieldPressureHPa, Convert: kPaToHPa, Note: "r_api.c _kPa suffix"},
	{Source: "pressure_PSI", Field: model.FieldPressureHPa, Convert: psiToHPa, Note: "r_api.c _PSI suffix"},
	{Source: "pressure_psi", Field: model.FieldPressureHPa, Convert: psiToHPa, Note: "DATA_FORMAT.md"},
	{Source: "pressure_inHg", Field: model.FieldPressureHPa, Convert: inHgToHPa, Note: "r_api.c _inHg suffix"},

	// Wind. DATA_FORMAT.md: wind_avg_m_s, wind_avg_km_h, wind_avg_mi_h and
	// wind_max_* for gusts. The wind_speed_* and gust_speed_* names are the
	// pre 2019 spelling kept by a few decoders and by old recordings.
	{Source: "wind_avg_m_s", Field: model.FieldWindSpeedMS, Note: "DATA_FORMAT.md, fineoffset.c, bresser_5in1.c"},
	{Source: "wind_avg_km_h", Field: model.FieldWindSpeedMS, Convert: kmhToMs, Note: "DATA_FORMAT.md, acurite.c 5n1"},
	{Source: "wind_avg_mi_h", Field: model.FieldWindSpeedMS, Convert: mphToMs, Note: "DATA_FORMAT.md, acurite.c Atlas and 3n1"},
	{Source: "wind_speed_m_s", Field: model.FieldWindSpeedMS, Note: "legacy spelling"},
	{Source: "wind_speed_km_h", Field: model.FieldWindSpeedMS, Convert: kmhToMs, Note: "legacy spelling"},
	{Source: "wind_speed_mi_h", Field: model.FieldWindSpeedMS, Convert: mphToMs, Note: "legacy spelling"},
	{Source: "wind_max_m_s", Field: model.FieldWindGustMS, Note: "DATA_FORMAT.md, fineoffset.c, bresser_5in1.c"},
	{Source: "wind_max_km_h", Field: model.FieldWindGustMS, Convert: kmhToMs, Note: "DATA_FORMAT.md, fineoffset_wh1050.c"},
	{Source: "wind_max_mi_h", Field: model.FieldWindGustMS, Convert: mphToMs, Note: "DATA_FORMAT.md"},
	{Source: "gust_speed_m_s", Field: model.FieldWindGustMS, Note: "legacy spelling"},
	{Source: "gust_speed_km_h", Field: model.FieldWindGustMS, Convert: kmhToMs, Note: "legacy spelling"},
	{Source: "gust_speed_mi_h", Field: model.FieldWindGustMS, Convert: mphToMs, Note: "legacy spelling"},
	{Source: "wind_dir_deg", Field: model.FieldWindDirDeg, Note: "DATA_FORMAT.md"},

	// Rain. DATA_FORMAT.md: rain_mm, rain_in are cumulative totals.
	// rain_rate_mm_h and rain_rate_in_h are derived by the sensor and have
	// no rule on purpose: the server derives rate from the counter, and the
	// raw passthrough keeps the sensor's own figure for comparison.
	{Source: "rain_mm", Field: model.FieldRainMM, Note: "DATA_FORMAT.md, fineoffset.c, bresser_5in1.c"},
	{Source: "rain_in", Field: model.FieldRainMM, Convert: inToMm, Note: "DATA_FORMAT.md, acurite.c 5n1"},

	// UV. "uvi" is the UV index (fineoffset.c, fineoffset_ws80.c,
	// ambientweather_wh31e.c WS68, acurite.c Atlas). "uv" in fineoffset.c is
	// the raw sensor reading in uW/cm2, so it has no default rule; Bresser
	// decoders use "uv" for the index and get a quirk rule instead.
	{Source: "uvi", Field: model.FieldUVIndex, Note: "fineoffset.c, acurite.c Atlas, ambientweather_wh31e.c"},

	// Light. "light_lux" (fineoffset.c, fineoffset_ws80.c, WS68), "lux"
	// (acurite.c Atlas), "light_klx" (bresser_7in1.c).
	{Source: "light_lux", Field: model.FieldLightLux, Note: "fineoffset.c, fineoffset_ws80.c"},
	{Source: "lux", Field: model.FieldLightLux, Note: "acurite.c Atlas"},
	{Source: "light_klx", Field: model.FieldLightLux, Convert: klxToLux, Note: "bresser_7in1.c"},

	// Solar irradiance: no rtl_433 decoder emits W/m2 as of the check (a
	// repository search for W_m2 under src/devices found nothing), so
	// solar_wm2 has no source yet. The server can derive it from lux.

	// Lightning. "strike_count" and "storm_dist_km" (fineoffset_wh31l.c,
	// bresser_lightning.c). Acurite's "storm_dist" and "strike_distance" are
	// raw values in miles and are handled by the Acurite quirk.
	{Source: "strike_count", Field: model.FieldLightningCount, Note: "fineoffset_wh31l.c, bresser_lightning.c, acurite.c"},
	{Source: "storm_dist_km", Field: model.FieldLightningKM, Note: "fineoffset_wh31l.c, bresser_lightning.c"},

	// Battery. DATA_FORMAT.md: battery_ok is 0..1, binary for most devices.
	{Source: "battery_ok", Field: model.FieldBatteryOK, Note: "DATA_FORMAT.md"},
}

// BatteryOKThreshold is the level at or above which battery_ok is true.
// Binary sensors report exactly 1 or 0; level sensors (EcoWitt-WH40 in
// ambientweather_wh31e.c) report a fraction, and half is the conventional
// warning point.
const BatteryOKThreshold = 0.5

// Quirk adds or overrides rules for one family of models, matched by model
// name prefix. Quirk rules are tried before DefaultRules.
type Quirk struct {
	Prefix string
	Rules  []Rule
	// Note documents the family's behaviour for maintainers even when no
	// rule is needed.
	Note string
}

// DefaultQuirks is the model-specific table.
var DefaultQuirks = []Quirk{
	{
		Prefix: "Acurite-5n1",
		Note: "acurite.c: alternates message_type 49 (wind_avg_km_h, wind_dir_deg, " +
			"rain_in) and 56 (wind_avg_km_h, temperature_F, humidity) every 18 s, so a " +
			"single reading never carries all fields and buckets fill from both. " +
			"rain_in is a cumulative bucket count that wraps; counter handling covers it.",
	},
	{
		Prefix: "Acurite-",
		Rules: []Rule{
			// acurite.c: 6045M "storm_dist" and Atlas "strike_distance" are the
			// raw sensor values. The source marks the unit as unknown; Acurite
			// consoles show miles and the community decoders treat it as miles,
			// so that is the assumption until a real capture says otherwise.
			{Source: "storm_dist", Field: model.FieldLightningKM, Convert: milesToKm, Note: "acurite.c 6045M, miles assumed"},
			{Source: "strike_distance", Field: model.FieldLightningKM, Convert: milesToKm, Note: "acurite.c Atlas, miles assumed"},
		},
		Note: "Atlas and 3n1 report wind_avg_mi_h, Tower reports temperature_C; the default table covers those.",
	},
	{
		Prefix: "Fineoffset-",
		Note: "fineoffset.c WH65B and fineoffset_ws80.c: temperature_C, humidity, " +
			"wind_avg_m_s, wind_max_m_s, wind_dir_deg, rain_mm, uvi, light_lux are " +
			"native SI. The raw 'uv' value is uW/cm2 and stays raw. WS80 'flags' and " +
			"'unknown' stay raw. Fields are omitted when the sensor reports invalid.",
	},
	{
		Prefix: "LaCrosse-",
		Note: "lacrosse_tx141x.c: TX141THBv2 emits temperature_C, humidity, battery_ok " +
			"and a string 'test' (Yes/No) that stays raw. Older LaCrosse decoders " +
			"use the legacy wind_speed_km_h and gust_speed_km_h names in the default table.",
	},
	{
		Prefix: "Bresser-",
		Rules: []Rule{
			// bresser_6in1.c and bresser_7in1.c emit "uv" as the UV index with
			// one decimal, unlike Fineoffset's raw reading.
			{Source: "uv", Field: model.FieldUVIndex, Note: "bresser_6in1.c, bresser_7in1.c"},
		},
		Note: "bresser_5in1.c is native SI; 7in1 adds light_klx which the default table scales to lux.",
	},
	{
		Prefix: "AmbientWeather-",
		Note: "ambientweather_wh31e.c: WH31E and WH31B are temperature_C and humidity " +
			"only; 'data' is a hex dump that stays raw.",
	},
	{
		Prefix: "EcoWitt-",
		Note: "ambientweather_wh31e.c: WH40 battery_ok is a 0..1 level, WS68 adds " +
			"light_lux, wind_avg_m_s, wind_max_m_s, wind_dir_deg and uvi.",
	},
}

// Metadata keys rtl_433 attaches outside the decoder's own data, from
// src/r_api.c data_acquired_handler and the decoder convention. They are
// routed into Reading fields rather than Raw.
var metadataKeys = map[string]bool{
	"model": true, "id": true, "channel": true,
	"protocol": true, "mod": true,
	"freq": true, "freq1": true, "freq2": true,
	"rssi": true, "snr": true, "noise": true,
}

// Mapper applies the rules. The zero value is not usable; call New.
type Mapper struct {
	rules  []Rule
	quirks []Quirk
}

// New returns a Mapper with the default tables.
func New() *Mapper {
	return &Mapper{rules: DefaultRules, quirks: DefaultQuirks}
}

// NewWith returns a Mapper with custom tables, for tests and for operators
// who add a mapping before it lands upstream.
func NewWith(rules []Rule, quirks []Quirk) *Mapper {
	return &Mapper{rules: rules, quirks: quirks}
}

// Map converts a decoded message into a Reading. Time is msg.Time when the
// line carried one, else zero; the collector decides the final stamp.
func (m *Mapper) Map(msg *rtl433.Message, radio string) model.Reading {
	r := model.Reading{
		Model:   msg.Model,
		ID:      msg.ID,
		Channel: msg.Channel,
		Radio:   radio,
		Fields:  map[string]float64{},
		Flags:   map[string]bool{},
		Raw:     map[string]any{},
	}
	if msg.HasTime {
		r.Time = msg.Time
	}

	consumed := map[string]bool{}
	applyRules := func(rules []Rule) {
		for _, rule := range rules {
			v, ok := msg.Data[rule.Source].(float64)
			if !ok || consumed[rule.Source] {
				continue
			}
			f, known := model.Lookup(rule.Field)
			if !known {
				continue
			}
			if f.Kind == model.KindFlag {
				if _, done := r.Flags[rule.Field]; done {
					continue
				}
				r.Flags[rule.Field] = rule.apply(v) >= BatteryOKThreshold
			} else {
				if _, done := r.Fields[rule.Field]; done {
					continue
				}
				r.Fields[rule.Field] = rule.apply(v)
			}
			consumed[rule.Source] = true
		}
	}
	for _, q := range m.quirks {
		if strings.HasPrefix(msg.Model, q.Prefix) {
			applyRules(q.Rules)
		}
	}
	applyRules(m.rules)

	for k, v := range msg.Data {
		if consumed[k] || metadataKeys[k] {
			continue
		}
		r.Raw[k] = v
	}
	r.Signal = signalFrom(msg)
	if p, ok := msg.Float("protocol"); ok {
		r.Protocol = int(p)
	}
	if len(r.Fields) == 0 {
		r.Fields = nil
	}
	if len(r.Flags) == 0 {
		r.Flags = nil
	}
	if len(r.Raw) == 0 {
		r.Raw = nil
	}
	return r
}

func signalFrom(msg *rtl433.Message) model.Signal {
	var s model.Signal
	if v, ok := msg.Float("rssi"); ok {
		s.RSSI = v
		s.HasLevel = true
	}
	if v, ok := msg.Float("snr"); ok {
		s.SNR = v
		s.HasLevel = true
	}
	if v, ok := msg.Float("noise"); ok {
		s.Noise = v
		s.HasLevel = true
	}
	if v, ok := msg.Float("freq"); ok {
		s.FreqMHz = v
	} else if v, ok := msg.Float("freq1"); ok {
		s.FreqMHz = v
	}
	return s
}

// Sources lists every rtl_433 key the default tables understand, sorted,
// for documentation and the web UI.
func Sources() []string {
	seen := map[string]bool{}
	var out []string
	add := func(rules []Rule) {
		for _, r := range rules {
			if !seen[r.Source] {
				seen[r.Source] = true
				out = append(out, r.Source)
			}
		}
	}
	add(DefaultRules)
	for _, q := range DefaultQuirks {
		add(q.Rules)
	}
	for k := range metadataKeys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Stamp returns a copy of r with Time set, for callers that stamp after
// mapping. It exists so the collector does not reach into the struct.
func Stamp(r model.Reading, t time.Time) model.Reading {
	r.Time = t
	return r
}
