//go:build !dev

package templates

import (
	"embed"
	"io"
)

var (
	//go:embed *.go.tmpl partials/*.go.tmpl
	filesystem embed.FS

	tpl = loadTemplates()
)

func ExecuteTemplate(w io.Writer, templateName string, data any) error {
	return tpl.ExecuteTemplate(w, templateName, data)
}
