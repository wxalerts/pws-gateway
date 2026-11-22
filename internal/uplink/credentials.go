package uplink

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// Credentials is the content of the credential file named by
// config.Uplink.CredentialFile. The password never lives in config.json and
// never appears in a log line or error message: the type redacts itself.
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// UserID is the owner's numeric account id from the claim response. When
	// present the uplink uses the V2 topics V2/{user_id}/PWS/{device_id}/...;
	// a credential file without it (claimed before contract v2) keeps the v1
	// topics.
	UserID int64 `json:"user_id,omitempty"`
	// ClientCertFile and ClientKeyFile are optional PEM paths for mutual TLS.
	// Both or neither.
	ClientCertFile string `json:"client_cert_file,omitempty"`
	ClientKeyFile  string `json:"client_key_file,omitempty"`
}

// LogValue implements slog.LogValuer so a Credentials passed to a logger by
// mistake prints no secret.
func (c Credentials) LogValue() slog.Value {
	return slog.GroupValue(slog.String("username", c.Username), slog.String("password", "[redacted]"))
}

// String redacts the password for fmt verbs.
func (c Credentials) String() string { return "uplink credentials for " + c.Username }

// GoString redacts the password for %#v.
func (c Credentials) GoString() string { return c.String() }

// ErrCredentials is wrapped by every credential file problem. The wrapped
// message never contains file content.
var ErrCredentials = errors.New("uplink: bad credential file")

// LoadCredentials reads and validates the credential file. Unknown keys are
// rejected so a typo does not silently drop the client certificate.
func LoadCredentials(path string) (Credentials, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		// Do not wrap err: a JSON syntax error can quote a fragment.
		return Credentials{}, fmt.Errorf("%w: not valid JSON of the expected shape", ErrCredentials)
	}
	if c.Username == "" || c.Password == "" {
		return Credentials{}, fmt.Errorf("%w: username and password are required", ErrCredentials)
	}
	if c.UserID < 0 {
		return Credentials{}, fmt.Errorf("%w: user_id must be a positive account id", ErrCredentials)
	}
	if (c.ClientCertFile == "") != (c.ClientKeyFile == "") {
		return Credentials{}, fmt.Errorf("%w: client_cert_file and client_key_file must be set together", ErrCredentials)
	}
	return c, nil
}

// TLSConfig builds the broker TLS configuration: the system roots plus the
// optional pinned CA file, TLS 1.2 or newer, and the client certificate when
// the credentials name one.
func TLSConfig(serverName string, cred Credentials, caFile string) (*tls.Config, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("uplink: read CA file: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("uplink: CA file %s holds no PEM certificate", caFile)
		}
	}
	cfg := &tls.Config{RootCAs: pool, ServerName: serverName, MinVersion: tls.VersionTLS12}
	if cred.ClientCertFile != "" {
		cert, err := tls.LoadX509KeyPair(cred.ClientCertFile, cred.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("uplink: load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
