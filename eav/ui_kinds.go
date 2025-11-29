package eav

import (
	"strings"

	"edev/db"
)

type uiKindOption struct {
	Value  string
	Label  string
	Detail string
}

const defaultUIKind = "text_input"

var (
	fieldUIKindOptions = []uiKindOption{
		{Value: "text_input", Label: "Campo de texto", Detail: "Entrada padrão de linha única"},
		{Value: "textarea", Label: "Área de texto", Detail: "Campo multilinha"},
		{Value: "checkbox", Label: "Caixa de seleção", Detail: "Valores booleanos"},
		{Value: "select", Label: "Lista suspensa", Detail: "Seleção única"},
		{Value: "hidden", Label: "Campo oculto", Detail: "Mantém dados sem renderizar"},
	}
	allowedUIKinds           = map[string]struct{}{}
	suppressedRuntimeUIKinds = map[string]struct{}{
		"hidden": {},
	}
)

func init() {
	for _, opt := range fieldUIKindOptions {
		allowedUIKinds[opt.Value] = struct{}{}
	}
}

func uiKindSelectOptions() []uiKindOption {
	return fieldUIKindOptions
}

func isValidUIKind(value string) bool {
	if value == "" {
		return false
	}
	_, ok := allowedUIKinds[value]
	return ok
}

func shouldSuppressRuntimeField(f db.EAVField) bool {
	if f.IsUI {
		return false
	}
	if !f.Visible {
		return true
	}
	_, suppressed := suppressedRuntimeUIKinds[strings.ToLower(f.UIKind)]
	return suppressed
}
