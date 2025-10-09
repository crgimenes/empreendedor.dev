package templates

import (
	"html/template"
	"log"
)

func loadTemplates() *template.Template {
	tpl, err := template.ParseFS(
		filesystem,
		"*.go.tmpl",
		"partials/*.go.tmpl",
	)
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}

	return tpl
}
