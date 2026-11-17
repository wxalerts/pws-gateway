// Package updatetest builds signed update bundles for tests. Its keys come
// from fixed seeds that are published in this repository, so they must never
// be pinned in a real device.
package updatetest

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wxalerts/pws-gateway/internal/update"
)

// Epoch is the fixed "now" of the fixtures.
var Epoch = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// Key is a test key pair.
type Key struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

// NewKey derives a key from a label, deterministically.
func NewKey(label string) Key {
	seed := sha256.Sum256([]byte("pws-gateway update test key: " + label))
	priv := ed25519.NewKeyFromSeed(seed[:])
	return Key{Priv: priv, Pub: ed25519.PublicKey(priv[32:])}
}

// Sig returns the .sig file contents for msg.
func (k Key) Sig(msg []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(k.Priv, msg)) + "\n")
}

// PubFile returns the .pub file contents.
func (k Key) PubFile() []byte {
	return []byte(base64.StdEncoding.EncodeToString(k.Pub) + "\n")
}

// Keys are the standard cast: two roots and two release keys.
var (
	Root         = NewKey("root")
	RootBackup   = NewKey("root-backup")
	Release      = NewKey("release")
	ReleaseNext  = NewKey("release-next")
	Stranger     = NewKey("stranger")
	Installed    = 1020399 // version 1.2.3
	InstalledStr = "1.2.3"
)

// Trust pins both roots, as a device does.
func Trust() update.Trust {
	return update.Trust{Roots: []ed25519.PublicKey{Root.Pub, RootBackup.Pub}}
}

// DelegationOptions shapes a delegation.
type DelegationOptions struct {
	Keys     []Key
	NotAfter time.Time
	Revoked  []string
}

// Delegation builds delegation.json bytes naming the keys.
func Delegation(o DelegationOptions) []byte {
	if o.NotAfter.IsZero() {
		o.NotAfter = Epoch.AddDate(1, 0, 0)
	}
	d := update.Delegation{Schema: 1, Issued: Epoch.AddDate(0, -1, 0), Revoked: o.Revoked}
	if d.Revoked == nil {
		d.Revoked = []string{}
	}
	for _, k := range o.Keys {
		d.OnlineKeys = append(d.OnlineKeys, update.OnlineKey{
			ID: update.KeyID(k.Pub), Pub: base64.StdEncoding.EncodeToString(k.Pub), NotAfter: o.NotAfter,
		})
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

// ChunkHashes returns the per-chunk SHA-256 list for data.
func ChunkHashes(data []byte, chunk int) []string {
	var out []string
	for off := 0; off < len(data); off += chunk {
		sum := sha256.Sum256(data[off:min(off+chunk, len(data))])
		out = append(out, hex.EncodeToString(sum[:]))
	}
	return out
}

// Gzip compresses data.
func Gzip(data []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// Artifact describes content as a manifest artifact served at url.
func Artifact(kind, name, url string, content []byte, chunk int) update.Artifact {
	sum := sha256.Sum256(content)
	return update.Artifact{
		Name: name, Kind: kind, OS: "linux", Arch: "arm64",
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)),
		URL: url, ChunkSize: int64(chunk), Chunks: ChunkHashes(content, chunk),
	}
}

// Manifest returns a manifest for version v expiring in 90 days.
func Manifest(v string, arts ...update.Artifact) update.Manifest {
	n, err := update.VersionNumber(v)
	if err != nil {
		panic(err)
	}
	return update.Manifest{
		Schema: 1, Version: v, VersionNumber: n, Channel: "stable",
		Created: Epoch, Expires: Epoch.AddDate(0, 0, 90), Commit: "abcdef0",
		Artifacts: arts,
	}
}

// Sign produces a bundle: the delegation signed by root, the manifest signed
// by signer.
func Sign(m update.Manifest, signer Key, root Key, del []byte) update.Bundle {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		panic(fmt.Sprint("updatetest: ", err))
	}
	raw = append(raw, '\n')
	return update.Bundle{
		Manifest: raw, ManifestSig: signer.Sig(raw),
		Delegation: del, DelegationSig: root.Sig(del),
	}
}

// StandardBundle is a manifest for v signed by Release under a delegation
// signed by Root.
func StandardBundle(m update.Manifest) update.Bundle {
	return Sign(m, Release, Root, Delegation(DelegationOptions{Keys: []Key{Release}}))
}
