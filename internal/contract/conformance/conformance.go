// Package conformance loads the test vectors under schemas/examples and
// checks them against the contract package. It is shared by the Go tests and
// by tools/schemacheck so that both apply the same rules to the same files.
package conformance

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
)

// Example is one file under schemas/examples/valid or schemas/examples/invalid.
type Example struct {
	// Path is the file path as given to LoadExamples.
	Path string
	// Name is the file name without the .json suffix.
	Name string
	// Kind is derived from the file name prefix up to the first hyphen.
	Kind contract.Kind
	// Valid is true for files under valid/.
	Valid bool
	// Data is the file content.
	Data []byte
	// Reason is the content of the sibling .reason.txt for invalid examples.
	Reason string
}

// LoadExamples reads every example under root/valid and root/invalid. An
// invalid example without a sibling .reason.txt is an error, as is a file
// whose prefix is not a message kind.
func LoadExamples(root string) ([]Example, error) {
	var out []Example
	for _, sub := range []string{"valid", "invalid"} {
		dir := filepath.Join(root, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") {
				continue
			}
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			base := strings.TrimSuffix(name, ".json")
			prefix, _, _ := strings.Cut(base, "-")
			kind := contract.Kind(prefix)
			if contract.New(kind) == nil {
				return nil, fmt.Errorf("%s: prefix %q is not a message kind; name files {kind}-{what}.json", path, prefix)
			}
			ex := Example{Path: path, Name: base, Kind: kind, Valid: sub == "valid", Data: data}
			if !ex.Valid {
				reason, err := os.ReadFile(filepath.Join(dir, base+".reason.txt"))
				if err != nil {
					return nil, fmt.Errorf("%s: every invalid example needs a sibling %s.reason.txt: %w", path, base, err)
				}
				ex.Reason = strings.TrimSpace(string(reason))
				if ex.Reason == "" {
					return nil, fmt.Errorf("%s: reason file is empty", path)
				}
			}
			out = append(out, ex)
		}
	}
	return out, nil
}

// RoundTrip decodes an example into its Go type and encodes it again through
// contract.Marshal, which also validates the result against the schema.
func RoundTrip(ex Example) ([]byte, error) {
	m := contract.New(ex.Kind)
	if err := contract.Unmarshal(ex.Data, m); err != nil {
		return nil, err
	}
	return contract.Marshal(m)
}

// Diff compares the JSON document got (the Go round trip) against want (the
// example file) and returns a human readable list of differences. The Go
// types may legitimately drop two sorts of member: keys beginning with "x_",
// which examples use to probe the additive rule, and members whose value is
// empty ({}, [] or null), which omitempty removes. Anything else that is
// missing, added or changed is reported.
func Diff(want, got []byte) ([]string, error) {
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		return nil, fmt.Errorf("want: %w", err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		return nil, fmt.Errorf("got: %w", err)
	}
	return diffValue("$", w, g), nil
}

func diffValue(path string, want, got any) []string {
	switch w := want.(type) {
	case map[string]any:
		return diffObject(path, w, got)
	case []any:
		return diffArray(path, w, got)
	case string:
		return diffString(path, w, got)
	default:
		return diffScalar(path, want, got)
	}
}

func diffObject(path string, w map[string]any, got any) []string {
	g, ok := got.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s: want object, got %T", path, got)}
	}
	var out []string
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child := path + "." + k
		gv, present := g[k]
		if !present {
			if strings.HasPrefix(k, "x_") || isEmpty(w[k]) {
				continue
			}
			out = append(out, fmt.Sprintf("%s: dropped by the Go type", child))
			continue
		}
		out = append(out, diffValue(child, w[k], gv)...)
	}
	for k := range g {
		if _, present := w[k]; !present {
			out = append(out, fmt.Sprintf("%s.%s: added by the Go type", path, k))
		}
	}
	return out
}

func diffArray(path string, w []any, got any) []string {
	g, ok := got.([]any)
	if !ok {
		return []string{fmt.Sprintf("%s: want array, got %T", path, got)}
	}
	if len(g) != len(w) {
		return []string{fmt.Sprintf("%s: want %d items, got %d", path, len(w), len(g))}
	}
	var out []string
	for i := range w {
		out = append(out, diffValue(fmt.Sprintf("%s[%d]", path, i), w[i], g[i])...)
	}
	return out
}

