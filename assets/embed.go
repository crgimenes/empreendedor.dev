//go:build !dev

package assets

import (
	"embed"
	"net/http"
)

//go:embed *.webmanifest *.png *.svg
var assets embed.FS

var FS = http.FS(assets)
