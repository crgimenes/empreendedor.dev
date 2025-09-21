//go:build !dev

package assets

import (
	"embed"
	"mime"
	"net/http"
)

//go:embed *.webmanifest *.png *.svg *.ico
var assets embed.FS

var FS = http.FS(assets)

func init() {
	// Ensure correct MIME types for certain assets.
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}
