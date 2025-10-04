//go:build !dev

package assets

import (
	"embed"
	"mime"
	"net/http"
)

//go:embed *.webmanifest *.png *.svg *.ico *.css bootstrap/css/*.min.css bootstrap/js/*.min.js bootstrap/js/*.min.js.map bootstrap/css/*.min.css.map
var assets embed.FS

var FS = http.FS(assets)

func init() {
	// Ensure correct MIME types for certain assets.
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	_ = mime.AddExtensionType(".css", "text/css")
	_ = mime.AddExtensionType(".js", "application/javascript")
	_ = mime.AddExtensionType(".map", "application/json")

}
