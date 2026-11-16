// Package web is the HTTPS server behind the pwsgw UI. It serves the
// embedded React bundle and the JSON API under /api.
//
// The server never reaches into the collector, the store or the uplink
// directly. It talks to them through the small interfaces in sources.go
// (StationSource, StatusSource, ReadingsSource) so this package builds and
// tests without hardware, and so the collector and uplink can be wired in
// later without touching any handler.
package web
