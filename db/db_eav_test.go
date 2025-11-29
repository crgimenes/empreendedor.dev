package db

import (
	"testing"
)

// TestEAVNullHandling verifies that NULL values in database are handled correctly
func TestEAVNullHandling(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	// Insert workspace directly with NULL description to test NULL handling
	const sqlInsertWorkspaceWithNull = `INSERT INTO workspaces (reference_id, name, description) VALUES (?, ?, NULL)`
	err := s.Exec(sqlInsertWorkspaceWithNull, "test-ref-null", "Test Workspace With NULL")
	if err != nil {
		t.Fatalf("Failed to insert workspace with NULL description: %v", err)
	}

	// Test ListEAVWorkspaces handles NULL description
	workspaces, err := s.ListEAVWorkspaces()
	if err != nil {
		t.Fatalf("ListEAVWorkspaces failed: %v", err)
	}
	if len(workspaces) == 0 {
		t.Fatal("Expected at least one workspace")
	}

	// Find our test workspace
	var found bool
	for _, ws := range workspaces {
		if ws.Name == "Test Workspace With NULL" {
			found = true
			if ws.Description != "" {
				t.Errorf("Expected empty string for NULL description, got %q", ws.Description)
			}
		}
	}
	if !found {
		t.Error("Test workspace not found in list")
	}

	// Test GetEAVWorkspaceByReferenceID handles NULL description
	ws, err := s.GetEAVWorkspaceByReferenceID("test-ref-null")
	if err != nil {
		t.Fatalf("GetEAVWorkspaceByReferenceID failed: %v", err)
	}
	if ws.Description != "" {
		t.Errorf("Expected empty string for NULL description, got %q", ws.Description)
	}

	// Test GetEAVWorkspace (by ID) handles NULL description
	ws2, err := s.GetEAVWorkspace(ws.ID)
	if err != nil {
		t.Fatalf("GetEAVWorkspace failed: %v", err)
	}
	if ws2.Description != "" {
		t.Errorf("Expected empty string for NULL description, got %q", ws2.Description)
	}
}

func TestEAVFullFlow(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	// Create a user for ownership
	const sqlInsertUser = `INSERT INTO users (username, email, enabled) VALUES (?, ?, ?)`
	if err := s.Exec(sqlInsertUser, "testowner", "owner@example.com", 1); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	// Assuming ID 1

	// 1. Workspace
	w, err := s.CreateEAVWorkspace("Test Workspace", "A test workspace")
	if err != nil {
		t.Fatalf("CreateEAVWorkspace error: %v", err)
	}

	// 2. Form
	f, err := s.CreateEAVForm(w.ID, 1, "test-form", "Test Form")
	if err != nil {
		t.Fatalf("CreateEAVForm error: %v", err)
	}

	// 3. Fields
	// Text field
	fldText, err := s.CreateEAVField(f.ID, "name", "Name", 1, true, "", false, nil, "TEXT", "text_input", "{}", "UPPER(name)", 2, true, true)
	if err != nil {
		t.Fatalf("CreateEAVField text error: %v", err)
	}

	// Int field
	fldAge, err := s.CreateEAVField(f.ID, "age", "Age", 2, true, "", false, nil, "INT", "number_input", "{}", "", 0, false, false)
	if err != nil {
		t.Fatalf("CreateEAVField int error: %v", err)
	}

	// Verify fields
	fields, err := s.GetEAVFieldsByFormID(f.ID)
	if err != nil {
		t.Fatalf("GetEAVFieldsByFormID error: %v", err)
	}
	if len(fields) != 2 {
		t.Errorf("Expected 2 fields, got %d", len(fields))
	}
	if !fields[0].IsReadonly {
		t.Errorf("expected first field to be read-only")
	}
	if fields[0].Expression == "" {
		t.Errorf("expected stored expression value")
	}
	if fields[0].ExpressionOrder != 2 {
		t.Errorf("expected expression order 2, got %d", fields[0].ExpressionOrder)
	}

	// 4. Record
	r, err := s.CreateEAVRecord(f.ID, w.ID, 1, "active", "{}", nil, nil)
	if err != nil {
		t.Fatalf("CreateEAVRecord error: %v", err)
	}
	if r.Rev != 1 {
		t.Errorf("Expected Rev 1, got %d", r.Rev)
	}

	// 5. Values
	valText := "Alice"
	err = s.SetEAVValue(r.ID, fldText.ID, f.ID, nil, nil, nil, nil, &valText)
	if err != nil {
		t.Fatalf("SetEAVValue text error: %v", err)
	}

	valInt := int64(30)
	err = s.SetEAVValue(r.ID, fldAge.ID, f.ID, nil, nil, nil, &valInt, nil)
	if err != nil {
		t.Fatalf("SetEAVValue int error: %v", err)
	}

	// Verify values
	values, err := s.GetEAVValues(r.ID)
	if err != nil {
		t.Fatalf("GetEAVValues error: %v", err)
	}
	if len(values) != 2 {
		t.Errorf("Expected 2 values, got %d", len(values))
	}

	// 6. Update Record (Optimistic Lock)
	rUpdated, err := s.UpdateEAVRecord(r.ID, r.Rev, "archived", "{}")
	if err != nil {
		t.Fatalf("UpdateEAVRecord error: %v", err)
	}
	if rUpdated.Rev != 2 {
		t.Errorf("Expected Rev 2, got %d", rUpdated.Rev)
	}
	if rUpdated.Status != "archived" {
		t.Errorf("Expected status 'archived', got %q", rUpdated.Status)
	}

	// Try update with old rev (should fail)
	_, err = s.UpdateEAVRecord(r.ID, r.Rev, "active", "{}")
	if err == nil {
		t.Error("Expected error on optimistic lock conflict, got nil")
	}
}

