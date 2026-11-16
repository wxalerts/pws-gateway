package contract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
)

func TestValidateRejectsOversize(t *testing.T) {
	big := bytes.Repeat([]byte(" "), contract.MaxMessageBytes+1)
	err := contract.Validate(contract.KindObs, big)
	if !errors.Is(err, contract.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestValidateRejectsInvalidJSON(t *testing.T) {
	err := contract.Validate(contract.KindTel, []byte(`{"v": 0,`))
	if !errors.Is(err, contract.ErrInvalidJSON) {
		t.Fatalf("want ErrInvalidJSON, got %v", err)
	}
}

func TestValidateUnknownKind(t *testing.T) {
	err := contract.Validate(contract.Kind("nope"), []byte(`{}`))
	if !errors.Is(err, contract.ErrUnknownKind) {
		t.Fatalf("want ErrUnknownKind, got %v", err)
	}
}

func TestMarshalRejectsLocalTimestamp(t *testing.T) {
	loc := time.FixedZone("plus1", 3600)
	ev := &contract.Event{V: contract.Version, TS: time.Date(2026, 11, 16, 15, 0, 0, 0, loc), Type: contract.EventSDRMissing}
	_, err := contract.Marshal(ev)
	var se *contract.SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("a timestamp with an offset must fail schema validation, got %v", err)
	}
	ev.TS = ev.TS.UTC()
	if _, err := contract.Marshal(ev); err != nil {
		t.Fatalf("UTC timestamp should pass: %v", err)
	}
}

func TestGoTypesProduceValidPayloads(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	n := func(v int) *int { return &v }
	ts := time.Date(2026, 11, 16, 14, 15, 3, 0, time.UTC)

	msgs := []contract.Message{
		&contract.Obs{
			V: contract.Version, DeviceID: "d_8f3a", Seq: 1, Sent: ts, IntervalS: 300,
			Buckets: []contract.Bucket{{
				Start: ts.Truncate(15 * time.Minute),
				Stations: []contract.Station{{
					Key: "Acurite-5n1:1234:A", N: 7,
					Meta: &contract.StationMeta{AltM: f(31), WindHeightM: f(4.5)},
					F: contract.Fields{
						TempC:      &contract.Gauge{Avg: f(18.2), Min: f(17.9), Max: f(18.6)},
						WindDirDeg: &contract.Direction{Vec: 212},
						RainMM:     &contract.Counter{Sum: 0},
						BatteryOK:  &contract.Flag{Last: 1},
					},
					Raw: json.RawMessage(`{"mic":"CRC"}`),
				}},
			}},
		},
		&contract.Tel{
			V: contract.Version, DeviceID: "d_8f3a", TS: ts, FW: "0.1.0", UptimeS: 10,
			Time:   contract.TimeSync{Src: "ntp", OffsetMS: 3, Synced: true},
			Sys:    contract.Sys{CPUTempC: f(52.1), Throttled: n(0)},
			Outbox: contract.Outbox{},
			SDR:    []contract.SDR{{RadioID: "1", Driver: contract.DriverRTL433, BandMHz: f(433.92)}},
			Net:    contract.Net{Link: "lte"},
		},
		&contract.Update{V: n(contract.Version), Current: "0.1.0", State: contract.UpdateIdle, TS: ts},
		&contract.Event{V: contract.Version, TS: ts, Type: contract.EventSensorLost, Detail: json.RawMessage(`{"key":"Acurite-5n1:1234:A"}`)},
		&contract.Ack{V: contract.Version, ID: "c_01", Type: contract.CmdReboot, OK: true, TS: ts},
	}
	for _, m := range msgs {
		if _, err := contract.Marshal(m); err != nil {
			t.Errorf("%s: %v", m.Kind(), err)
		}
	}
	st := contract.StatusOnline
	if err := st.Validate(); err != nil {
		t.Errorf("status: %v", err)
	}
	bad := contract.Status("connected")
	if err := bad.Validate(); err == nil {
		t.Error("status connected should fail")
	}
}

func TestGoTypesCatchContractMistakes(t *testing.T) {
	ts := time.Date(2026, 11, 16, 14, 15, 3, 0, time.UTC)
	cases := map[string]contract.Message{
		"nil buckets marshal as null": &contract.Obs{V: 0, DeviceID: "d", Seq: 1, Sent: ts, IntervalS: 300},
		"gauge without statistics": &contract.Obs{V: 0, DeviceID: "d", Seq: 1, Sent: ts, IntervalS: 300,
			Buckets: []contract.Bucket{{Start: ts, Stations: []contract.Station{{Key: "a:1:A", F: contract.Fields{TempC: &contract.Gauge{}}}}}}},
		"unknown update state": &contract.Update{Current: "0.1.0", State: "installing", TS: ts},
		"unknown event type":   &contract.Event{V: 0, TS: ts, Type: "boom"},
		"unknown diag action":  &contract.Command{ID: "c", Type: contract.CmdDiag, DeviceID: "d", Exp: ts, Body: json.RawMessage(`{"action":"rm"}`), Sig: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="},
	}
	for name, m := range cases {
		if _, err := contract.Marshal(m); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestAcceptsVersion(t *testing.T) {
	if !contract.AcceptsVersion(contract.Version) {
		t.Error("current version must be accepted")
	}
	if contract.AcceptsVersion(contract.Version + 1) {
		t.Error("a future version must not be accepted")
	}
	if contract.AcceptsVersion(-1) {
		t.Error("negative versions must not be accepted")
	}
}

func TestUnmarshalIgnoresUnknownFields(t *testing.T) {
	var ev contract.Event
	err := contract.Unmarshal([]byte(`{"v":0,"ts":"2026-11-16T14:15:03Z","type":"rollback","x_new":{"a":1}}`), &ev)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != contract.EventRollback {
		t.Fatalf("type %q", ev.Type)
	}
}
