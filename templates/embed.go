//go:build !dev

package templates

import (
	"embed"
	"io"
)

var (
	//go:embed *.ghtml partials/*.ghtml
	filesystem embed.FS

	tpl = loadTemplates()
)

func Exec(w io.Writer, templateName string, data any) error {
	return tpl.ExecuteTemplate(w, templateName, data)
}