func TestGetEAVFieldsByFormIDOrdering(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	const sqlInsertUser = `INSERT INTO users (username, email, enabled) VALUES (?, ?, ?)`
	if err := s.Exec(sqlInsertUser, "orderowner", "orderowner@example.com", 1); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	ws, err := s.CreateEAVWorkspace("Workspace Ordering", "")
	if err != nil {
		t.Fatalf("CreateEAVWorkspace error: %v", err)
	}

	form, err := s.CreateEAVForm(ws.ID, 1, "ordering-form", "Ordering Form")
	if err != nil {
		t.Fatalf("CreateEAVForm error: %v", err)
	}

	if _, err := s.CreateEAVField(form.ID, "gamma", "Bravo", 2, false, "", false, nil, "TEXT", "text_input", "{}", "", 0, false, false); err != nil {
		t.Fatalf("CreateEAVField Bravo: %v", err)
	}
	if _, err := s.CreateEAVField(form.ID, "beta", "Charlie", 1, false, "", false, nil, "TEXT", "text_input", "{}", "calc_b", 5, false, false); err != nil {
		t.Fatalf("CreateEAVField Charlie: %v", err)
	}
	if _, err := s.CreateEAVField(form.ID, "alpha", "Alpha", 1, false, "", false, nil, "TEXT", "text_input", "{}", "calc_a", 3, false, false); err != nil {
		t.Fatalf("CreateEAVField Alpha: %v", err)
	}

	fields, err := s.GetEAVFieldsByFormID(form.ID)
	if err != nil {
		t.Fatalf("GetEAVFieldsByFormID error: %v", err)
	}

	labels := []string{"Alpha", "Charlie", "Bravo"}
	if len(fields) != len(labels) {
		t.Fatalf("expected %d fields, got %d", len(labels), len(fields))
	}
	for i, want := range labels {
		if fields[i].Label != want {
			t.Fatalf("unexpected field order at index %d: want %s, got %s", i, want, fields[i].Label)
		}
	}
}

func TestGetEAVFieldsByProcessingOrder(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	const sqlInsertUser = `INSERT INTO users (username, email, enabled) VALUES (?, ?, ?)`
	if err := s.Exec(sqlInsertUser, "procowner", "procowner@example.com", 1); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	ws, err := s.CreateEAVWorkspace("Workspace Proc", "")
	if err != nil {
		t.Fatalf("CreateEAVWorkspace error: %v", err)
	}

	form, err := s.CreateEAVForm(ws.ID, 1, "proc-form", "Proc Form")
	if err != nil {
		t.Fatalf("CreateEAVForm error: %v", err)
	}

	// Expressions with explicit order should come first in ascending expression_order).
	if _, err := s.CreateEAVField(form.ID, "expr_low", "Expr Low", 2, false, "", false, nil, "TEXT", "text_input", "{}", "low", 2, false, false); err != nil {
		t.Fatalf("CreateEAVField expr_low: %v", err)
	}
	if _, err := s.CreateEAVField(form.ID, "expr_high", "Expr High", 1, false, "", false, nil, "TEXT", "text_input", "{}", "high", 5, false, false); err != nil {
		t.Fatalf("CreateEAVField expr_high: %v", err)
	}
	// Expression without order should appear after ordered expressions but before non-expression fields?
	if _, err := s.CreateEAVField(form.ID, "expr_no_order", "Expr Default", 1, false, "", false, nil, "TEXT", "text_input", "{}", "calc", 0, false, false); err != nil {
		t.Fatalf("CreateEAVField expr_no_order: %v", err)
	}
	if _, err := s.CreateEAVField(form.ID, "plain", "Plain", 1, false, "", false, nil, "TEXT", "text_input", "{}", "", 0, false, false); err != nil {
		t.Fatalf("CreateEAVField plain: %v", err)
	}

	fields, err := s.GetEAVFieldsByProcessingOrder(form.ID)
	if err != nil {
		t.Fatalf("GetEAVFieldsByProcessingOrder error: %v", err)
	}
	got := make([]string, 0, len(fields))
	for _, fld := range fields {
		got = append(got, fld.MachineName)
	}
	want := []string{"expr_low", "expr_high", "expr_no_order", "plain"}
	if len(got) != len(want) {
		t.Fatalf("unexpected list length: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected order at index %d: got %s want %s", i, got[i], want[i])
		}
	}
}

