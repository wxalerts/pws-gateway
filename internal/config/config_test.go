package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsValidate(t *testing.T) {
	d := Defaults()
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	if d.History.CadenceMinutes < MinHistoryCadenceMinutes {
		t.Fatal("default cadence below minimum")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	created, err := s.Load()
	if err != nil || !created {
		t.Fatalf("Load on empty dir = created %v err %v", created, err)
	}
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Load must not write the file by itself")
	}

	deadline := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	want, err := s.Update(func(c *Config) error {
		c.Radios = append(c.Radios, Radio{Serial: "00000001", Role: RoleRTL433, BandMHz: 433.92})
		c.Stations["Acurite-5n1/1234/A"] = Station{Selected: true, Name: "Roof", AltM: 12.5, WindHeightM: 3}
		c.Uplink.DeviceID = "pwsgw-abc"
		c.Network.Pending = &NetworkSettings{Mode: "static", Address: "192.168.1.9/24", Gateway: "192.168.1.1", DNS: []string{"1.1.1.1"}}
		c.Network.ConfirmDeadline = &deadline
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != filePerm {
		t.Errorf("perm = %o", st.Mode().Perm())
	}

	s2 := NewStore(dir)
	created, err = s2.Load()
	if err != nil || created {
		t.Fatalf("second Load = created %v err %v", created, err)
	}
	got := s2.Get()
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if !bytes.Equal(a, b) {
		t.Fatalf("round trip mismatch:\n%s\n%s", a, b)
	}
	if got.Network.Pending == nil || got.Network.Pending.DNS[0] != "1.1.1.1" || !got.Network.ConfirmDeadline.Equal(deadline) {
		t.Fatalf("network state lost: %+v", got.Network)
	}
}

func TestGetReturnsDeepCopy(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _ = s.Load()
	c := s.Get()
	c.Stations["x"] = Station{Name: "mutated"}
	c.Radios = append(c.Radios, Radio{Serial: "s"})
	if len(s.Get().Stations) != 0 || len(s.Get().Radios) != 0 {
		t.Fatal("Get leaked internal state")
	}
}

func TestUpdateRejectsInvalidAndKeepsOld(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	_, _ = s.Load()
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.Path())

	_, err := s.Update(func(c *Config) error {
		c.History.CadenceMinutes = 1
		c.Updates.Channel = "nightly"
		return nil
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if len(ve.Problems) != 2 {
		t.Fatalf("problems = %v", ve.Problems)
	}
	after, _ := os.ReadFile(s.Path())
	if !bytes.Equal(before, after) {
		t.Fatal("invalid update must not touch the file")
	}
	if s.Get().History.CadenceMinutes != 15 {
		t.Fatal("invalid update must not touch memory")
	}
}

func TestApplySections(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _ = s.Load()

	cfg, err := s.Apply("history", json.RawMessage(`{"cadence_minutes": 30}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.History.CadenceMinutes != 30 {
		t.Fatalf("cadence = %d", cfg.History.CadenceMinutes)
	}

	_, err = s.Apply("history", json.RawMessage(`{"cadence_minutes": 30, "bogus": 1}`))
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("unknown field err = %v", err)
	}

	if _, err := s.Apply("nope", json.RawMessage(`{}`)); !errors.Is(err, ErrUnknownSection) {
		t.Fatalf("unknown section err = %v", err)
	}

	cfg, err = s.Apply("stations", json.RawMessage(`{"k1":{"selected":true,"name":"A","alt_m":1,"wind_height_m":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Stations["k1"].Selected {
		t.Fatal("station not applied")
	}
	// Replacing the section drops stations that are not in the body.
	cfg, err = s.Apply("stations", json.RawMessage(`{}`))
	if err != nil || len(cfg.Stations) != 0 {
		t.Fatalf("stations replace = %v %v", cfg.Stations, err)
	}

	cfg, err = s.Apply("radios", json.RawMessage(`[{"serial":"a","role":"rtl_433","band_mhz":915}]`))
	if err != nil || len(cfg.Radios) != 1 {
		t.Fatalf("radios = %v %v", cfg.Radios, err)
	}
	if _, err := s.Apply("radios", json.RawMessage(`[{"serial":"a","role":"spy","band_mhz":915}]`)); err == nil {
		t.Fatal("bad role must fail")
	}
	cfg, err = s.Apply("radios", json.RawMessage(`[]`))
	if err != nil || cfg.Radios == nil {
		t.Fatalf("empty radios must stay a non-nil slice: %v %v", cfg.Radios, err)
	}

	// Network only accepts a pending candidate.
	cfg, err = s.Apply("network", json.RawMessage(`{"pending":{"mode":"dhcp"}}`))
	if err != nil || cfg.Network.Pending == nil || cfg.Network.Pending.Mode != "dhcp" {
		t.Fatalf("network pending = %+v %v", cfg.Network, err)
	}
	if _, err := s.Apply("network", json.RawMessage(`{"current":{"mode":"static"}}`)); err == nil {
		t.Fatal("writing network.current directly must fail")
	}
	if _, err := s.Apply("network", json.RawMessage(`{"pending":{"mode":"static"}}`)); err == nil {
		t.Fatal("static without address must fail validation")
	}
	deadline := time.Now().Add(time.Minute)
	_, _ = s.Update(func(c *Config) error { c.Network.ConfirmDeadline = &deadline; return nil })
	if _, err := s.Apply("network", json.RawMessage(`{"pending":{"mode":"dhcp"}}`)); !errors.Is(err, ErrCommitInProgress) {
		t.Fatalf("err during commit = %v", err)
	}
}

func TestParseMigratesVersionZero(t *testing.T) {
	// A file written before schema_version existed: only some sections.
	in := `{"history":{"cadence_minutes":20},"updates":{"window":"01:00-02:00","channel":"beta"}}`
	cfg, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %d", cfg.SchemaVersion)
	}
	if cfg.History.CadenceMinutes != 20 || cfg.Updates.Channel != "beta" {
		t.Fatalf("values lost: %+v", cfg)
	}
	if cfg.Web.Listen != ":443" {
		t.Fatal("missing sections must take defaults")
	}

	if _, err := Parse([]byte(`{"schema_version": 99}`)); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("future version err = %v", err)
	}
	if _, err := Parse([]byte(`{not json`)); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := Save(path, Defaults()); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
	bad := Defaults()
	bad.History.CadenceMinutes = 0
	if err := Save(path, bad); err == nil {
		t.Fatal("Save must validate")
	}
}

func TestApplySectionsAtomic(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Load(); err != nil {
		t.Fatal(err)
	}
	got, err := s.ApplySections(map[string]json.RawMessage{
		"history":  json.RawMessage(`{"cadence_minutes": 30}`),
		"stations": json.RawMessage(`{"Acurite-5n1:1:A": {"selected": true, "alt_m": 12}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.History.CadenceMinutes != 30 || !got.Stations["Acurite-5n1:1:A"].Selected {
		t.Fatalf("not applied: %+v", got)
	}

	// One bad section rejects the whole patch and leaves the earlier one out.
	_, err = s.ApplySections(map[string]json.RawMessage{
		"history": json.RawMessage(`{"cadence_minutes": 60}`),
		"updates": json.RawMessage(`{"window": "nope", "channel": "stable"}`),
	})
	if err == nil {
		t.Fatal("invalid section accepted")
	}
	if s.Get().History.CadenceMinutes != 30 {
		t.Fatal("partial patch was applied")
	}
	if _, err := s.ApplySections(map[string]json.RawMessage{"shell": json.RawMessage(`{}`)}); !errors.Is(err, ErrUnknownSection) {
		t.Fatalf("unknown section: %v", err)
	}
}

func TestSeveralStationsMayBeSelected(t *testing.T) {
	c := Defaults()
	c.Stations["Acurite-5n1:1:A"] = Station{Selected: true}
	c.Stations["Acurite-609TXC:224:"] = Station{Selected: true}
	c.Stations["WxAlerts-WxLink:1:"] = Station{Selected: true}
	c.Stations["Neighbour:9:"] = Station{}
	if err := c.Validate(); err != nil {
		t.Fatalf("three selected stations should validate: %v", err)
	}
}
