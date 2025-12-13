package main

import (
	"bytes"
	"testing"

	"github.com/crgimenes/devengine/templates"
	edevTemplates "github.com/crgimenes/empreendedor.dev/templates"
)

func TestEngineTemplatesStillWork(t *testing.T) {
	// Configure app templates
	templates.SetAppTemplatesFS(edevTemplates.EmbeddedFS)

	// Try to execute an engine template (should still work)
	var buf bytes.Buffer
	err := templates.ExecuteTemplate(&buf, "login.go.tmpl", map[string]any{
		"Authed": false,
		"User":   nil,
		"Error":  "",
	})

	if err != nil {
		t.Fatalf("failed to execute engine template: %v", err)
	}

	result := buf.String()
	if len(result) == 0 {
		t.Fatal("engine template rendered empty content")
	}

	t.Log("✅ Engine template still works after app templates configured")
}

func TestAppTemplatesLoading(t *testing.T) {
	// Configure app templates
	templates.SetAppTemplatesFS(edevTemplates.EmbeddedFS)

	// Try to execute the app test template
	var buf bytes.Buffer
	err := templates.ExecuteTemplate(&buf, "app_test.go.tmpl", map[string]string{
		"Date": "2025-12-09",
	})

	if err != nil {
		t.Fatalf("failed to execute app template: %v", err)
	}

	result := buf.String()
	if len(result) == 0 {
		t.Fatal("app template rendered empty content")
	}

	if !bytes.Contains([]byte(result), []byte("empreendedor.dev")) {
		t.Fatal("app template should contain empreendedor.dev text")
	}

	t.Log("✅ App template loaded and rendered successfully")
}
