package templates

import (
	"html/template"
	"log"
)

func loadTemplates() *template.Template {
	tpl, err := template.ParseFS(
		filesystem,
		"*.ghtml",
		"partials/*.ghtml",
	)
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}

	return tpl
}
