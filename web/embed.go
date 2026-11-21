// SPDX-License-Identifier: Apache-2.0

// Package web embeds the static assets (CSS, JS, vendored htmx) into the
// binary. The handlers and templates live in internal/web.
package web

import "embed"

// Static holds everything under web/static.
//
//go:embed static
var Static embed.FS
