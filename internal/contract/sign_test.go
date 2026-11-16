package contract_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"path/filepath"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/contract/conformance"
)

// update rewrites the committed signing vectors, the JCS vectors and the
// valid cmd examples from the inputs in this file:
//
//	go test ./internal/contract -run 'TestSigningVectors|TestCanonicalizeVectorsFile' -update
var update = flag.Bool("update", false, "rewrite schemas/examples/signing and the valid cmd examples")

// testSeedHex is the published test key seed. It appears in the vectors file
// and in every implementation's tests; it must never sign a real command.
const testSeedHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// verifyAt is the clock value at which every vector command is unexpired.
var verifyAt = time.Date(2026, 11, 16, 14, 30, 0, 0, time.UTC)

// vectorInputs are the unsigned commands behind the vectors file and the
// valid cmd examples. Key order and number spelling are deliberately not
// canonical so an implementation has to apply RFC 8785 to match.
var vectorInputs = []conformance.SigningInput{
	{Name: "config", Command: json.RawMessage(`{"type":"config","id":"c_01","device_id":"d_8f3a","exp":"2026-11-16T15:00:00Z","body":{"uplink":{"interval_s":300,"jitter_s":45.0},"stations":[{"selected":true,"key":"Acurite-5n1:1234:A"}],"note":"caf\u00e9 \"quoted\" \\ slash/ 1E2"}}`)},
	{Name: "reboot", Command: json.RawMessage(`{"id":"c_02","type":"reboot","device_id":"d_8f3a","exp":"2026-11-16T15:00:00Z","body":{}}`)},
	{Name: "diag-restart-service", Command: json.RawMessage(`{"id":"c_03","type":"diag","device_id":"d_8f3a","exp":"2026-11-16T15:00:00Z","body":{"service":"rtl_433","action":"restart_service"}}`)},
	{Name: "resync", Command: json.RawMessage(`{"id":"c_04","type":"resync","device_id":"d_8f3a","exp":"2026-11-16T15:00:00Z","body":{"to":"2026-11-16T14:00:00Z","from":"2026-11-16T12:00:00Z"}}`)},
	{Name: "update-check", Command: json.RawMessage(`{"id":"c_05","type":"update-check","device_id":"pi-garden-01","exp":"2026-11-16T15:05:00Z","body":{}}`)},
}

