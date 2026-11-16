package model

import (
	"math"
	"testing"
)

func TestRegistryHasUniqueNamesAndLookup(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Registry {
		if seen[f.Name] {
			t.Fatalf("duplicate field %q", f.Name)
		}
		seen[f.Name] = true
		got, ok := Lookup(f.Name)
		if !ok || got.Name != f.Name {
			t.Fatalf("Lookup(%q) = %v, %v", f.Name, got, ok)
		}
		if f.Min > f.Max {
			t.Fatalf("%s has min > max", f.Name)
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown field found")
	}
}

func TestPlausible(t *testing.T) {
	cases := []struct {
		field string
		v     float64
		want  bool
	}{
		{FieldTempC, 21.5, true},
		{FieldTempC, -60, true},
		{FieldTempC, 70.1, false},
		{FieldTempC, math.NaN(), false},
		{FieldTempC, math.Inf(1), false},
		{FieldHumidityPct, 100, true},
		{FieldHumidityPct, 101, false},
		{FieldPressureHPa, 1013.25, true},
		{FieldPressureHPa, 299, false},
		{FieldWindSpeedMS, 90, true},
		{FieldWindSpeedMS, -0.1, false},
		{FieldRainMM, 0, true},
		{FieldRainMM, 1e9, true},
		{FieldRainMM, -1, false},
		{FieldWindDirDeg, 360, true},
		{FieldWindDirDeg, 361, false},
		{"unknown", 1, false},
	}
	for _, c := range cases {
		if got := Plausible(c.field, c.v); got != c.want {
			t.Errorf("Plausible(%s, %v) = %v, want %v", c.field, c.v, got, c.want)
		}
	}
}

func TestKindRoundTrip(t *testing.T) {
	for _, k := range []Kind{KindGauge, KindDirection, KindCounter, KindFlag} {
		got, ok := ParseKind(k.String())
		if !ok || got != k {
			t.Fatalf("ParseKind(%q) = %v, %v", k.String(), got, ok)
		}
	}
	if Kind(99).String() != "unknown" {
		t.Fatal("out of range kind should be unknown")
	}
	if _, ok := ParseKind("bogus"); ok {
		t.Fatal("bogus kind parsed")
	}
}

func TestStationKey(t *testing.T) {
	key := StationKey("Acurite-5n1", "2342", "A")
	if key != "Acurite-5n1:2342:A" {
		t.Fatalf("key = %q", key)
	}
	m, id, ch, ok := SplitStationKey(key)
	if !ok || m != "Acurite-5n1" || id != "2342" || ch != "A" {
		t.Fatalf("split = %q %q %q %v", m, id, ch, ok)
	}
	empty := StationKey("Fineoffset-WH65B", "77", "")
	if empty != "Fineoffset-WH65B:77:" {
		t.Fatalf("empty channel key = %q", empty)
	}
	m, id, ch, ok = SplitStationKey(empty)
	if !ok || m != "Fineoffset-WH65B" || id != "77" || ch != "" {
		t.Fatalf("split = %q %q %q %v", m, id, ch, ok)
	}
	if _, _, _, ok := SplitStationKey("no-colons"); ok {
		t.Fatal("bad key parsed")
	}
}

func TestReadingFieldNamesSorted(t *testing.T) {
	r := Reading{
		Fields: map[string]float64{FieldTempC: 1, FieldHumidityPct: 2},
		Flags:  map[string]bool{FieldBatteryOK: true},
	}
	got := r.FieldNames()
	want := []string{FieldBatteryOK, FieldHumidityPct, FieldTempC}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
