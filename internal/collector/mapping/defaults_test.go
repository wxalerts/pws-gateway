package mapping

import (
	"bufio"
	"os"
	"slices"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
)

const realCapture = "../../../testdata/real/acurite-433-20261116.jsonl"

// seenFromCapture maps the real capture and returns the canonical fields each
// model reported.
func seenFromCapture(t *testing.T) map[string][]string {
	t.Helper()
	f, err := os.Open(realCapture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := New()
	seen := map[string]map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		msg, err := (&rtl433.Decoder{}).Decode(sc.Bytes())
		if err != nil {
			continue
		}
		r := m.Map(msg, "r")
		if seen[r.Model] == nil {
			seen[r.Model] = map[string]bool{}
		}
		for _, n := range r.FieldNames() {
			seen[r.Model][n] = true
		}
	}
	out := map[string][]string{}
	for mod, set := range seen {
		for n := range set {
			out[mod] = append(out[mod], n)
		}
	}
	return out
}

func TestDefaultFieldsRealCapture(t *testing.T) {
	seen := seenFromCapture(t)
	if len(seen["Acurite-609TXC"]) == 0 || len(seen["Acurite-5n1"]) == 0 {
		t.Fatalf("capture lacks expected models: %v", seen)
	}
	// 609TXC reports battery_ok too, but only temperature and humidity are
	// on by default.
	if got := DefaultFields("Acurite-609TXC", seen["Acurite-609TXC"]); !slices.Equal(got, []string{"temp_c", "humidity_pct"}) {
		t.Fatalf("609TXC default = %v (seen %v)", got, seen["Acurite-609TXC"])
	}
	// 5n1 keeps everything it reported.
	got := DefaultFields("Acurite-5n1", seen["Acurite-5n1"])
	if len(got) != len(seen["Acurite-5n1"]) {
		t.Fatalf("5n1 default = %v, seen %v", got, seen["Acurite-5n1"])
	}
}

func TestDefaultFieldsTable(t *testing.T) {
	seen := []string{"battery_ok", "wind_speed_ms", "temp_c", "pressure_station_hpa", "humidity_pct"}
	if got := DefaultFields("WxAlerts-WxLink", seen); !slices.Equal(got, []string{"temp_c", "humidity_pct", "pressure_station_hpa"}) {
		t.Fatalf("wxlink = %v", got)
	}
	if got := DefaultFields("Some-New-Model", seen); len(got) != 5 || got[0] != "temp_c" {
		t.Fatalf("unknown model should get everything seen in registry order: %v", got)
	}
	if got := DefaultFields("Acurite-609TXC", []string{"temp_c"}); !slices.Equal(got, []string{"temp_c"}) {
		t.Fatalf("subset of seen only: %v", got)
	}
	if got := DefaultFields("Acurite-5n1", nil); len(got) != 0 {
		t.Fatalf("nothing seen: %v", got)
	}
}
