package claim

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wxalerts/pws-gateway/internal/atomicfile"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// HWIDDomain is the fixed domain string mixed into the hardware hash. It
// keeps the value unusable for anything but this protocol: the same serial
// hashed elsewhere gives a different digest.
const HWIDDomain = "wxalerts-pws-hwid-v1"

// hwidPrefix marks the derivation version. The server accepts 8 to 128
// characters; the prefix plus a SHA-256 hex digest is 68.
const hwidPrefix = "hw1_"

// hwidFile holds a generated id on a host with neither a serial nor a
// machine id (a container, for example).
const hwidFile = "hwid"

// Hardware id sources, for logs and docs. The values are never sent.
const (
	SourceSerial    = "serial"
	SourceMachineID = "machine-id"
	SourceGenerated = "generated"
)

// HardwareID derives the id the gateway sends as hw_id. The input is the
// board serial when the platform reports one, else the machine id, else a
// random value generated once and kept in dataDir. The input is hashed as
// SHA-256(HWIDDomain NUL source ":" value), so the raw serial never leaves
// the device. The result is "hw1_" plus 64 lowercase hex characters.
func HardwareID(b platform.BoardInfo, dataDir string) (id, source string, err error) {
	value, source := strings.TrimSpace(b.Serial), SourceSerial
	if value == "" {
		value, source = strings.TrimSpace(b.MachineID), SourceMachineID
	}
	if value == "" {
		source = SourceGenerated
		if value, err = generatedID(dataDir); err != nil {
			return "", "", err
		}
	}
	return hashHWID(source, value), source, nil
}

func hashHWID(source, value string) string {
	sum := sha256.Sum256([]byte(HWIDDomain + "\x00" + source + ":" + value))
	return hwidPrefix + hex.EncodeToString(sum[:])
}

func generatedID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, hwidFile)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if v := strings.TrimSpace(string(raw)); len(v) >= 16 {
			return v, nil
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("claim: read %s: %w", path, err)
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("claim: generate hardware id: %w", err)
	}
	v := hex.EncodeToString(buf)
	if err := atomicfile.WriteFile(path, []byte(v+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("claim: save %s: %w", path, err)
	}
	return v, nil
}
