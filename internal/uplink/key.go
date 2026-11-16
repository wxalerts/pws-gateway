package uplink

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wxalerts/pws-gateway/keys"
)

// ErrNoCommandKey means no command-signing public key is pinned, so every
// command is ignored.
var ErrNoCommandKey = errors.New("uplink: no command-signing public key is pinned")

// ParsePublicKey decodes a key file: 64 hex characters or standard base64 of
// the 32 byte Ed25519 public key, whitespace ignored.
func ParsePublicKey(data []byte) (ed25519.PublicKey, error) {
	s := strings.TrimSpace(string(data))
	if b, err := hex.DecodeString(s); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	return nil, fmt.Errorf("uplink: public key must be %d bytes as hex or base64", ed25519.PublicKeySize)
}

// LoadCommandKey returns the pinned command-signing public key: the one
// embedded in the binary from keys/command-signing.pub when the release build
// added it, otherwise <dataDir>/keys/command-signing.pub. It returns
// ErrNoCommandKey when neither exists. The embedded test key is never used
// here.
func LoadCommandKey(dataDir string) (ed25519.PublicKey, string, error) {
	if b, err := keys.FS.ReadFile(keys.CommandSigning); err == nil {
		k, err := ParsePublicKey(b)
		return k, "embedded " + keys.CommandSigning, err
	}
	path := filepath.Join(dataDir, "keys", keys.CommandSigning)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", ErrNoCommandKey
	}
	if err != nil {
		return nil, "", err
	}
	k, err := ParsePublicKey(b)
	return k, path, err
}
