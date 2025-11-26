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
		"first": func(s string) string {
			if len(s) == 0 {
				return ""
			}
			return string(s[0])
		},
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
	}

	base := template.New("").Funcs(funcMap)
	t, err := base.ParseFS(
		filesystem,
		"*.go.tmpl",
		"partials/*.go.tmpl",
	)
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}

	return t
}
