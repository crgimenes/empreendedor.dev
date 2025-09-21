//go:build dev

package templates

import (
	"io/fs"
	"os"
)

// FS provides direct filesystem access to templates during development.
var FS fs.FS = os.DirFS("./templates")
