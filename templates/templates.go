package templates

import (
	"html/template"
	"log"
	"strings"
)

func loadTemplates() *template.Template {
	// Register small helper functions for templates
	funcMap := template.FuncMap{
		"split": strings.Split,
		"trim":  strings.TrimSpace,
	}

	base := template.New("").Funcs(funcMap)
	tpl, err := base.ParseFS(
		filesystem,
		"*.go.tmpl",
		"partials/*.go.tmpl",
	)
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}

	return tpl
}