func TestSoftDeleteEAVField(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	const sqlInsertUser = `INSERT INTO users (username, email, enabled) VALUES (?, ?, ?)`
	if err := s.Exec(sqlInsertUser, "fieldowner", "fieldowner@example.com", 1); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	ws, err := s.CreateEAVWorkspace("Workspace Field Delete", "")
	if err != nil {
		t.Fatalf("CreateEAVWorkspace error: %v", err)
	}

	form, err := s.CreateEAVForm(ws.ID, 1, "field-delete-form", "Field Delete Form")
	if err != nil {
		t.Fatalf("CreateEAVForm error: %v", err)
	}

	field, err := s.CreateEAVField(form.ID, "temp", "Temp", 1, false, "", false, nil, "TEXT", "text_input", "{}", "", 0, false, false)
	if err != nil {
		t.Fatalf("CreateEAVField error: %v", err)
	}

	if err := s.SoftDeleteEAVField(field.ID); err != nil {
		t.Fatalf("SoftDeleteEAVField error: %v", err)
	}

	if _, err := s.GetEAVField(field.ID); err == nil {
		t.Fatalf("expected error when fetching soft-deleted field")
	}

	fields, err := s.GetEAVFieldsByFormID(form.ID)
	if err != nil {
		t.Fatalf("GetEAVFieldsByFormID error: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("expected no fields after soft delete, got %d", len(fields))
	}
}

func TestGetEAVFormBySlug(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	const sqlInsertUser = `INSERT INTO users (username, email, enabled) VALUES (?, ?, ?)`
	if err := s.Exec(sqlInsertUser, "slugowner", "slug@example.com", 1); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	ws, err := s.CreateEAVWorkspace("Workspace Slug", "")
	if err != nil {
		t.Fatalf("CreateEAVWorkspace error: %v", err)
	}

	created, err := s.CreateEAVForm(ws.ID, 1, "runtime-form", "Runtime Form")
	if err != nil {
		t.Fatalf("CreateEAVForm error: %v", err)
	}

	got, err := s.GetEAVFormBySlug(ws.ID, "runtime-form")
	if err != nil {
		t.Fatalf("GetEAVFormBySlug error: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("expected form ID %d, got %d", created.ID, got.ID)
	}
}

func TestSoftDeleteRecordAndValueRemoval(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	const sqlInsertUser = `INSERT INTO users (username, email, enabled) VALUES (?, ?, ?)`
	if err := s.Exec(sqlInsertUser, "deleter", "deleter@example.com", 1); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	ws, err := s.CreateEAVWorkspace("Workspace Delete", "")
	if err != nil {
		t.Fatalf("CreateEAVWorkspace error: %v", err)
	}

	form, err := s.CreateEAVForm(ws.ID, 1, "delete-form", "Delete Form")
	if err != nil {
		t.Fatalf("CreateEAVForm error: %v", err)
	}

	field, err := s.CreateEAVField(form.ID, "title", "Title", 1, true, "", false, nil, "TEXT", "text_input", "{}", "", 0, false, false)
	if err != nil {
		t.Fatalf("CreateEAVField error: %v", err)
	}

	record, err := s.CreateEAVRecord(form.ID, ws.ID, 1, "active", "{}", nil, nil)
	if err != nil {
		t.Fatalf("CreateEAVRecord error: %v", err)
	}

	value := "Hello"
	if err := s.SetEAVValue(record.ID, field.ID, form.ID, nil, nil, nil, nil, &value); err != nil {
		t.Fatalf("SetEAVValue error: %v", err)
	}

	if err := s.DeleteEAVValue(record.ID, field.ID); err != nil {
		t.Fatalf("DeleteEAVValue error: %v", err)
	}

	values, err := s.GetEAVValues(record.ID)
	if err != nil {
		t.Fatalf("GetEAVValues error: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("expected values to be removed, got %d", len(values))
	}

	recordByRef, err := s.GetEAVRecordByReference(record.ReferenceID)
	if err != nil {
		t.Fatalf("GetEAVRecordByReference error: %v", err)
	}
	if err := s.SoftDeleteEAVRecord(recordByRef.ID); err != nil {
		t.Fatalf("SoftDeleteEAVRecord error: %v", err)
	}

	if _, err := s.GetEAVRecordByReference(record.ReferenceID); err == nil {
		t.Fatalf("expected error when fetching soft-deleted record")
	}
}
