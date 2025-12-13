//go:build dev

package templates

import (
	"io/fs"
	"os"
)

// EmbeddedFS provides access to templates via DirFS in dev mode
var EmbeddedFS fs.FS = os.DirFS("./templates")
