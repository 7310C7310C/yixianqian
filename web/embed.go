// Package webassets embeds the server-rendered templates and static assets.
package webassets

import "embed"

// FS contains templates/ and static/.
//
//go:embed templates/*.tmpl templates/pages/*.tmpl templates/partials/*.tmpl static/*
var FS embed.FS
