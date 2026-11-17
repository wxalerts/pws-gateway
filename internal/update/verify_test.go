package update_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/update"
	"github.com/wxalerts/pws-gateway/internal/update/updatetest"
)

func manifestFor(v string) update.Manifest {
	return updatetest.Manifest(v, updatetest.Artifact(update.KindApp, "pwsgw-linux-arm64", "pwsgw-linux-arm64", []byte("binary"), 4))
}

func TestVerifyAcceptsDelegatedReleaseKey(t *testing.T) {
	b := updatetest.StandardBundle(manifestFor("1.3.0"))
	m, dec, err := updatetest.Trust().Verify(b, updatetest.Epoch, updatetest.Installed)
	if err != nil {
		t.Fatal(err)
	}
	if dec != update.DecisionNewer || m.Version != "1.3.0" {
		t.Fatalf("got %v %s", dec, m.Version)
	}
}

func TestVerifySameVersion(t *testing.T) {
	b := updatetest.StandardBundle(manifestFor(updatetest.InstalledStr))
	_, dec, err := updatetest.Trust().Verify(b, updatetest.Epoch, updatetest.Installed)
	if err != nil || dec != update.DecisionSame {
		t.Fatalf("got %v, %v", dec, err)
	}
}

// Each rejection has its own code so the update message tells them apart.
func TestVerifyRejections(t *testing.T) {
	now := updatetest.Epoch
	del := updatetest.Delegation(updatetest.DelegationOptions{Keys: []updatetest.Key{updatetest.Release}})

	expired := manifestFor("1.3.0")
	expired.Expires = now.Add(-time.Hour)
	lower := manifestFor("1.2.0")
	rbOther := manifestFor("1.2.0")
	rbOther.Rollback = &update.Rollback{FromVersionNumbers: []int{1030099}}

	tests := []struct {
		name   string
		bundle update.Bundle
		code   string
	}{
		{"expired manifest", updatetest.Sign(expired, updatetest.Release, updatetest.Root, del), update.CodeManifestExpiry},
		{"lower version without authorization", updatetest.Sign(lower, updatetest.Release, updatetest.Root, del), update.CodeVersionLower},
		{"rollback authorized for another version", updatetest.Sign(rbOther, updatetest.Release, updatetest.Root, del), update.CodeVersionLower},
		{"unknown signing key", updatetest.Sign(manifestFor("1.3.0"), updatetest.Stranger, updatetest.Root, del), update.CodeUnknownKey},
		{"delegation signed by a stranger root", updatetest.Sign(manifestFor("1.3.0"), updatetest.Release, updatetest.Stranger, del), update.CodeBadDelegation},
		{"revoked release key", updatetest.Sign(manifestFor("1.3.0"), updatetest.Release, updatetest.Root,
			updatetest.Delegation(updatetest.DelegationOptions{
				Keys: []updatetest.Key{updatetest.Release, updatetest.ReleaseNext}, Revoked: []string{update.KeyID(updatetest.Release.Pub)},
			})), update.CodeKeyRevoked},
		{"release key past not_after", updatetest.Sign(manifestFor("1.3.0"), updatetest.Release, updatetest.Root,
			updatetest.Delegation(updatetest.DelegationOptions{
				Keys: []updatetest.Key{updatetest.Release}, NotAfter: now.Add(-time.Minute),
			})), update.CodeKeyExpired},
	}
	seen := map[string]bool{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := updatetest.Trust().Verify(tc.bundle, now, updatetest.Installed)
			if got := update.CodeOf(err); got != tc.code {
				t.Fatalf("code = %q (%v), want %q", got, err, tc.code)
			}
		})
		seen[tc.code] = true
	}
	if len(seen) < 6 {
		t.Fatalf("only %d distinct codes", len(seen))
	}
}

func TestVerifyTamperedManifestFailsSignature(t *testing.T) {
	b := updatetest.StandardBundle(manifestFor("1.3.0"))
	b.Manifest = append([]byte(nil), b.Manifest...)
	b.Manifest[10] ^= 1
	_, _, err := updatetest.Trust().Verify(b, updatetest.Epoch, updatetest.Installed)
	if update.CodeOf(err) != update.CodeUnknownKey {
		t.Fatalf("got %v", err)
	}
}

func TestRollbackAuthorization(t *testing.T) {
	m := manifestFor("1.2.0")
	m.Rollback = &update.Rollback{FromVersionNumbers: []int{updatetest.Installed}, Reason: "1.2.3 breaks the modem"}
	b := updatetest.StandardBundle(m)
	got, dec, err := updatetest.Trust().Verify(b, updatetest.Epoch, updatetest.Installed)
	if err != nil || dec != update.DecisionRollback || got.Version != "1.2.0" {
		t.Fatalf("got %v %v %v", got, dec, err)
	}
}

