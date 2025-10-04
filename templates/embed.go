//go:build !dev

package templates

import "embed"

//go:embed *.ghtml partials/*.ghtml
var FS embed.FS
