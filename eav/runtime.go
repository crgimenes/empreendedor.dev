package eav

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"edev/config"
	"edev/db"
	"edev/log"
	"edev/permit"
	"edev/session"
	"edev/templates"
)

const (
	eavPermView   = "eav.records.view"
	eavPermCreate = "eav.records.create"
	eavPermEdit   = "eav.records.edit"
	eavPermDelete = "eav.records.delete"
)

type runtimeFormContext struct {
	Workspace *db.EAVWorkspace
	Form      *db.EAVForm
	Fields    []db.EAVField
}

type runtimeFieldState struct {
	Field        db.EAVField
	InputName    string
	Value        string
	BoolValue    bool
	Error        string
	IsUIOnly     bool
	ReadOnly     bool
	HasValue     bool
	ValuePayload runtimeValuePayload
	DisplayOnly  bool
}

func (s *runtimeFieldState) applyExistingValue(val db.EAVValue) {
	if !valueHasContent(val) {
		return
	}
	s.HasValue = true
	switch s.Field.PrimitiveKind {
	case "TEXT":
		if val.ValueText != nil {
			s.Value = *val.ValueText
		}
	case "INT":
		if val.ValueInt != nil {
			s.Value = strconv.FormatInt(*val.ValueInt, 10)
		}
	case "FLOAT":
		if val.ValueFloat != nil {
			s.Value = strconv.FormatFloat(*val.ValueFloat, 'f', -1, 64)
		}
	case "DATETIME":
		if val.ValueDatetime != nil {
			s.Value = formatDatetimeForInput(*val.ValueDatetime)
		}
	case "BOOL":
		if val.ValueBool != nil {
			s.BoolValue = *val.ValueBool
		}
	}
}

type runtimeValuePayload struct {
	BoolValue    *bool
	TimeValue    *time.Time
	FloatValue   *float64
	IntValue     *int64
	TextValue    *string
	HasPayload   bool
	ShouldDelete bool
}

type runtimeFieldDisplay struct {
	Field   db.EAVField
	Display string
}

func runtimeScopeForForm(f *db.EAVForm) string {
	return fmt.Sprintf("form:%s", f.ReferenceID)
}

func loadRuntimeContext(workspaceRef, formSlug string) (*runtimeFormContext, error) {
	ws, err := db.Storage.GetEAVWorkspaceByReferenceID(workspaceRef)
	if err != nil {
		return nil, err
	}

	form, err := db.Storage.GetEAVFormBySlug(ws.ID, formSlug)
	if err != nil {
		return nil, err
	}

	fields, err := db.Storage.GetEAVFieldsByFormID(form.ID)
	if err != nil {
		return nil, err
	}

	return &runtimeFormContext{
		Workspace: ws,
		Form:      form,
		Fields:    fields,
	}, nil
}

