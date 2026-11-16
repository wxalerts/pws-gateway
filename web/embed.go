// Package web embeds the built React single-page application.
//
// `npm run build` inside this directory writes the production bundle to
// web/dist/. The Go binary embeds that directory so the Pi image ships a
// single executable. In a development checkout dist/ contains only .keep and
// the HTTP server falls back to a placeholder page that explains how to build
// the UI.
package web

import "embed"

// Dist holds the compiled UI. Paths are prefixed with "dist/", so callers use
// fs.Sub(Dist, "dist") to serve it as the site root.
//
//go:embed all:dist
var Dist embed.FS