func TestSigningVectors(t *testing.T) {
	path := filepath.Join(examplesRoot, "signing", "ed25519.json")
	if *update {
		seed, err := hex.DecodeString(testSeedHex)
		if err != nil {
			t.Fatal(err)
		}
		f, err := conformance.GenerateSigningVectors(seed, verifyAt, vectorInputs)
		if err != nil {
			t.Fatal(err)
		}
		if err := conformance.WriteJSON(path, f); err != nil {
			t.Fatal(err)
		}
		for _, v := range f.Vectors {
			signed, err := conformance.SignedCommandJSON(v.Command, v.Sig)
			if err != nil {
				t.Fatal(err)
			}
			var cmd contract.Command
			if err := json.Unmarshal(signed, &cmd); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(examplesRoot, "valid", "cmd-"+v.Name+".json")
			if err := conformance.WriteJSON(out, &cmd); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("rewrote %s and the valid cmd examples", path)
	}

	f, err := conformance.LoadSigningVectors(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.SeedHex != testSeedHex {
		t.Fatalf("vectors file seed changed: %s", f.SeedHex)
	}
	if len(f.Vectors) != len(vectorInputs) {
		t.Fatalf("vectors file has %d vectors, this test defines %d; run with -update", len(f.Vectors), len(vectorInputs))
	}
	for i, v := range f.Vectors {
		// The file is re-indented on write, so compare canonical forms
		// rather than bytes.
		want, err := contract.Canonicalize(vectorInputs[i].Command)
		if err != nil {
			t.Fatal(err)
		}
		got, err := contract.Canonicalize(v.Command)
		if err != nil {
			t.Fatal(err)
		}
		if v.Name != vectorInputs[i].Name || string(want) != string(got) {
			t.Errorf("vector %d (%s) differs from the input in this test; run with -update", i, v.Name)
		}
	}
	for _, err := range f.Check() {
		t.Error(err)
	}
}

func testKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	seed, err := hex.DecodeString(testSeedHex)
	if err != nil {
		t.Fatal(err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(ed25519.PublicKey), priv
}

func newTestCommand() *contract.Command {
	return &contract.Command{
		ID:       "c_42",
		Type:     contract.CmdDiag,
		DeviceID: "d_8f3a",
		Exp:      verifyAt.Add(30 * time.Minute),
		Body:     json.RawMessage(`{"action":"connectivity_test"}`),
	}
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	pub, priv := testKeys(t)
	cmd := newTestCommand()
	if err := contract.SignCommand(priv, cmd); err != nil {
		t.Fatal(err)
	}
	wire, err := contract.Marshal(cmd)
	if err != nil {
		t.Fatalf("signed command does not validate: %v", err)
	}
	parsed, err := contract.ParseCommand(wire)
	if err != nil {
		t.Fatal(err)
	}
	cache := contract.NewMemoryReplayCache(func() time.Time { return verifyAt })
	opts := contract.VerifyOptions{DeviceID: "d_8f3a", Now: func() time.Time { return verifyAt }, Replay: cache}
	if err := contract.VerifyCommand(pub, parsed, opts); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := contract.VerifyCommand(pub, parsed, opts); !errors.Is(err, contract.ErrReplay) {
		t.Fatalf("second delivery should be a replay, got %v", err)
	}
	// A command received with different whitespace and key order must still
	// verify, because the signature covers the canonical form.
	reordered := []byte(`{ "sig": "` + cmd.Sig + `", "body": {"action": "connectivity_test"}, "exp": "` + cmd.Exp.Format(time.RFC3339) + `",
	  "device_id": "d_8f3a", "type": "diag", "id": "c_42" }`)
	parsed2, err := contract.ParseCommand(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.VerifyCommand(pub, parsed2, contract.VerifyOptions{DeviceID: "d_8f3a", Now: opts.Now}); err != nil {
		t.Fatalf("reordered command should verify: %v", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	pub, priv := testKeys(t)
	now := func() time.Time { return verifyAt }

	t.Run("expired", func(t *testing.T) {
		cmd := newTestCommand()
		cmd.Exp = verifyAt
		if err := contract.SignCommand(priv, cmd); err != nil {
			t.Fatal(err)
		}
		err := contract.VerifyCommand(pub, cmd, contract.VerifyOptions{DeviceID: "d_8f3a", Now: now})
		if !errors.Is(err, contract.ErrExpired) {
			t.Fatalf("want ErrExpired, got %v", err)
		}
	})
	t.Run("device mismatch", func(t *testing.T) {
		cmd := newTestCommand()
		if err := contract.SignCommand(priv, cmd); err != nil {
			t.Fatal(err)
		}
		err := contract.VerifyCommand(pub, cmd, contract.VerifyOptions{DeviceID: "other", Now: now})
		if !errors.Is(err, contract.ErrDeviceMismatch) {
			t.Fatalf("want ErrDeviceMismatch, got %v", err)
		}
	})
	t.Run("tampered body", func(t *testing.T) {
		cmd := newTestCommand()
		if err := contract.SignCommand(priv, cmd); err != nil {
			t.Fatal(err)
		}
		cmd.Body = json.RawMessage(`{"action":"clear_outbox"}`)
		err := contract.VerifyCommand(pub, cmd, contract.VerifyOptions{DeviceID: "d_8f3a", Now: now})
		if !errors.Is(err, contract.ErrBadSignature) {
			t.Fatalf("want ErrBadSignature, got %v", err)
		}
	})
	t.Run("wrong key", func(t *testing.T) {
		cmd := newTestCommand()
		if err := contract.SignCommand(priv, cmd); err != nil {
			t.Fatal(err)
		}
		otherPub, _, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		err = contract.VerifyCommand(otherPub, cmd, contract.VerifyOptions{DeviceID: "d_8f3a", Now: now})
		if !errors.Is(err, contract.ErrBadSignature) {
			t.Fatalf("want ErrBadSignature, got %v", err)
		}
	})
	t.Run("garbage signature", func(t *testing.T) {
		cmd := newTestCommand()
		cmd.Sig = "not base64!"
		err := contract.VerifyCommand(pub, cmd, contract.VerifyOptions{DeviceID: "d_8f3a", Now: now})
		if !errors.Is(err, contract.ErrBadSignature) {
			t.Fatalf("want ErrBadSignature, got %v", err)
		}
	})
	t.Run("bad key size", func(t *testing.T) {
		cmd := newTestCommand()
		if err := contract.SignCommand(priv[:10], cmd); !errors.Is(err, contract.ErrBadKey) {
			t.Fatalf("want ErrBadKey, got %v", err)
		}
		if err := contract.VerifyCommand(pub[:10], cmd, contract.VerifyOptions{Now: now}); !errors.Is(err, contract.ErrBadKey) {
			t.Fatalf("want ErrBadKey, got %v", err)
		}
	})
	t.Run("replay cache not consulted before signature", func(t *testing.T) {
		cmd := newTestCommand()
		cmd.Sig = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
		cache := contract.NewMemoryReplayCache(now)
		err := contract.VerifyCommand(pub, cmd, contract.VerifyOptions{DeviceID: "d_8f3a", Now: now, Replay: cache})
		if !errors.Is(err, contract.ErrBadSignature) {
			t.Fatalf("want ErrBadSignature, got %v", err)
		}
		if cache.Len() != 0 {
			t.Fatal("an unsigned command must not enter the replay cache")
		}
	})
}

func TestMemoryReplayCacheEvicts(t *testing.T) {
	clock := verifyAt
	cache := contract.NewMemoryReplayCache(func() time.Time { return clock })
	exp := verifyAt.Add(time.Minute)
	if seen, _ := cache.Seen("a", exp); seen {
		t.Fatal("first sighting reported as seen")
	}
	if seen, _ := cache.Seen("a", exp); !seen {
		t.Fatal("second sighting not reported as seen")
	}
	clock = exp
	if seen, _ := cache.Seen("b", exp.Add(time.Hour)); seen {
		t.Fatal("new id reported as seen")
	}
	if cache.Len() != 1 {
		t.Fatalf("expired id was not evicted, len %d", cache.Len())
	}
}

func TestSigningInputEmptyBody(t *testing.T) {
	_, priv := testKeys(t)
	cmd := newTestCommand()
	cmd.Body = nil
	if err := contract.SignCommand(priv, cmd); err != nil {
		t.Fatal(err)
	}
	if string(cmd.Body) != "{}" {
		t.Fatalf("SignCommand must normalise an empty body to {}, got %q", cmd.Body)
	}
	input, err := contract.SigningInput(cmd)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"body":{},"device_id":"d_8f3a","exp":"2026-11-16T15:00:00Z","id":"c_42","type":"diag"}`
	if string(input) != want {
		t.Fatalf("signing input\n want %s\n got  %s", want, input)
	}
}
