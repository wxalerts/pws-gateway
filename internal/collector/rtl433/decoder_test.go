package rtl433

import (
	"errors"
	"testing"
	"time"
)

const acuriteLine = `{"time" : "2024-05-01T12:00:00.123456Z", "protocol" : 40, "model" : "Acurite-5n1", "message_type" : 56, "id" : 2342, "channel" : "A", "sequence_num" : 0, "battery_ok" : 1, "wind_avg_km_h" : 3.218, "temperature_F" : 71.6, "humidity" : 48, "mic" : "CHECKSUM", "mod" : "ASK", "freq" : 433.937, "rssi" : -7.112, "snr" : 22.301, "noise" : -29.413, "unknown_thing" : [1, 2], "nested" : {"a" : 1}}`

func TestDecodeAcuriteLine(t *testing.T) {
	var d Decoder
	msg, err := d.Decode([]byte(acuriteLine))
	if err != nil {
		t.Fatal(err)
	}
	if !msg.HasTime {
		t.Fatal("time not parsed")
	}
	want := time.Date(2024, 5, 1, 12, 0, 0, 123456000, time.UTC)
	if !msg.Time.Equal(want) {
		t.Fatalf("time = %v want %v", msg.Time, want)
	}
	if msg.Model != "Acurite-5n1" || msg.ID != "2342" || msg.Channel != "A" {
		t.Fatalf("identity = %q %q %q", msg.Model, msg.ID, msg.Channel)
	}
	if v, ok := msg.Float("rssi"); !ok || v != -7.112 {
		t.Fatalf("rssi = %v %v", v, ok)
	}
	if v, ok := msg.Float("humidity"); !ok || v != 48 {
		t.Fatalf("humidity = %v %v", v, ok)
	}
	if s, ok := msg.String("mic"); !ok || s != "CHECKSUM" {
		t.Fatalf("mic = %v %v", s, ok)
	}
	if _, ok := msg.Data["time"]; ok {
		t.Fatal("time should not be in Data")
	}
	arr, ok := msg.Data["unknown_thing"].([]any)
	if !ok || len(arr) != 2 || arr[0] != float64(1) {
		t.Fatalf("unknown_thing = %#v", msg.Data["unknown_thing"])
	}
	nested, ok := msg.Data["nested"].(map[string]any)
	if !ok || nested["a"] != float64(1) {
		t.Fatalf("nested = %#v", msg.Data["nested"])
	}
	if v, ok := msg.Float("protocol"); !ok || v != 40 {
		t.Fatalf("protocol = %v %v", v, ok)
	}
}

func TestDecodeIDFormatting(t *testing.T) {
	var d Decoder
	cases := []struct {
		line        string
		id, channel string
	}{
		{`{"model":"X","id":12345678901}`, "12345678901", ""},
		{`{"model":"X","id":"0x1F","channel":2}`, "0x1F", "2"},
		{`{"model":"X","id":7,"channel":"B"}`, "7", "B"},
		{`{"model":"X","id":1.5}`, "1.5", ""},
	}
	for _, c := range cases {
		msg, err := d.Decode([]byte(c.line))
		if err != nil {
			t.Fatalf("%s: %v", c.line, err)
		}
		if msg.ID != c.id || msg.Channel != c.channel {
			t.Errorf("%s: id=%q channel=%q", c.line, msg.ID, msg.Channel)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	var d Decoder
	if _, err := d.Decode([]byte("   \n")); !errors.Is(err, ErrEmptyLine) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := d.Decode([]byte("not json")); !errors.Is(err, ErrNotObject) {
		t.Fatalf("not json: %v", err)
	}
	if _, err := d.Decode([]byte(`{"id": 1}`)); !errors.Is(err, ErrNoModel) {
		t.Fatalf("no model: %v", err)
	}
	if _, err := d.Decode([]byte(`{"model": 5}`)); !errors.Is(err, ErrNoModel) {
		t.Fatalf("numeric model: %v", err)
	}
	if _, err := d.Decode([]byte(`{"model": "X", "id": }`)); err == nil {
		t.Fatal("truncated object should fail")
	}
}

func TestDecodeDefaultLocalTime(t *testing.T) {
	loc := time.FixedZone("test", -5*3600)
	d := Decoder{Location: loc}
	msg, err := d.Decode([]byte(`{"time" : "2024-05-01 07:00:00", "model" : "X", "id" : 1}`))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	if !msg.HasTime || !msg.Time.Equal(want) {
		t.Fatalf("time = %v want %v", msg.Time, want)
	}
}

func TestDecodeBadTimeIsTolerated(t *testing.T) {
	var d Decoder
	msg, err := d.Decode([]byte(`{"time" : "garbage", "model" : "X", "id" : 1}`))
	if err != nil {
		t.Fatal(err)
	}
	if msg.HasTime {
		t.Fatal("garbage time should not parse")
	}
}

func TestParseTime(t *testing.T) {
	utc := time.UTC
	base := time.Date(2024, 5, 1, 12, 0, 0, 0, utc)
	usec := base.Add(123456 * time.Microsecond)
	est := time.FixedZone("EST", -5*3600)
	cases := []struct {
		in   string
		loc  *time.Location
		want time.Time
	}{
		{"2024-05-01T12:00:00.123456Z", nil, usec},
		{"2024-05-01T12:00:00Z", nil, base},
		{"2024-05-01T07:00:00.123456-0500", nil, usec},
		{"2024-05-01T07:00:00-05:00", nil, base},
		{"2024-05-01 12:00:00.123456Z", nil, usec},
		{"2024-05-01 12:00:00", utc, base},
		{"2024-05-01 07:00:00", est, base},
		{"2024-05-01T07:00:00.123456", est, usec},
		{"1714564800", nil, base},
		{"1714564800.123456", nil, usec},
	}
	for _, c := range cases {
		got, err := ParseTime(c.in, c.loc)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
		if got.Location() != time.UTC {
			t.Errorf("%q: not UTC", c.in)
		}
	}
	for _, bad := range []string{"", "yesterday", "2024-05-01", "12:00:00", ".5", "1e9"} {
		if _, err := ParseTime(bad, nil); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestFormatTimeRoundTrip(t *testing.T) {
	want := time.Date(2024, 5, 1, 12, 0, 0, 123456000, time.UTC)
	s := FormatTime(want)
	if s != "2024-05-01T12:00:00.123456Z" {
		t.Fatalf("format = %q", s)
	}
	got, err := ParseTime(s, nil)
	if err != nil || !got.Equal(want) {
		t.Fatalf("round trip = %v %v", got, err)
	}
}