func (ctx *runtimeFormContext) renderableFields() []db.EAVField {
	out := make([]db.EAVField, 0, len(ctx.Fields))
	for _, f := range ctx.Fields {
		if shouldSuppressRuntimeField(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func ensureRuntimePermission(w http.ResponseWriter, form *db.EAVForm, user *db.User, resource string) bool {
	if user.ID == form.OwnerUserID {
		return true
	}

	allowed, err := permit.Check(form.WorkspaceID, user.ID, resource, runtimeScopeForForm(form))
	if err != nil {
		log.Printf("permit check error: %v", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return false
	}
	if !allowed {
		http.Error(w, "acesso negado", http.StatusForbidden)
		return false
	}
	return true
}

func parsePositiveInt(q url.Values, key string, def, max int) int {
	raw := q.Get(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	if max > 0 && v > max {
		return max
	}
	return v
}

func runtimeFieldInputName(id int64) string {
	return fmt.Sprintf("field_%d", id)
}

func submittedValue(r *http.Request, key string) (string, bool) {
	vals, ok := r.Form[key]
	if !ok || len(vals) == 0 {
		return "", false
	}
	return vals[len(vals)-1], true
}

func formatDatetimeForInput(t time.Time) string {
	return t.In(time.Local).Format("2006-01-02T15:04")
}

func parseDatetimeInput(raw string) (*time.Time, error) {
	layouts := []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04"}
	for _, layout := range layouts {
		var (
			parsed time.Time
			err    error
		)
		if layout == time.RFC3339 {
			parsed, err = time.Parse(layout, raw)
		} else {
			parsed, err = time.ParseInLocation(layout, raw, time.Local)
			parsed = parsed.UTC()
		}
		if err == nil {
			return &parsed, nil
		}
	}
	return nil, errors.New("invalid datetime")
}

func valueHasContent(v db.EAVValue) bool {
	return v.ValueBool != nil || v.ValueDatetime != nil || v.ValueFloat != nil || v.ValueInt != nil || v.ValueText != nil
}

func valuesByField(values []db.EAVValue) map[int64]db.EAVValue {
	out := make(map[int64]db.EAVValue, len(values))
	for _, v := range values {
		out[v.FieldID] = v
	}
	return out
}

func hydrateFieldStates(fields []db.EAVField, valueMap map[int64]db.EAVValue) []runtimeFieldState {
	states := make([]runtimeFieldState, 0, len(fields))
	for _, fld := range fields {
		state := runtimeFieldState{
			Field:     fld,
			InputName: runtimeFieldInputName(fld.ID),
			IsUIOnly:  fld.IsUI,
			ReadOnly:  fld.IsReadonly,
		}
		if val, ok := valueMap[fld.ID]; ok {
			state.applyExistingValue(val)
		}
		if fld.IsUI {
			states = append(states, state)
			continue
		}
		states = append(states, state)
	}
	return states
}

func processFieldSubmission(r *http.Request, fields []db.EAVField, valueMap map[int64]db.EAVValue, isEdit bool) ([]runtimeFieldState, bool) {
	states := make([]runtimeFieldState, 0, len(fields))
	hasErr := false

	for _, fld := range fields {
		state := runtimeFieldState{
			Field:     fld,
			InputName: runtimeFieldInputName(fld.ID),
			IsUIOnly:  fld.IsUI,
			ReadOnly:  fld.IsReadonly,
		}
		if val, ok := valueMap[fld.ID]; ok {
			state.applyExistingValue(val)
		}
		if fld.IsUI {
			states = append(states, state)
			continue
		}
		if state.ReadOnly {
			raw, submitted := submittedValue(r, state.InputName)
			if submitted {
				raw = strings.TrimSpace(raw)
				if !state.HasValue {
					if raw != "" {
						state.Error = "Campo somente leitura"
						hasErr = true
					}
				} else {
					switch fld.PrimitiveKind {
					case "BOOL":
						attempt := raw == "on" || raw == "1" || strings.EqualFold(raw, "true")
						if attempt != state.BoolValue {
							state.Error = "Campo somente leitura"
							hasErr = true
						}
					default:
						if raw != state.Value {
							state.Error = "Campo somente leitura"
							hasErr = true
						}
					}
				}
			}
			states = append(states, state)
			continue
		}

		raw := strings.TrimSpace(r.FormValue(state.InputName))
		switch fld.PrimitiveKind {
		case "BOOL":
			checked := raw == "on" || raw == "1" || strings.EqualFold(raw, "true")
			state.BoolValue = checked
			state.ValuePayload.BoolValue = &checked
			state.ValuePayload.HasPayload = true
			if !checked && fld.Required {
				state.Error = "Campo obrigatório"
				hasErr = true
			}
		case "TEXT":
			state.Value = raw
			if raw == "" {
				if fld.Required {
					state.Error = "Campo obrigatório"
					hasErr = true
				} else if isEdit {
					if val, ok := valueMap[fld.ID]; ok && valueHasContent(val) {
						state.ValuePayload.ShouldDelete = true
					}
				}
			} else {
				txt := raw
				state.ValuePayload.TextValue = &txt
				state.ValuePayload.HasPayload = true
			}
		case "INT":
			state.Value = raw
			if raw == "" {
				if fld.Required {
					state.Error = "Campo obrigatório"
					hasErr = true
				} else if isEdit {
					if val, ok := valueMap[fld.ID]; ok && valueHasContent(val) {
						state.ValuePayload.ShouldDelete = true
					}
				}
			} else {
				parsed, err := strconv.ParseInt(raw, 10, 64)
				if err != nil {
					state.Error = "Valor inválido"
					hasErr = true
				} else {
					state.ValuePayload.IntValue = &parsed
					state.ValuePayload.HasPayload = true
				}
			}
		case "FLOAT":
			state.Value = raw
			if raw == "" {
				if fld.Required {
					state.Error = "Campo obrigatório"
					hasErr = true
				} else if isEdit {
					if val, ok := valueMap[fld.ID]; ok && valueHasContent(val) {
						state.ValuePayload.ShouldDelete = true
					}
				}
			} else {
				parsed, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					state.Error = "Valor inválido"
					hasErr = true
				} else {
					state.ValuePayload.FloatValue = &parsed
					state.ValuePayload.HasPayload = true
				}
			}
		case "DATETIME":
			state.Value = raw
			if raw == "" {
				if fld.Required {
					state.Error = "Campo obrigatório"
					hasErr = true
				} else if isEdit {
					if val, ok := valueMap[fld.ID]; ok && valueHasContent(val) {
						state.ValuePayload.ShouldDelete = true
					}
				}
			} else {
				parsed, err := parseDatetimeInput(raw)
				if err != nil {
					state.Error = "Data inválida"
					hasErr = true
				} else {
					state.ValuePayload.TimeValue = parsed
					state.ValuePayload.HasPayload = true
				}
			}
		default:
			state.Error = "Tipo não suportado"
			hasErr = true
		}

		states = append(states, state)
	}

	return states, hasErr
}

func runtimeRecordListHandler(w http.ResponseWriter, r *http.Request) {
	u, authed := checkAuth(w, r)
	if !authed {
		return
	}

	ctx, err := loadRuntimeContext(r.PathValue("workspaceRef"), r.PathValue("formSlug"))
	if err != nil {
		log.Printf("runtime context error: %v", err)
		http.Error(w, "formulário não encontrado", http.StatusNotFound)
		return
	}

	if !ensureRuntimePermission(w, ctx.Form, u, eavPermView) {
		return
	}

	limit := parsePositiveInt(r.URL.Query(), "limit", 20, 100)
	offset := parsePositiveInt(r.URL.Query(), "offset", 0, 0)

	records, err := db.Storage.ListEAVRecords(ctx.Form.ID, limit+1, offset)
	if err != nil {
		log.Printf("list records error: %v", err)
		http.Error(w, "erro ao listar registros", http.StatusInternalServerError)
		return
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	nextOffset := offset
	if hasMore {
		nextOffset = offset + limit
	}

	message := strings.TrimSpace(r.URL.Query().Get("message"))
	if len(message) > 200 {
		message = ""
	}

	data := struct {
		Authed     bool
		User       db.User
		Config     config.Config
		Workspace  db.EAVWorkspace
		Form       db.EAVForm
		Records    []db.EAVRecord
		Limit      int
		Offset     int
		NextOffset int
		HasMore    bool
		Message    string
	}{
		Authed:     true,
		User:       *u,
		Config:     *config.Cfg,
		Workspace:  *ctx.Workspace,
		Form:       *ctx.Form,
		Records:    records,
		Limit:      limit,
		Offset:     offset,
		NextOffset: nextOffset,
		HasMore:    hasMore,
		Message:    message,
	}

	if err := templates.ExecuteTemplate(w, "eav_runtime_record_list.go.tmpl", data); err != nil {
		log.Printf("template error: %v", err)
		http.Error(w, "erro ao renderizar", http.StatusInternalServerError)
	}
}

func runtimeRecordCreateHandler(w http.ResponseWriter, r *http.Request) {
	u, authed := checkAuth(w, r)
	if !authed {
		return
	}

	ctx, err := loadRuntimeContext(r.PathValue("workspaceRef"), r.PathValue("formSlug"))
	if err != nil {
		http.Error(w, "formulário não encontrado", http.StatusNotFound)
		return
	}

	if !ctx.Form.Active {
		http.Error(w, "formulário inativo", http.StatusForbidden)
		return
	}

	if !ensureRuntimePermission(w, ctx.Form, u, eavPermCreate) {
		return
	}

	renderForm := func(states []runtimeFieldState, statusCode int, formErrors []string) {
		if statusCode != 0 {
			w.WriteHeader(statusCode)
		}
		data := struct {
			Authed    bool
			User      db.User
			Config    config.Config
			Workspace db.EAVWorkspace
			Form      db.EAVForm
			Fields    []runtimeFieldState
			Csrf      string
			ActionURL string
			BackURL   string
			Mode      string
			Errors    []string
			Record    *db.EAVRecord
			DeleteURL string
		}{
			Authed:    true,
			User:      *u,
			Config:    *config.Cfg,
			Workspace: *ctx.Workspace,
			Form:      *ctx.Form,
			Fields:    states,
			Csrf:      session.GenerateCSRFToken(w, r),
			ActionURL: r.URL.Path,
			BackURL:   fmt.Sprintf("/eav/workspaces/%s/forms/%s/records", ctx.Workspace.ReferenceID, ctx.Form.Slug),
			Mode:      "create",
			Errors:    formErrors,
			DeleteURL: "",
		}
		if err := templates.ExecuteTemplate(w, "eav_runtime_record_form.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
			http.Error(w, "erro ao renderizar", http.StatusInternalServerError)
		}
	}

	fields := ctx.renderableFields()
	if r.Method != http.MethodPost {
		renderForm(hydrateFieldStates(fields, nil), 0, nil)
		return
	}

	if err := r.ParseForm(); err != nil {
		log.Printf("parse form error: %v", err)
		http.Error(w, "dados inválidos", http.StatusBadRequest)
		return
	}

	if !session.ValidateCSRF(r) {
		http.Error(w, "CSRF inválido", http.StatusForbidden)
		return
	}

	states, hasErr := processFieldSubmission(r, fields, nil, false)
	if hasErr {
		renderForm(states, http.StatusBadRequest, []string{"Verifique os campos destacados."})
		return
	}

	record, err := db.Storage.CreateEAVRecord(ctx.Form.ID, ctx.Workspace.ID, u.ID, "active", "{}", nil, nil)
	if err != nil {
		log.Printf("create record error: %v", err)
		http.Error(w, "erro ao criar registro", http.StatusInternalServerError)
		return
	}

	if err := persistFieldStates(record.ID, ctx.Form.ID, states); err != nil {
		log.Printf("persist values error: %v", err)
		http.Error(w, "erro ao salvar valores", http.StatusInternalServerError)
		return
	}

	redirectURL := fmt.Sprintf("/eav/workspaces/%s/forms/%s/records?message=%s",
		ctx.Workspace.ReferenceID,
		ctx.Form.Slug,
		url.QueryEscape("Registro criado com sucesso."),
	)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func persistFieldStates(recordID, formID int64, states []runtimeFieldState) error {
	for _, st := range states {
		if st.IsUIOnly || st.ReadOnly {
			continue
		}
		payload := st.ValuePayload
		if payload.ShouldDelete {
			if err := db.Storage.DeleteEAVValue(recordID, st.Field.ID); err != nil {
				return err
			}
			continue
		}
		if !payload.HasPayload {
			continue
		}
		if err := db.Storage.SetEAVValue(recordID, st.Field.ID, formID, payload.BoolValue, payload.TimeValue, payload.FloatValue, payload.IntValue, payload.TextValue); err != nil {
			return err
		}
	}
	return nil
}

func runtimeRecordViewHandler(w http.ResponseWriter, r *http.Request) {
	u, authed := checkAuth(w, r)
	if !authed {
		return
	}

	ctx, err := loadRuntimeContext(r.PathValue("workspaceRef"), r.PathValue("formSlug"))
	if err != nil {
		http.Error(w, "formulário não encontrado", http.StatusNotFound)
		return
	}

	if !ensureRuntimePermission(w, ctx.Form, u, eavPermView) {
		return
	}

	record, err := db.Storage.GetEAVRecordByReference(r.PathValue("recordRef"))
	if err != nil || record.FormID != ctx.Form.ID {
		http.Error(w, "registro não encontrado", http.StatusNotFound)
		return
	}

	values, err := db.Storage.GetEAVValues(record.ID)
	if err != nil {
		log.Printf("get values error: %v", err)
		http.Error(w, "erro ao carregar registro", http.StatusInternalServerError)
		return
	}

	valueMap := valuesByField(values)
	displays := make([]runtimeFieldDisplay, 0, len(ctx.Fields))
	for _, fld := range ctx.renderableFields() {
		if fld.IsUI {
			continue
		}
		display := "-"
		if val, ok := valueMap[fld.ID]; ok {
			switch fld.PrimitiveKind {
			case "TEXT":
				if val.ValueText != nil {
					display = *val.ValueText
				}
			case "INT":
				if val.ValueInt != nil {
					display = strconv.FormatInt(*val.ValueInt, 10)
				}
			case "FLOAT":
				if val.ValueFloat != nil {
					display = strconv.FormatFloat(*val.ValueFloat, 'f', -1, 64)
				}
			case "DATETIME":
				if val.ValueDatetime != nil {
					display = val.ValueDatetime.In(time.Local).Format("02/01/2006 15:04")
				}
			case "BOOL":
				if val.ValueBool != nil {
					if *val.ValueBool {
						display = "Sim"
					} else {
						display = "Não"
					}
				}
			}
		}
		displays = append(displays, runtimeFieldDisplay{Field: fld, Display: display})
	}

	message := strings.TrimSpace(r.URL.Query().Get("message"))
	if len(message) > 200 {
		message = ""
	}

	data := struct {
		Authed    bool
		User      db.User
		Config    config.Config
		Workspace db.EAVWorkspace
		Form      db.EAVForm
		Record    db.EAVRecord
		Values    []runtimeFieldDisplay
		Message   string
		Csrf      string
	}{
		Authed:    true,
		User:      *u,
		Config:    *config.Cfg,
		Workspace: *ctx.Workspace,
		Form:      *ctx.Form,
		Record:    *record,
		Values:    displays,
		Message:   message,
		Csrf:      session.GenerateCSRFToken(w, r),
	}

	if err := templates.ExecuteTemplate(w, "eav_runtime_record_view.go.tmpl", data); err != nil {
		log.Printf("template error: %v", err)
		http.Error(w, "erro ao renderizar", http.StatusInternalServerError)
	}
}

func runtimeRecordEditHandler(w http.ResponseWriter, r *http.Request) {
	u, authed := checkAuth(w, r)
	if !authed {
		return
	}

	ctx, err := loadRuntimeContext(r.PathValue("workspaceRef"), r.PathValue("formSlug"))
	if err != nil {
		http.Error(w, "formulário não encontrado", http.StatusNotFound)
		return
	}

	if !ensureRuntimePermission(w, ctx.Form, u, eavPermEdit) {
		return
	}

	record, err := db.Storage.GetEAVRecordByReference(r.PathValue("recordRef"))
	if err != nil || record.FormID != ctx.Form.ID {
		http.Error(w, "registro não encontrado", http.StatusNotFound)
		return
	}

	values, err := db.Storage.GetEAVValues(record.ID)
	if err != nil {
		log.Printf("get values error: %v", err)
		http.Error(w, "erro ao carregar registro", http.StatusInternalServerError)
		return
	}
	valueMap := valuesByField(values)
	fields := ctx.renderableFields()

	renderForm := func(states []runtimeFieldState, statusCode int, formErrors []string) {
		if statusCode != 0 {
			w.WriteHeader(statusCode)
		}
		data := struct {
			Authed    bool
			User      db.User
			Config    config.Config
			Workspace db.EAVWorkspace
			Form      db.EAVForm
			Fields    []runtimeFieldState
			Csrf      string
			ActionURL string
			BackURL   string
			Mode      string
			Errors    []string
			Record    *db.EAVRecord
			DeleteURL string
		}{
			Authed:    true,
			User:      *u,
			Config:    *config.Cfg,
			Workspace: *ctx.Workspace,
			Form:      *ctx.Form,
			Fields:    states,
			Csrf:      session.GenerateCSRFToken(w, r),
			ActionURL: r.URL.Path,
			BackURL:   fmt.Sprintf("/eav/workspaces/%s/forms/%s/records/%s", ctx.Workspace.ReferenceID, ctx.Form.Slug, record.ReferenceID),
			Mode:      "edit",
			Errors:    formErrors,
			Record:    record,
			DeleteURL: fmt.Sprintf("/eav/workspaces/%s/forms/%s/records/%s/delete", ctx.Workspace.ReferenceID, ctx.Form.Slug, record.ReferenceID),
		}
		if err := templates.ExecuteTemplate(w, "eav_runtime_record_form.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
			http.Error(w, "erro ao renderizar", http.StatusInternalServerError)
		}
	}

	if r.Method != http.MethodPost {
		renderForm(hydrateFieldStates(fields, valueMap), 0, nil)
		return
	}

	if err := r.ParseForm(); err != nil {
		log.Printf("parse form error: %v", err)
		http.Error(w, "dados inválidos", http.StatusBadRequest)
		return
	}

	if !session.ValidateCSRF(r) {
		http.Error(w, "CSRF inválido", http.StatusForbidden)
		return
	}

	states, hasErr := processFieldSubmission(r, fields, valueMap, true)
	if hasErr {
		renderForm(states, http.StatusBadRequest, []string{"Verifique os campos destacados."})
		return
	}

	rev, err := strconv.Atoi(r.FormValue("record_rev"))
	if err != nil {
		renderForm(states, http.StatusBadRequest, []string{"Versão do registro inválida."})
		return
	}
	updated, err := db.Storage.UpdateEAVRecord(record.ID, rev, record.Status, record.TagsJSON)
	if err != nil {
		if strings.Contains(err.Error(), "optimistic") {
			renderForm(states, http.StatusConflict, []string{"O registro foi alterado por outra pessoa. Atualize a página."})
			return
		}
		log.Printf("update record error: %v", err)
		http.Error(w, "erro ao atualizar registro", http.StatusInternalServerError)
		return
	}
	record = updated

	if err := persistFieldStates(record.ID, ctx.Form.ID, states); err != nil {
		log.Printf("persist values error: %v", err)
		http.Error(w, "erro ao salvar valores", http.StatusInternalServerError)
		return
	}

	redirectURL := fmt.Sprintf("/eav/workspaces/%s/forms/%s/records/%s?message=%s",
		ctx.Workspace.ReferenceID,
		ctx.Form.Slug,
		record.ReferenceID,
		url.QueryEscape("Registro atualizado."),
	)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func runtimeRecordDeleteHandler(w http.ResponseWriter, r *http.Request) {
	u, authed := checkAuth(w, r)
	if !authed {
		return
	}

	ctx, err := loadRuntimeContext(r.PathValue("workspaceRef"), r.PathValue("formSlug"))
	if err != nil {
		http.Error(w, "formulário não encontrado", http.StatusNotFound)
		return
	}

	if !ensureRuntimePermission(w, ctx.Form, u, eavPermDelete) {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "dados inválidos", http.StatusBadRequest)
		return
	}

	if !session.ValidateCSRF(r) {
		http.Error(w, "CSRF inválido", http.StatusForbidden)
		return
	}

	if r.FormValue("confirm_delete") != "on" {
		http.Error(w, "confirmação obrigatória", http.StatusBadRequest)
		return
	}

	record, err := db.Storage.GetEAVRecordByReference(r.PathValue("recordRef"))
	if err != nil || record.FormID != ctx.Form.ID {
		http.Error(w, "registro não encontrado", http.StatusNotFound)
		return
	}

	if err := db.Storage.SoftDeleteEAVRecord(record.ID); err != nil {
		log.Printf("delete record error: %v", err)
		http.Error(w, "erro ao excluir registro", http.StatusInternalServerError)
		return
	}

	redirectURL := fmt.Sprintf("/eav/workspaces/%s/forms/%s/records?message=%s",
		ctx.Workspace.ReferenceID,
		ctx.Form.Slug,
		url.QueryEscape("Registro excluído com sucesso."),
	)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}
