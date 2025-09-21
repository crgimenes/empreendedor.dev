//go:build dev

package assets

import "net/http"

var FS = http.Dir("./assets")
