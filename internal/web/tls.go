package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/wxalerts/pws-gateway/internal/atomicfile"
)

// Self-signed certificate file names inside <data-dir>/tls.
const (
	CertFileName = "cert.pem"
	KeyFileName  = "key.pem"
	// MDNSName is the name the gateway is reachable under on the local
	// network.
	MDNSName = "pwsgw.local"
)

const (
	certLifetime = 10 * 365 * 24 * time.Hour
	// certRenewBefore regenerates a certificate that is close to expiry so a
	// long running appliance never serves an expired one.
	certRenewBefore = 30 * 24 * time.Hour
)

// CertNames is the set of subject alternative names a certificate must cover.
type CertNames struct {
	DNS []string
	IPs []net.IP
}

// LocalCertNames collects pwsgw.local, the host name and the current
// non-loopback unicast addresses.
func LocalCertNames() CertNames {
	names := CertNames{DNS: []string{MDNSName}}
	if h, err := os.Hostname(); err == nil && h != "" {
		names.DNS = append(names.DNS, h)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLoopback() || !ipn.IP.IsGlobalUnicast() {
				continue
			}
			names.IPs = append(names.IPs, ipn.IP)
		}
	}
	return names.normalize()
}

func (n CertNames) normalize() CertNames {
	dns := map[string]bool{}
	for _, d := range n.DNS {
		if d != "" {
			dns[d] = true
		}
	}
	ips := map[string]net.IP{}
	for _, ip := range n.IPs {
		ips[ip.String()] = ip
	}
	var out CertNames
	for d := range dns {
		out.DNS = append(out.DNS, d)
	}
	sort.Strings(out.DNS)
	keys := make([]string, 0, len(ips))
	for k := range ips {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.IPs = append(out.IPs, ips[k])
	}
	return out
}

// EnsureCert returns a certificate for names stored under dir, generating a
// new ECDSA P-256 one when none exists, it cannot be loaded, it is close to
// expiry or its SAN set differs from names. regenerated reports whether a new
// pair was written.
func EnsureCert(dir string, names CertNames, now time.Time) (cert tls.Certificate, regenerated bool, err error) {
	names = names.normalize()
	certPath := filepath.Join(dir, CertFileName)
	keyPath := filepath.Join(dir, KeyFileName)

	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil && len(c.Certificate) > 0 {
		if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil && certCurrent(leaf, names, now) {
			c.Leaf = leaf
			return c, false, nil
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, false, fmt.Errorf("web: create tls dir: %w", err)
	}
	certPEM, keyPEM, err := generateCert(names, now)
	if err != nil {
		return tls.Certificate{}, false, err
	}
	// Key first: a crash between the two writes leaves a pair that fails to
	// load, which simply triggers regeneration on the next start.
	if err := atomicfile.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, false, fmt.Errorf("web: write key: %w", err)
	}
	if err := atomicfile.WriteFile(certPath, certPEM, 0o644); err != nil { //nolint:gosec // a certificate is public
		return tls.Certificate{}, false, fmt.Errorf("web: write certificate: %w", err)
	}
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, false, err
	}
	return c, true, nil
}

func certCurrent(leaf *x509.Certificate, names CertNames, now time.Time) bool {
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter.Add(-certRenewBefore)) {
		return false
	}
	have := CertNames{DNS: leaf.DNSNames, IPs: leaf.IPAddresses}.normalize()
	if len(have.DNS) != len(names.DNS) || len(have.IPs) != len(names.IPs) {
		return false
	}
	for i := range have.DNS {
		if have.DNS[i] != names.DNS[i] {
			return false
		}
	}
	for i := range have.IPs {
		if !have.IPs[i].Equal(names.IPs[i]) {
			return false
		}
	}
	return true
}

func generateCert(names CertNames, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("web: generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, fmt.Errorf("web: serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: MDNSName, Organization: []string{"pwsgw self-signed"}},
		// Backdate a little so a Pi whose clock is slightly behind a browser
		// does not see a certificate that is not yet valid.
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              names.DNS,
		IPAddresses:           names.IPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("web: sign certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}
