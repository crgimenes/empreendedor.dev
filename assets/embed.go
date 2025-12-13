//go:build !dev

package assets

import (
	"embed"
	"mime"
	"net/http"
)

// Arquivos específicos da aplicação (favicons, manifests)
//
//go:embed *.png *.svg *.ico
var assets embed.FS

var FS = http.FS(assets)

func init() {
	// Ensure correct MIME types for certain assets.
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".png", "image/png")
	_ = mime.AddExtensionType(".ico", "image/x-icon")
}
