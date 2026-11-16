// Package model holds the in-memory types shared by the collector, discovery,
// aggregation and store packages: readings, stations, buckets and the
// registry of canonical fields.
//
// Everything here is SI. Unit conversion happens in the mapping layer when a
// decoder value is translated into a canonical field, and in the UI when a
// value is displayed. Nothing in between converts.
package model

import "math"

// Kind says how a field is aggregated into a bucket.
type Kind int

const (
	// KindGauge is a point sample. Buckets keep avg, min and max.
	KindGauge Kind = iota
	// KindDirection is an angle in degrees. Buckets keep the vector mean so
	// that readings around north do not average to south.
	KindDirection
	// KindCounter is a monotonically increasing total such as a rain bucket
	// tip count. Buckets keep the delta since the previous sample, with a
	// drop treated as a reset.
	KindCounter
	// KindFlag is a boolean state. Buckets keep the last value.
	KindFlag
)

var kindNames = [...]string{"gauge", "direction", "counter", "flag"}

// String returns the wire name of the kind.
func (k Kind) String() string {
	if int(k) < 0 || int(k) >= len(kindNames) {
		return "unknown"
	}
	return kindNames[k]
}

// ParseKind is the inverse of Kind.String.
func ParseKind(s string) (Kind, bool) {
	for i, n := range kindNames {
		if n == s {
			return Kind(i), true
		}
	}
	return 0, false
}

// Field describes one canonical field: its name on the wire, how it is
// aggregated, its SI unit and the range outside which a value is considered
// implausible.
type Field struct {
	Name string
	Kind Kind
	Unit string
	// Min and Max bound plausible values. Values outside are noise, a decoder
	// collision or a broken sensor and never count towards discovery.
	Min, Max float64
}

// Plausible reports whether v is within the field's range. NaN and infinities
// are never plausible.
func (f Field) Plausible(v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	return v >= f.Min && v <= f.Max
}

// Canonical field names, v1. These are the only names that appear in the
// Fields map of a Reading and in rollups. Anything else a decoder emits is
// carried under Raw.
const (
	FieldTempC          = "temp_c"
	FieldHumidityPct    = "humidity_pct"
	FieldPressureHPa    = "pressure_station_hpa"
	FieldWindSpeedMS    = "wind_speed_ms"
	FieldWindGustMS     = "wind_gust_ms"
	FieldWindDirDeg     = "wind_dir_deg"
	FieldRainMM         = "rain_mm"
	FieldUVIndex        = "uv_index"
	FieldLightLux       = "light_lux"
	FieldSolarWM2       = "solar_wm2"
	FieldLightningCount = "lightning_count"
	FieldLightningKM    = "lightning_km"
	FieldBatteryOK      = "battery_ok"
)

// Registry lists every canonical field in a stable order. The order is the
// order fields appear in payloads and tables, so append only.
var Registry = []Field{
	{Name: FieldTempC, Kind: KindGauge, Unit: "C", Min: -60, Max: 70},
	{Name: FieldHumidityPct, Kind: KindGauge, Unit: "%", Min: 0, Max: 100},
	{Name: FieldPressureHPa, Kind: KindGauge, Unit: "hPa", Min: 300, Max: 1100},
	{Name: FieldWindSpeedMS, Kind: KindGauge, Unit: "m/s", Min: 0, Max: 90},
	{Name: FieldWindGustMS, Kind: KindGauge, Unit: "m/s", Min: 0, Max: 90},
	{Name: FieldWindDirDeg, Kind: KindDirection, Unit: "deg", Min: 0, Max: 360},
	{Name: FieldRainMM, Kind: KindCounter, Unit: "mm", Min: 0, Max: math.MaxFloat64},
	{Name: FieldUVIndex, Kind: KindGauge, Unit: "index", Min: 0, Max: 20},
	{Name: FieldLightLux, Kind: KindGauge, Unit: "lux", Min: 0, Max: 200000},
	{Name: FieldSolarWM2, Kind: KindGauge, Unit: "W/m2", Min: 0, Max: 1500},
	{Name: FieldLightningCount, Kind: KindCounter, Unit: "count", Min: 0, Max: math.MaxFloat64},
	{Name: FieldLightningKM, Kind: KindGauge, Unit: "km", Min: 0, Max: 100},
	{Name: FieldBatteryOK, Kind: KindFlag, Unit: "", Min: 0, Max: 1},
}

var registryByName = func() map[string]Field {
	m := make(map[string]Field, len(Registry))
	for _, f := range Registry {
		m[f.Name] = f
	}
	return m
}()

// Lookup returns the registry entry for a canonical field name.
func Lookup(name string) (Field, bool) {
	f, ok := registryByName[name]
	return f, ok
}

// Plausible reports whether v is a plausible value for the named field.
// Unknown names are never plausible, so a typo cannot slip past discovery.
func Plausible(name string, v float64) bool {
	f, ok := registryByName[name]
	if !ok {
		return false
	}
	return f.Plausible(v)
}
