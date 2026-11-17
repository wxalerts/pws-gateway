package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion is the only manifest and delegation format this code reads.
const SchemaVersion = 1

// Artifact kinds.
const (
	// KindApp is a single pwsgw executable for the device architecture.
	KindApp = "app"
	// KindOSBoot and KindOSRoot are gzip compressed raw partition images for
	// the boot and root partitions of the inactive A/B slot.
	KindOSBoot = "os-boot"
	KindOSRoot = "os-root"
)

// Artifact is one downloadable file listed in a manifest.
type Artifact struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// URL is absolute https, or relative to the manifest directory.
	URL string `json:"url"`
	// ChunkSize and Chunks give the SHA-256 (hex) of every ChunkSize bytes
	// of the file, the last chunk being shorter. They are what makes a
	// download resumable and individually repairable on a weak link.
	ChunkSize int64    `json:"chunk_size"`
	Chunks    []string `json:"chunks"`
	// RawSize is the decompressed size, required for OS images.
	RawSize int64 `json:"raw_size,omitempty"`
}

// Rollback authorizes a manifest whose version_number is lower than the
// installed one: only devices running one of FromVersionNumbers may take it.
type Rollback struct {
	FromVersionNumbers []int  `json:"from_version_numbers"`
	Reason             string `json:"reason,omitempty"`
}

// Manifest is manifest.json, the signed list of what a release contains.
type Manifest struct {
	Schema        int    `json:"schema"`
	Version       string `json:"version"`
	VersionNumber int    `json:"version_number"`
	// OSVersionNumber numbers the OS image revision that the os-boot and
	// os-root artifacts carry. Zero or absent when the release has no OS
	// image. A device installs the OS image only when this is higher than the
	// revision it runs.
	OSVersionNumber int             `json:"os_version_number,omitempty"`
	Channel         string          `json:"channel,omitempty"`
	Created         time.Time       `json:"created"`
	Expires         time.Time       `json:"expires"`
	Commit          string          `json:"commit,omitempty"`
	Artifacts       []Artifact      `json:"artifacts"`
	Images          json.RawMessage `json:"images,omitempty"`
	Rollback        *Rollback       `json:"rollback,omitempty"`
}

// OnlineKey is one release key named by a delegation.
type OnlineKey struct {
	ID       string    `json:"id"`
	Pub      string    `json:"pub"`
	NotAfter time.Time `json:"not_after"`
	Comment  string    `json:"comment,omitempty"`
}

// Delegation is delegation.json, signed by a root key.
type Delegation struct {
	Schema     int         `json:"schema"`
	Issued     time.Time   `json:"issued"`
	OnlineKeys []OnlineKey `json:"online_keys"`
	Revoked    []string    `json:"revoked"`
}

// Bundle is the four files a device downloads before it trusts anything.
type Bundle struct {
	Manifest      []byte
	ManifestSig   []byte
	Delegation    []byte
	DelegationSig []byte
}

// KeyID is the first 16 hex characters of SHA-256 over the raw public key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// ParseKeyFile decodes a .pub file: one base64 line of the 32 raw bytes,
// lines starting with # ignored.
func ParseKeyFile(data []byte) (ed25519.PublicKey, error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return parsePubB64(line)
	}
	return nil, errors.New("update: key file has no key line")
}

func parsePubB64(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("update: public key must be %d bytes of base64", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// parseSig decodes a .sig file: one base64 line.
func parseSig(data []byte) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(b) != ed25519.SignatureSize {
		return nil, errors.New("update: signature must be one line of base64 for 64 bytes")
	}
	return b, nil
}

func verifyRaw(pub ed25519.PublicKey, msg, sigFile []byte) bool {
	sig, err := parseSig(sigFile)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, msg, sig)
}

var reHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ParseManifest decodes and structurally validates manifest bytes. It does not
// check the signature, expiry or version.
func ParseManifest(raw []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m.Schema != SchemaVersion {
		return nil, fmt.Errorf("manifest schema %d is not supported", m.Schema)
	}
	if m.Version == "" || m.VersionNumber <= 0 {
		return nil, errors.New("manifest has no version or version_number")
	}
	if n, err := VersionNumber(m.Version); err != nil || n != m.VersionNumber {
		return nil, fmt.Errorf("manifest version %q does not match version_number %d", m.Version, m.VersionNumber)
	}
	if m.Expires.IsZero() {
		return nil, errors.New("manifest has no expires")
	}
	for i := range m.Artifacts {
		if err := m.Artifacts[i].validate(); err != nil {
			return nil, fmt.Errorf("artifact %d (%s): %w", i, m.Artifacts[i].Name, err)
		}
	}
	return &m, nil
}

func (a *Artifact) validate() error {
	switch {
	case a.Name == "" || strings.ContainsAny(a.Name, `/\`) || a.Name == "." || a.Name == "..":
		return errors.New("name must be a plain file name")
	case a.Kind != KindApp && a.Kind != KindOSBoot && a.Kind != KindOSRoot:
		return fmt.Errorf("unknown kind %q", a.Kind)
	case !reHex64.MatchString(a.SHA256):
		return errors.New("sha256 must be 64 lowercase hex characters")
	case a.Size <= 0:
		return errors.New("size must be positive")
	case a.URL == "":
		return errors.New("url is empty")
	case a.ChunkSize <= 0:
		return errors.New("chunk_size must be positive")
	case int64(len(a.Chunks)) != (a.Size+a.ChunkSize-1)/a.ChunkSize:
		return errors.New("chunks does not cover the size")
	}
	for _, c := range a.Chunks {
		if !reHex64.MatchString(c) {
			return errors.New("chunk hash must be 64 lowercase hex characters")
		}
	}
	if a.Kind != KindApp && a.RawSize <= 0 {
		return errors.New("os images need raw_size")
	}
	return nil
}

// Find returns the artifact of kind for this OS and architecture. OS images
// are architecture specific too.
func (m *Manifest) Find(kind, goos, arch string) (Artifact, bool) {
	for i := range m.Artifacts {
		if a := &m.Artifacts[i]; a.Kind == kind && a.OS == goos && a.Arch == arch {
			return *a, true
		}
	}
	return Artifact{}, false
}

var reSemver = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$`)

// VersionNumber computes MAJOR*1000000 + MINOR*10000 + PATCH*100 + T where T
// is 99 for a release and N for -rc.N, as docs/release-signing.md defines.
func VersionNumber(v string) (int, error) {
	m := reSemver.FindStringSubmatch(strings.TrimPrefix(v, "v"))
	if m == nil {
		return 0, fmt.Errorf("version %q is not MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-rc.N", v)
	}
	n := func(s string) int {
		i, err := strconv.Atoi(s)
		if err != nil {
			return 0
		}
		return i
	}
	t := 99
	if m[4] != "" {
		t = n(m[4])
		if t < 1 || t > 98 {
			return 0, fmt.Errorf("rc number %d is out of range", t)
		}
	}
	if n(m[2]) > 99 || n(m[3]) > 99 {
		return 0, fmt.Errorf("version %q has a component above 99", v)
	}
	return n(m[1])*1000000 + n(m[2])*10000 + n(m[3])*100 + t, nil
}