func diffString(path, w string, got any) []string {
	// Timestamps may be re-spelled by time.Time (for example .250Z
	// becomes .25Z). Compare them as instants.
	if g, ok := got.(string); ok {
		wt, werr := time.Parse(time.RFC3339Nano, w)
		gt, gerr := time.Parse(time.RFC3339Nano, g)
		if werr == nil && gerr == nil {
			if !wt.Equal(gt) {
				return []string{fmt.Sprintf("%s: want %s, got %s", path, w, g)}
			}
			return nil
		}
	}
	return diffScalar(path, w, got)
}

func diffScalar(path string, want, got any) []string {
	if !reflect.DeepEqual(want, got) {
		return []string{fmt.Sprintf("%s: want %v, got %v", path, want, got)}
	}
	return nil
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// SigningVectors is the content of schemas/examples/signing/ed25519.json.
// The seed is a published test key. It must never sign a real command.
type SigningVectors struct {
	Description  string          `json:"description"`
	Algorithm    string          `json:"algorithm"`
	SeedHex      string          `json:"seed_hex"`
	PublicKeyHex string          `json:"public_key_hex"`
	VerifyAt     time.Time       `json:"verify_at"`
	Vectors      []SigningVector `json:"vectors"`
}

// SigningVector is one signed command.
type SigningVector struct {
	Name string `json:"name"`
	// Command is the unsigned object {id, type, device_id, exp, body} in
	// whatever key order and number spelling the author chose, so that an
	// implementation has to canonicalize to match Canonical.
	Command json.RawMessage `json:"command"`
	// Canonical is the RFC 8785 form of Command, the bytes that are signed.
	Canonical string `json:"canonical"`
	// SignatureHex is the 64 byte Ed25519 signature in lowercase hex.
	SignatureHex string `json:"signature_hex"`
	// Sig is the same signature as it appears on the wire: standard base64
	// with padding.
	Sig string `json:"sig"`
}

// SigningInput is what GenerateSigningVectors needs for one vector.
type SigningInput struct {
	Name    string
	Command json.RawMessage
}

// LoadSigningVectors reads a vectors file.
func LoadSigningVectors(path string) (*SigningVectors, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f SigningVectors
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

// Keys derives the key pair from SeedHex.
func (f *SigningVectors) Keys() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	seed, err := hex.DecodeString(f.SeedHex)
	if err != nil {
		return nil, nil, fmt.Errorf("seed_hex: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, nil, fmt.Errorf("seed_hex: want %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	// The public half is the tail of the expanded private key.
	return ed25519.PublicKey(priv[ed25519.SeedSize:]), priv, nil
}

// Check recomputes every vector from the seed and the unsigned command and
// reports each mismatch. It also parses the signed command with the contract
// package and verifies it, so the file and the implementation agree.
func (f *SigningVectors) Check() []error {
	pub, priv, err := f.Keys()
	if err != nil {
		return []error{err}
	}
	var errs []error
	if got := hex.EncodeToString(pub); got != f.PublicKeyHex {
		errs = append(errs, fmt.Errorf("public_key_hex: want %s, got %s", f.PublicKeyHex, got))
	}
	for _, v := range f.Vectors {
		canonical, err := contract.Canonicalize(v.Command)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: canonicalize: %w", v.Name, err))
			continue
		}
		if string(canonical) != v.Canonical {
			errs = append(errs, fmt.Errorf("%s: canonical mismatch\n want %s\n got  %s", v.Name, v.Canonical, canonical))
		}
		sig := ed25519.Sign(priv, canonical)
		if got := hex.EncodeToString(sig); got != v.SignatureHex {
			errs = append(errs, fmt.Errorf("%s: signature_hex mismatch\n want %s\n got  %s", v.Name, v.SignatureHex, got))
		}
		if got := base64.StdEncoding.EncodeToString(sig); got != v.Sig {
			errs = append(errs, fmt.Errorf("%s: sig (base64) mismatch\n want %s\n got  %s", v.Name, v.Sig, got))
		}
		signed, err := SignedCommandJSON(v.Command, v.Sig)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", v.Name, err))
			continue
		}
		cmd, err := contract.ParseCommand(signed)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: parse signed command: %w", v.Name, err))
			continue
		}
		opts := contract.VerifyOptions{DeviceID: cmd.DeviceID, Now: func() time.Time { return f.VerifyAt }}
		if err := contract.VerifyCommand(pub, cmd, opts); err != nil {
			errs = append(errs, fmt.Errorf("%s: verify: %w", v.Name, err))
		}
	}
	return errs
}

