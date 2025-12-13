//go:build !dev

package templates

import (
	"embed"
	"io/fs"
)

//go:embed *.go.tmpl partials/*.go.tmpl
var FS embed.FS

// EmbeddedFS exports FS as an io/fs.FS for use with template system
var EmbeddedFS fs.FS = FS
