//go:build dev

package templates

import (
	"html/template"
	"io"
	"os"
)

var (
	filesystem = os.DirFS("./templates")

	tpl *template.Template
)

func ExecuteTemplate(w io.Writer, templateName string, data any) error {
	tpl = loadTemplates()
	return tpl.ExecuteTemplate(w, templateName, data)
}
