package collector

import (
	"context"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/model"
)

// TestSelectionStoresOnlySelectedStationsAndFields replays the real capture
// with the 5-in-1 selected for temperature only, the 609TXC unselected and
// the neighbor unselected.
func TestSelectionStoresOnlySelectedStationsAndFields(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Selection = func(st model.Station) (bool, []string) {
			if st.Key == keyAcu5n1 {
				return true, []string{model.FieldTempC}
			}
			return false, nil
		}
	})
	h.replayFile(t, realCapture)

	ctx := context.Background()
	from := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	to := from.Add(48 * time.Hour)

	bs, err := h.store.Rollups(ctx, keyAcu5n1, from, to)
	if err != nil || len(bs) == 0 {
		t.Fatalf("selected station rollups = %d, %v", len(bs), err)
	}
	for _, b := range bs {
		if len(b.Fields) != 1 || b.Fields[model.FieldTempC].N == 0 {
			t.Fatalf("bucket %v fields = %v, want only temp_c", b.Start, b.FieldNames())
		}
	}
	for _, key := range []string{key609, keyNeigh} {
		got, err := h.store.Rollups(ctx, key, from, to)
		if err != nil || len(got) != 0 {
			t.Fatalf("%s: unselected station has %d rollups, %v", key, len(got), err)
		}
		// Still discovered and previewable from memory.
		if len(h.p.Readings(key, from)) == 0 {
			t.Fatalf("%s: no in-memory readings for the preview", key)
		}
	}
	found := false
	for _, k := range stationKeys(h) {
		found = found || k == key609
	}
	if !found {
		t.Fatal("unselected station is no longer listed")
	}
	// The preview keeps every field, not only the selected ones.
	rs := h.p.Readings(keyAcu5n1, from)
	full := false
	for _, r := range rs {
		if _, ok := r.Fields[model.FieldWindDirDeg]; ok {
			full = true
		}
	}
	if !full {
		t.Fatal("recent readings should keep unselected fields for preview")
	}
	if h.p.Status().Unselected == 0 {
		t.Fatal("unselected counter not incremented")
	}
}