// SignedCommandJSON appends "sig" to an unsigned command document, keeping
// the original members byte for byte.
func SignedCommandJSON(command json.RawMessage, sig string) ([]byte, error) {
	trimmed := bytes.TrimSpace(command)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, errors.New("command is not a JSON object")
	}
	sigJSON, err := json.Marshal(sig)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(trimmed[:len(trimmed)-1])
	if len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) > 0 {
		buf.WriteByte(',')
	}
	buf.WriteString(`"sig":`)
	buf.Write(sigJSON)
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// GenerateSigningVectors builds a vectors file from a seed and unsigned
// commands. It is used by the -update flag of the contract tests.
func GenerateSigningVectors(seed []byte, verifyAt time.Time, inputs []SigningInput) (*SigningVectors, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("seed must be %d bytes", ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	f := &SigningVectors{
		Description: "Test vectors for signed PWS commands. The seed is a PUBLISHED TEST KEY shared by every implementation's test suite; it must never sign a real command. Regenerate with: go test ./internal/contract -run TestSigningVectors -update",
		Algorithm: "Ed25519 (RFC 8032) over the RFC 8785 canonical JSON of the object {id, type, device_id, exp, body}. " +
			"On the wire the signature is carried in 'sig' as standard base64 with padding (RFC 4648 section 4). " +
			"verify_at is the clock value at which each command is still unexpired.",
		SeedHex:      hex.EncodeToString(seed),
		PublicKeyHex: hex.EncodeToString(priv[ed25519.SeedSize:]),
		VerifyAt:     verifyAt,
	}
	for _, in := range inputs {
		canonical, err := contract.Canonicalize(in.Command)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", in.Name, err)
		}
		sig := ed25519.Sign(priv, canonical)
		f.Vectors = append(f.Vectors, SigningVector{
			Name:         in.Name,
			Command:      in.Command,
			Canonical:    string(canonical),
			SignatureHex: hex.EncodeToString(sig),
			Sig:          base64.StdEncoding.EncodeToString(sig),
		})
	}
	return f, nil
}

// JCSVectors is the content of schemas/examples/signing/jcs.json.
type JCSVectors struct {
	Description string      `json:"description"`
	Vectors     []JCSVector `json:"vectors"`
}

// JCSVector is one canonicalization case. Input is kept as raw bytes so the
// author's whitespace and number spelling reach the canonicalizer unchanged.
type JCSVector struct {
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Expected string          `json:"expected"`
}

// LoadJCSVectors reads a JCS vectors file.
func LoadJCSVectors(path string) (*JCSVectors, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f JCSVectors
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

// Check canonicalizes every input and compares with Expected.
func (f *JCSVectors) Check() []error {
	var errs []error
	for _, v := range f.Vectors {
		got, err := contract.Canonicalize(v.Input)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", v.Name, err))
			continue
		}
		if string(got) != v.Expected {
			errs = append(errs, fmt.Errorf("%s: canonical mismatch\n want %s\n got  %s", v.Name, v.Expected, got))
		}
	}
	return errs
}

// Fill computes Expected for every vector. Used by the -update flag.
func (f *JCSVectors) Fill() error {
	for i := range f.Vectors {
		got, err := contract.Canonicalize(f.Vectors[i].Input)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Vectors[i].Name, err)
		}
		f.Vectors[i].Expected = string(got)
	}
	return nil
}

// VerifyCommandExample parses a valid cmd example and verifies its signature
// against pub at time now, so the committed examples stay signed with the
// published test key.
func VerifyCommandExample(ex Example, pub ed25519.PublicKey, now time.Time) error {
	cmd, err := contract.ParseCommand(ex.Data)
	if err != nil {
		return err
	}
	return contract.VerifyCommand(pub, cmd, contract.VerifyOptions{
		DeviceID: cmd.DeviceID,
		Now:      func() time.Time { return now },
	})
}

// WriteJSON writes v as indented JSON with a trailing newline, the layout
// used for committed example files.
func WriteJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o640)
}
