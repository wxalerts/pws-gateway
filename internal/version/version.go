// Package version holds the build identity of the pwsgw binary.
//
// The three variables are overwritten at link time by the release build:
//
//	go build -ldflags "-X github.com/wxalerts/pws-gateway/internal/version.Version=1.2.3 \
//	  -X github.com/wxalerts/pws-gateway/internal/version.Commit=abcdef0 \
//	  -X github.com/wxalerts/pws-gateway/internal/version.BuildDate=2026-01-02T03:04:05Z"
//
// A plain `go build` leaves the development defaults in place so that a dev
// binary is always recognisable as one in logs and in the web UI.
package version

// Version is the semantic version of the release, or "dev" for local builds.
var Version = "dev"

// Commit is the short git commit the binary was built from, or "unknown".
var Commit = "unknown"

// BuildDate is the RFC 3339 UTC build timestamp, or "unknown".
var BuildDate = "unknown"

// String returns a single line suitable for `pwsgw version` and log banners.
func String() string {
	return Version + " (" + Commit + ", " + BuildDate + ")"
}