// Rotating to a release key delegated by the backup root needs no change on
// the device: both roots are pinned, either may sign the delegation.
func TestBackupRootDelegationWorks(t *testing.T) {
	del := updatetest.Delegation(updatetest.DelegationOptions{Keys: []updatetest.Key{updatetest.ReleaseNext}})
	b := updatetest.Sign(manifestFor("1.3.0"), updatetest.ReleaseNext, updatetest.RootBackup, del)
	if _, _, err := updatetest.Trust().Verify(b, updatetest.Epoch, updatetest.Installed); err != nil {
		t.Fatalf("backup root delegation rejected: %v", err)
	}
	// The primary root keeps working for the same device.
	b = updatetest.Sign(manifestFor("1.3.0"), updatetest.ReleaseNext, updatetest.Root, del)
	if _, _, err := updatetest.Trust().Verify(b, updatetest.Epoch, updatetest.Installed); err != nil {
		t.Fatalf("primary root delegation rejected: %v", err)
	}
	// A device that pins only the primary root would reject the backup root.
	only := update.Trust{Roots: []ed25519.PublicKey{updatetest.Root.Pub}}
	b = updatetest.Sign(manifestFor("1.3.0"), updatetest.ReleaseNext, updatetest.RootBackup, del)
	if _, _, err := only.Verify(b, updatetest.Epoch, updatetest.Installed); update.CodeOf(err) != update.CodeBadDelegation {
		t.Fatalf("got %v", err)
	}
	// Old release key stops working once the new delegation revokes it.
	del2 := updatetest.Delegation(updatetest.DelegationOptions{
		Keys: []updatetest.Key{updatetest.Release, updatetest.ReleaseNext}, Revoked: []string{update.KeyID(updatetest.Release.Pub)},
	})
	b = updatetest.Sign(manifestFor("1.3.0"), updatetest.Release, updatetest.RootBackup, del2)
	if _, _, err := updatetest.Trust().Verify(b, updatetest.Epoch, updatetest.Installed); update.CodeOf(err) != update.CodeKeyRevoked {
		t.Fatalf("got %v", err)
	}
}

func TestVersionNumber(t *testing.T) {
	cases := map[string]int{"1.2.3": 1020399, "0.0.1": 199, "2.0.0": 2000099, "1.0.0-rc.2": 1000002, "v1.2.3": 1020399}
	for v, want := range cases {
		got, err := update.VersionNumber(v)
		if err != nil || got != want {
			t.Errorf("%s: %d, %v, want %d", v, got, err, want)
		}
	}
	for _, bad := range []string{"dev", "1.2", "1.2.3-beta", "1.2.100", "1.0.0-rc.0"} {
		if _, err := update.VersionNumber(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	// rc sorts below the release of the same version.
	rc, _ := update.VersionNumber("1.3.0-rc.5")
	rel, _ := update.VersionNumber("1.3.0")
	if rc >= rel {
		t.Fatal("rc must sort below release")
	}
}

// The published command-signing test seed is a plain Ed25519 key, and the
// release signature format is a raw signature over the file bytes, so the
// same seed works as a root in a conformance check of the raw format.
func TestRawSignatureFormatWithPublishedSeed(t *testing.T) {
	raw, err := os.ReadFile("../../schemas/examples/signing/ed25519.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		SeedHex string `json:"seed_hex"`
		PubHex  string `json:"public_key_hex"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	seed, _ := hex.DecodeString(v.SeedHex)
	priv := ed25519.NewKeyFromSeed(seed)
	if hex.EncodeToString(priv.Public().(ed25519.PublicKey)) != v.PubHex {
		t.Fatal("seed does not match the published public key")
	}
	k := updatetest.Key{Priv: priv, Pub: priv.Public().(ed25519.PublicKey)}
	del := updatetest.Delegation(updatetest.DelegationOptions{Keys: []updatetest.Key{updatetest.Release}})
	b := updatetest.Sign(manifestFor("1.3.0"), updatetest.Release, k, del)
	tr := update.Trust{Roots: []ed25519.PublicKey{k.Pub}}
	if _, _, err := tr.Verify(b, updatetest.Epoch, updatetest.Installed); err != nil {
		t.Fatal(err)
	}
	pub, err := update.ParseKeyFile(append([]byte("# comment\n"), k.PubFile()...))
	if err != nil || !pub.Equal(k.Pub) {
		t.Fatalf("ParseKeyFile: %v", err)
	}
}

func rootPub() []byte   { return updatetest.Root.PubFile() }
func backupPub() []byte { return updatetest.RootBackup.PubFile() }
