package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DefaultManifestBase is where the WxAlerts manifest service publishes
// bundles. The channel name is appended as a path segment.
const DefaultManifestBase = "https://updates.wxalerts.org/pws/"

// maxSmallFile bounds manifest, delegation and signature downloads.
const maxSmallFile = 1 << 20

// Source supplies the signed bundle for a channel.
type Source interface {
	Fetch(ctx context.Context, channel string) (Bundle, error)
	// ArtifactURL resolves an artifact URL from the manifest to an absolute
	// URL for channel.
	ArtifactURL(channel, ref string) (string, error)
}

// HTTPSource fetches over HTTPS from the manifest service.
type HTTPSource struct {
	// Base is the manifest service root, with or without a trailing slash.
	Base   string
	Client *http.Client
	// AllowInsecure permits http URLs (tests and the bench only).
	AllowInsecure bool
}

func (s *HTTPSource) base(channel string) (*url.URL, error) {
	b := s.Base
	if b == "" {
		b = DefaultManifestBase
	}
	u, err := url.Parse(strings.TrimRight(b, "/") + "/" + url.PathEscape(channel) + "/")
	if err != nil {
		return nil, err
	}
	if !s.schemeOK(u.Scheme) {
		return nil, fmt.Errorf("update: manifest base %q must be https", b)
	}
	return u, nil
}

// Fetch downloads manifest.json, manifest.json.sig, delegation.json and
// delegation.json.sig for the channel.
func (s *HTTPSource) Fetch(ctx context.Context, channel string) (Bundle, error) {
	base, err := s.base(channel)
	if err != nil {
		return Bundle{}, err
	}
	var b Bundle
	for _, f := range []struct {
		name string
		dst  *[]byte
	}{
		{"manifest.json", &b.Manifest}, {"manifest.json.sig", &b.ManifestSig},
		{"delegation.json", &b.Delegation}, {"delegation.json.sig", &b.DelegationSig},
	} {
		data, err := s.get(ctx, base.JoinPath(f.name).String())
		if err != nil {
			return Bundle{}, fmt.Errorf("fetch %s: %w", f.name, err)
		}
		*f.dst = data
	}
	return b, nil
}

func (s *HTTPSource) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c := s.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSmallFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSmallFile {
		return nil, fmt.Errorf("response is larger than %d bytes", maxSmallFile)
	}
	return data, nil
}

// ArtifactURL resolves ref against the channel directory. Only https (or http
// when AllowInsecure) is accepted, so a manifest cannot point a device at a
// file or ftp URL.
func (s *HTTPSource) ArtifactURL(channel, ref string) (string, error) {
	base, err := s.base(channel)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	abs := base.ResolveReference(r)
	if !s.schemeOK(abs.Scheme) {
		return "", fmt.Errorf("artifact url %q must be https", ref)
	}
	return abs.String(), nil
}

func (s *HTTPSource) schemeOK(scheme string) bool {
	return scheme == "https" || (s.AllowInsecure && scheme == "http")
}
