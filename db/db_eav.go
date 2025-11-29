package db

import (
	"database/sql"
	"errors"
	"time"

	"edev/utils"
)

// EAV Structs

type EAVWorkspace struct {
	ID          int64     `json:"id"`
	ReferenceID string    `json:"reference_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type EAVForm struct {
	ID          int64     `json:"id"`
	ReferenceID string    `json:"reference_id"`
	WorkspaceID int64     `json:"workspace_id"`
	OwnerUserID int64     `json:"owner_user_id"`
	Slug        string    `json:"slug"`
	Label       string    `json:"label"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type EAVField struct {
	ID              int64  `json:"id"`
	FormID          int64  `json:"form_id"`
	MachineName     string `json:"machine_name"`
	Label           string `json:"label"`
	Visible         bool   `json:"visible"`
	ZOrder          int    `json:"z_order"`
	IsUI            bool   `json:"is_ui"`
	UIRole          string `json:"ui_role"`
	IsSubform       bool   `json:"is_subform"`
	SubformFormID   *int64 `json:"subform_form_id"`
	PrimitiveKind   string `json:"primitive_kind"`
	UIKind          string `json:"ui_kind"`
	UIMetaJSON      string `json:"ui_meta_json"`
	Expression      string `json:"expression"`
	ExpressionOrder int    `json:"expression_order"`
	IsReadonly      bool   `json:"is_readonly"`
	Required        bool   `json:"required"`
}

type EAVRecord struct {
	ID             int64      `json:"id"`
	ReferenceID    string     `json:"reference_id"`
	FormID         int64      `json:"form_id"`
	WorkspaceID    int64      `json:"workspace_id"`
	OwnerUserID    int64      `json:"owner_user_id"`
	Status         string     `json:"status"`
	TagsJSON       string     `json:"tags_json"`
	Rev            int        `json:"rev"`
	ParentRecordID *int64     `json:"parent_record_id"`
	ParentFieldID  *int64     `json:"parent_field_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at"`
}

type EAVValue struct {
	RecordID      int64      `json:"record_id"`
	FieldID       int64      `json:"field_id"`
	FormID        int64      `json:"form_id"`
	ValueBool     *bool      `json:"value_bool"`
	ValueDatetime *time.Time `json:"value_datetime"`
	ValueFloat    *float64   `json:"value_float"`
	ValueInt      *int64     `json:"value_int"`
	ValueText     *string    `json:"value_text"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// EAVWorkspace Methods

func (s *SQLite) CreateEAVWorkspace(name, description string) (*EAVWorkspace, error) {
	refID := utils.NewOpaqueID()
	const sqlInsert = `INSERT INTO workspaces (
            reference_id,  -- 1
            name,          -- 2
            description,   -- 3
            created_at,
            updated_at
        ) VALUES (
            ?,
            ?,
            ?,
            CURRENT_TIMESTAMP,
            CURRENT_TIMESTAMP
        ) RETURNING
            id,             -- 1
            reference_id,   -- 2
            name,           -- 3
            description,    -- 4
            created_at,     -- 5
            updated_at      -- 6
        ;`

	var w EAVWorkspace
	err := s.QueryRowRW(sqlInsert,
		refID,       // 1
		name,        // 2
		description, // 3
	).Scan(
		&w.ID,          // 1
		&w.ReferenceID, // 2
		&w.Name,        // 3
		&w.Description, // 4
		&w.CreatedAt,   // 5
		&w.UpdatedAt,   // 6
	)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *SQLite) GetEAVWorkspace(id int64) (*EAVWorkspace, error) {
	const sqlSelect = `SELECT
            id,                         -- 1
            reference_id,               -- 2
            name,                       -- 3
            COALESCE(description, ''),  -- 4
            created_at,                 -- 5
            updated_at                  -- 6
        FROM workspaces
        WHERE id = ?;`

	var w EAVWorkspace
	err := s.QueryRow(sqlSelect,
		id, // 1
	).Scan(
		&w.ID,          // 1
		&w.ReferenceID, // 2
		&w.Name,        // 3
		&w.Description, // 4
		&w.CreatedAt,   // 5
		&w.UpdatedAt,   // 6
	)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *SQLite) GetEAVWorkspaceByReferenceID(refID string) (*EAVWorkspace, error) {
	const sqlSelect = `SELECT
            id,                         -- 1
            reference_id,               -- 2
            name,                       -- 3
            COALESCE(description, ''),  -- 4
            created_at,                 -- 5
            updated_at                  -- 6
        FROM workspaces
        WHERE reference_id = ?;` // 1

	var w EAVWorkspace
	err := s.QueryRow(sqlSelect,
		refID, // 1
	).Scan(
		&w.ID,          // 1
		&w.ReferenceID, // 2
		&w.Name,        // 3
		&w.Description, // 4
		&w.CreatedAt,   // 5
		&w.UpdatedAt,   // 6
	)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *SQLite) UpdateEAVWorkspace(id int64, name, description string) (*EAVWorkspace, error) {
	const sqlUpdate = `UPDATE workspaces
        SET
            name = ?,                  -- 1
            description = ?,           -- 2
            updated_at = CURRENT_TIMESTAMP
        WHERE id = ?                   -- 3
        RETURNING
            id,                        -- 1
            reference_id,              -- 2
            name,                      -- 3
            description,               -- 4
            created_at,                -- 5
            updated_at                 -- 6
        ;`

	var w EAVWorkspace
	err := s.QueryRowRW(sqlUpdate,
		name,        // 1
		description, // 2
		id,          // 3
	).Scan(
		&w.ID,          // 1
		&w.ReferenceID, // 2
		&w.Name,        // 3
		&w.Description, // 4
		&w.CreatedAt,   // 5
		&w.UpdatedAt,   // 6
	)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *SQLite) ListEAVWorkspaces() ([]EAVWorkspace, error) {
	const sqlSelect = `SELECT
            id,                         -- 1
            reference_id,               -- 2
            name,                       -- 3
            COALESCE(description, ''),  -- 4
            created_at,                 -- 5
            updated_at                  -- 6
        FROM workspaces
        ORDER BY name ASC;`

	rows, err := s.Query(sqlSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []EAVWorkspace
	for rows.Next() {
		var w EAVWorkspace
		if err := rows.Scan(
			&w.ID,          // 1
			&w.ReferenceID, // 2
			&w.Name,        // 3
			&w.Description, // 4
			&w.CreatedAt,   // 5
			&w.UpdatedAt,   // 6
		); err != nil {
			return nil, err
		}
		list = append(list, w)
	}
	return list, nil
}

// EAVForm Methods

func (s *SQLite) CreateEAVForm(workspaceID, ownerUserID int64, slug, label string) (*EAVForm, error) {
	refID := utils.NewOpaqueID()
	const sqlInsert = `INSERT INTO eav_forms (
            reference_id,  -- 1
            workspace_id,  -- 2
            owner_user_id, -- 3
            slug,          -- 4
            label,         -- 5
            active,
            created_at,
            updated_at
        ) VALUES (
            ?,
            ?,
            ?,
            ?,
            ?,
            1,
            CURRENT_TIMESTAMP,
            CURRENT_TIMESTAMP
        ) RETURNING
            id,            -- 1
            reference_id,  -- 2
            workspace_id,  -- 3
            owner_user_id, -- 4
            slug,          -- 5
            label,         -- 6
            active,        -- 7
            created_at,    -- 8
            updated_at     -- 9
        ;`

	var f EAVForm
	err := s.QueryRowRW(sqlInsert,
		refID,       // 1
		workspaceID, // 2
		ownerUserID, // 3
		slug,        // 4
		label,       // 5
	).Scan(
		&f.ID,          // 1
		&f.ReferenceID, // 2
		&f.WorkspaceID, // 3
		&f.OwnerUserID, // 4
		&f.Slug,        // 5
		&f.Label,       // 6
		&f.Active,      // 7
		&f.CreatedAt,   // 8
		&f.UpdatedAt,   // 9
	)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *SQLite) GetEAVForm(id int64) (*EAVForm, error) {
	const sqlSelect = `SELECT
            id,            -- 1
            reference_id,  -- 2
            workspace_id,  -- 3
            owner_user_id, -- 4
            slug,          -- 5
            label,         -- 6
            active,        -- 7
            created_at,    -- 8
            updated_at     -- 9
        FROM eav_forms
        WHERE id = ?;`

	var f EAVForm
	err := s.QueryRow(sqlSelect,
		id, // 1
	).Scan(
		&f.ID,          // 1
		&f.ReferenceID, // 2
		&f.WorkspaceID, // 3
		&f.OwnerUserID, // 4
		&f.Slug,        // 5
		&f.Label,       // 6
		&f.Active,      // 7
		&f.CreatedAt,   // 8
		&f.UpdatedAt,   // 9
	)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *SQLite) GetEAVFormBySlug(workspaceID int64, slug string) (*EAVForm, error) {
	const sqlSelect = `SELECT
            id,            -- 1
            reference_id,  -- 2
            workspace_id,  -- 3
            owner_user_id, -- 4
            slug,          -- 5
            label,         -- 6
            active,        -- 7
            created_at,    -- 8
            updated_at     -- 9
        FROM eav_forms
        WHERE workspace_id = ? AND slug = ?;`

	var f EAVForm
	err := s.QueryRow(sqlSelect,
		workspaceID, // 1
		slug,        // 2
	).Scan(
		&f.ID,          // 1
		&f.ReferenceID, // 2
		&f.WorkspaceID, // 3
		&f.OwnerUserID, // 4
		&f.Slug,        // 5
		&f.Label,       // 6
		&f.Active,      // 7
		&f.CreatedAt,   // 8
		&f.UpdatedAt,   // 9
	)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *SQLite) UpdateEAVForm(id int64, label string, active bool) (*EAVForm, error) {
	const sqlUpdate = `UPDATE eav_forms
        SET
            label = ?,                 -- 1
            active = ?,                -- 2
            updated_at = CURRENT_TIMESTAMP
        WHERE id = ?                   -- 3
        RETURNING
            id,            -- 1
            reference_id,  -- 2
            workspace_id,  -- 3
            owner_user_id, -- 4
            slug,          -- 5
            label,         -- 6
            active,        -- 7
            created_at,    -- 8
            updated_at     -- 9
        ;`

	var f EAVForm
	err := s.QueryRowRW(sqlUpdate,
		label,  // 1
		active, // 2
		id,     // 3
	).Scan(
		&f.ID,          // 1
		&f.ReferenceID, // 2
		&f.WorkspaceID, // 3
		&f.OwnerUserID, // 4
		&f.Slug,        // 5
		&f.Label,       // 6
		&f.Active,      // 7
		&f.CreatedAt,   // 8
		&f.UpdatedAt,   // 9
	)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *SQLite) ListEAVForms(workspaceID int64) ([]EAVForm, error) {
	const sqlSelect = `SELECT
            id,            -- 1
            reference_id,  -- 2
            workspace_id,  -- 3
            owner_user_id, -- 4
            slug,          -- 5
            label,         -- 6
            active,        -- 7
            created_at,    -- 8
            updated_at     -- 9
        FROM eav_forms
        WHERE workspace_id = ?
        ORDER BY label ASC;`

	rows, err := s.Query(sqlSelect,
		workspaceID, // 1
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []EAVForm
	for rows.Next() {
		var f EAVForm
		if err := rows.Scan(
			&f.ID,          // 1
			&f.ReferenceID, // 2
			&f.WorkspaceID, // 3
			&f.OwnerUserID, // 4
			&f.Slug,        // 5
			&f.Label,       // 6
			&f.Active,      // 7
			&f.CreatedAt,   // 8
			&f.UpdatedAt,   // 9
		); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, nil
}

// EAVField Methods

func (s *SQLite) CreateEAVField(
	formID int64,
	machineName, label string,
	zOrder int,
	isUI bool, uiRole string,
	isSubform bool, subformFormID *int64,
	primitiveKind, uiKind, uiMetaJSON string,
	expression string,
	expressionOrder int,
	isReadonly bool,
	required bool,
) (*EAVField, error) {
	const sqlInsert = `INSERT INTO eav_fields (
            form_id,             -- 1
            machine_name,        -- 2
            label,               -- 3
            visible,
            z_order,             -- 4
            is_ui,               -- 5
            ui_role,             -- 6
            is_subform,          -- 7
            subform_form_id,     -- 8
            primitive_kind,      -- 9
            ui_kind,             -- 10
            ui_meta_json,        -- 11
            expression,          -- 12
            expression_order,    -- 13
            is_readonly,         -- 14
            required,            -- 15
            created_at,
            updated_at
        ) VALUES (
            ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
            CURRENT_TIMESTAMP,
            CURRENT_TIMESTAMP
        ) RETURNING
            id,                        -- 1
            form_id,                   -- 2
            machine_name,              -- 3
            label,                     -- 4
            visible,                   -- 5
            z_order,                   -- 6
            is_ui,                     -- 7
            ui_role,                   -- 8
            is_subform,                -- 9
            subform_form_id,           -- 10
            primitive_kind,            -- 11
            ui_kind,                   -- 12
            ui_meta_json,              -- 13
            expression,                -- 14
            COALESCE(expression_order, 0) AS expression_order, -- 15
            is_readonly,               -- 16
            required                   -- 17
        ;`

	var f EAVField
	var uiRoleNull sql.NullString
	if uiRole != "" {
		uiRoleNull.String = uiRole
		uiRoleNull.Valid = true
	}

	var exprOrder any
	if expressionOrder > 0 {
		exprOrder = expressionOrder
	}

	err := s.QueryRowRW(sqlInsert,
		formID,        // 1
		machineName,   // 2
		label,         // 3
		zOrder,        // 4
		isUI,          // 5
		uiRoleNull,    // 6
		isSubform,     // 7
		subformFormID, // 8
		primitiveKind, // 9
		uiKind,        // 10
		uiMetaJSON,    // 11
		expression,    // 12
		exprOrder,     // 13
		isReadonly,    // 14
		required,      // 15
	).Scan(
		&f.ID,              // 1
		&f.FormID,          // 2
		&f.MachineName,     // 3
		&f.Label,           // 4
		&f.Visible,         // 5
		&f.ZOrder,          // 6
		&f.IsUI,            // 7
		&uiRoleNull,        // 8
		&f.IsSubform,       // 9
		&f.SubformFormID,   // 10
		&f.PrimitiveKind,   // 11
		&f.UIKind,          // 12
		&f.UIMetaJSON,      // 13
		&f.Expression,      // 14
		&f.ExpressionOrder, // 15
		&f.IsReadonly,      // 16
		&f.Required,        // 17
	)
	if err != nil {
		return nil, err
	}
	if uiRoleNull.Valid {
		f.UIRole = uiRoleNull.String
	}
	return &f, nil
}

func (s *SQLite) GetEAVField(id int64) (*EAVField, error) {
	const sqlSelect = `SELECT
            id,                              -- 1
            form_id,                         -- 2
            machine_name,                    -- 3
            label,                           -- 4
            visible,                         -- 5
            z_order,                         -- 6
            is_ui,                           -- 7
            ui_role,                         -- 8
            is_subform,                      -- 9
            subform_form_id,                 -- 10
            primitive_kind,                  -- 11
            ui_kind,                         -- 12
            ui_meta_json,                    -- 13
            COALESCE(expression, '') AS expression,            -- 14
            COALESCE(expression_order, 0) AS expression_order, -- 15
            is_readonly,                     -- 16
            required                         -- 17
        FROM eav_fields
        WHERE id = ? AND visible = 1;`

	var f EAVField
	var uiRoleNull sql.NullString
	err := s.QueryRow(sqlSelect,
		id, // 1
	).Scan(
		&f.ID,              // 1
		&f.FormID,          // 2
		&f.MachineName,     // 3
		&f.Label,           // 4
		&f.Visible,         // 5
		&f.ZOrder,          // 6
		&f.IsUI,            // 7
		&uiRoleNull,        // 8
		&f.IsSubform,       // 9
		&f.SubformFormID,   // 10
		&f.PrimitiveKind,   // 11
		&f.UIKind,          // 12
		&f.UIMetaJSON,      // 13
		&f.Expression,      // 14
		&f.ExpressionOrder, // 15
		&f.IsReadonly,      // 16
		&f.Required,        // 17
	)
	if err != nil {
		return nil, err
	}
	if uiRoleNull.Valid {
		f.UIRole = uiRoleNull.String
	}
	return &f, nil
}

func (s *SQLite) GetEAVFieldsByFormID(formID int64) ([]EAVField, error) {
	const sqlSelect = `SELECT
            id,                              -- 1
            form_id,                         -- 2
            machine_name,                    -- 3
            label,                           -- 4
            visible,                         -- 5
            z_order,                         -- 6
            is_ui,                           -- 7
            ui_role,                         -- 8
            is_subform,                      -- 9
            subform_form_id,                 -- 10
            primitive_kind,                  -- 11
            ui_kind,                         -- 12
            ui_meta_json,                    -- 13
            COALESCE(expression, '') AS expression,            -- 14
            COALESCE(expression_order, 0) AS expression_order, -- 15
            is_readonly,                     -- 16
            required                         -- 17
        FROM eav_fields
        WHERE form_id = ? AND visible = 1
        ORDER BY z_order ASC, label ASC;`

	rows, err := s.Query(sqlSelect,
		formID, // 1
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fields []EAVField
	for rows.Next() {
		var f EAVField
		var uiRoleNull sql.NullString
		if err := rows.Scan(
			&f.ID,              // 1
			&f.FormID,          // 2
			&f.MachineName,     // 3
			&f.Label,           // 4
			&f.Visible,         // 5
			&f.ZOrder,          // 6
			&f.IsUI,            // 7
			&uiRoleNull,        // 8
			&f.IsSubform,       // 9
			&f.SubformFormID,   // 10
			&f.PrimitiveKind,   // 11
			&f.UIKind,          // 12
			&f.UIMetaJSON,      // 13
			&f.Expression,      // 14
			&f.ExpressionOrder, // 15
			&f.IsReadonly,      // 16
			&f.Required,        // 17
		); err != nil {
			return nil, err
		}
		if uiRoleNull.Valid {
			f.UIRole = uiRoleNull.String
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// GetEAVFieldsByProcessingOrder returns fields sorted for expression processing.
// Fields without expressions (empty string) or without an expression_order are placed last
// while respecting z_order/label as secondary tie-breakers.
func (s *SQLite) GetEAVFieldsByProcessingOrder(formID int64) ([]EAVField, error) {
	const sqlSelect = `SELECT
            id,                              -- 1
            form_id,                         -- 2
            machine_name,                    -- 3
            label,                           -- 4
            visible,                         -- 5
            z_order,                         -- 6
            is_ui,                           -- 7
            ui_role,                         -- 8
            is_subform,                      -- 9
            subform_form_id,                 -- 10
            primitive_kind,                  -- 11
            ui_kind,                         -- 12
            ui_meta_json,                    -- 13
            COALESCE(expression, '') AS expression,            -- 14
            COALESCE(expression_order, 0) AS expression_order, -- 15
            is_readonly,                     -- 16
            required                         -- 17
        FROM eav_fields
        WHERE form_id = ? AND visible = 1
        ORDER BY
            CASE WHEN expression IS NULL OR expression = '' THEN 1 ELSE 0 END,
            CASE WHEN expression_order IS NULL OR expression_order <= 0 THEN 1 ELSE 0 END,
            expression_order ASC,
            z_order ASC,
            label ASC;`

	rows, err := s.Query(sqlSelect,
		formID, // 1
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fields []EAVField
	for rows.Next() {
		var f EAVField
		var uiRoleNull sql.NullString
		if err := rows.Scan(
			&f.ID,              // 1
			&f.FormID,          // 2
			&f.MachineName,     // 3
			&f.Label,           // 4
			&f.Visible,         // 5
			&f.ZOrder,          // 6
			&f.IsUI,            // 7
			&uiRoleNull,        // 8
			&f.IsSubform,       // 9
			&f.SubformFormID,   // 10
			&f.PrimitiveKind,   // 11
			&f.UIKind,          // 12
			&f.UIMetaJSON,      // 13
			&f.Expression,      // 14
			&f.ExpressionOrder, // 15
			&f.IsReadonly,      // 16
			&f.Required,        // 17
		); err != nil {
			return nil, err
		}
		if uiRoleNull.Valid {
			f.UIRole = uiRoleNull.String
		}
		fields = append(fields, f)
	}
	return fields, nil
}

func (s *SQLite) UpdateEAVField(
	id int64,
	label string,
	zOrder int,
	uiKind, uiMetaJSON string,
	expression string,
	expressionOrder int,
	isReadonly bool,
	required bool,
) (*EAVField, error) {
	const sqlUpdate = `UPDATE eav_fields
        SET
            label = ?,              -- 1
            z_order = ?,            -- 2
            ui_kind = ?,            -- 3
            ui_meta_json = ?,       -- 4
            expression = ?,         -- 5
            expression_order = ?,   -- 6
            is_readonly = ?,        -- 7
            required = ?,           -- 8
            updated_at = CURRENT_TIMESTAMP
        WHERE id = ?                -- 9
        RETURNING
            id,                        -- 1
            form_id,                   -- 2
            machine_name,              -- 3
            label,                     -- 4
            visible,                   -- 5
            z_order,                   -- 6
            is_ui,                     -- 7
            ui_role,                   -- 8
            is_subform,                -- 9
            subform_form_id,           -- 10
            primitive_kind,            -- 11
            ui_kind,                   -- 12
            ui_meta_json,              -- 13
            expression,                -- 14
            COALESCE(expression_order, 0) AS expression_order, -- 15
            is_readonly,               -- 16
            required                   -- 17
        ;`

	var f EAVField
	var uiRoleNull sql.NullString
	var exprOrder any
	if expressionOrder > 0 {
		exprOrder = expressionOrder
	}

	err := s.QueryRowRW(sqlUpdate,
		label,      // 1
		zOrder,     // 2
		uiKind,     // 3
		uiMetaJSON, // 4
		expression, // 5
		exprOrder,  // 6
		isReadonly, // 7
		required,   // 8
		id,         // 9
	).Scan(
		&f.ID,              // 1
		&f.FormID,          // 2
		&f.MachineName,     // 3
		&f.Label,           // 4
		&f.Visible,         // 5
		&f.ZOrder,          // 6
		&f.IsUI,            // 7
		&uiRoleNull,        // 8
		&f.IsSubform,       // 9
		&f.SubformFormID,   // 10
		&f.PrimitiveKind,   // 11
		&f.UIKind,          // 12
		&f.UIMetaJSON,      // 13
		&f.Expression,      // 14
		&f.ExpressionOrder, // 15
		&f.IsReadonly,      // 16
		&f.Required,        // 17
	)
	if err != nil {
		return nil, err
	}
	if uiRoleNull.Valid {
		f.UIRole = uiRoleNull.String
	}
	return &f, nil
}

// SoftDeleteEAVField hides the field while keeping historical data.
func (s *SQLite) SoftDeleteEAVField(id int64) error {
	const sqlUpdate = `UPDATE eav_fields
        SET visible = 0,
            updated_at = CURRENT_TIMESTAMP
        WHERE id = ? AND visible = 1;`
	return s.Exec(sqlUpdate,
		id, // 1
	)
}

// EAVRecord Methods

func (s *SQLite) CreateEAVRecord(
	formID, workspaceID, ownerUserID int64,
	status, tagsJSON string,
	parentRecordID, parentFieldID *int64,
) (*EAVRecord, error) {
	refID := utils.NewOpaqueID()
	const sqlInsert = `INSERT INTO eav_records (
            reference_id,    -- 1
            form_id,         -- 2
            workspace_id,    -- 3
            owner_user_id,   -- 4
            status,          -- 5
            tags_json,       -- 6
            rev,
            parent_record_id, -- 7
            parent_field_id, -- 8
            created_at,
            updated_at
        ) VALUES (
            ?, ?, ?, ?, ?, ?, 1, ?, ?,
            CURRENT_TIMESTAMP,
            CURRENT_TIMESTAMP
        ) RETURNING
            id,             -- 1
            reference_id,   -- 2
            form_id,        -- 3
            workspace_id,   -- 4
            owner_user_id,  -- 5
            status,         -- 6
            tags_json,      -- 7
            rev,            -- 8
            parent_record_id, -- 9
            parent_field_id, -- 10
            created_at,     -- 11
            updated_at,     -- 12
            deleted_at      -- 13
        ;`

	var r EAVRecord
	err := s.QueryRowRW(sqlInsert,
		refID,          // 1
		formID,         // 2
		workspaceID,    // 3
		ownerUserID,    // 4
		status,         // 5
		tagsJSON,       // 6
		parentRecordID, // 7
		parentFieldID,  // 8
	).Scan(
		&r.ID,             // 1
		&r.ReferenceID,    // 2
		&r.FormID,         // 3
		&r.WorkspaceID,    // 4
		&r.OwnerUserID,    // 5
		&r.Status,         // 6
		&r.TagsJSON,       // 7
		&r.Rev,            // 8
		&r.ParentRecordID, // 9
		&r.ParentFieldID,  // 10
		&r.CreatedAt,      // 11
		&r.UpdatedAt,      // 12
		&r.DeletedAt,      // 13
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *SQLite) GetEAVRecord(id int64) (*EAVRecord, error) {
	const sqlSelect = `SELECT
            id,              -- 1
            reference_id,    -- 2
            form_id,         -- 3
            workspace_id,    -- 4
            owner_user_id,   -- 5
            status,          -- 6
            tags_json,       -- 7
            rev,             -- 8
            parent_record_id,-- 9
            parent_field_id, -- 10
            created_at,      -- 11
            updated_at,      -- 12
            deleted_at       -- 13
        FROM eav_records
        WHERE id = ?;`

	var r EAVRecord
	err := s.QueryRow(sqlSelect,
		id, // 1
	).Scan(
		&r.ID,             // 1
		&r.ReferenceID,    // 2
		&r.FormID,         // 3
		&r.WorkspaceID,    // 4
		&r.OwnerUserID,    // 5
		&r.Status,         // 6
		&r.TagsJSON,       // 7
		&r.Rev,            // 8
		&r.ParentRecordID, // 9
		&r.ParentFieldID,  // 10
		&r.CreatedAt,      // 11
		&r.UpdatedAt,      // 12
		&r.DeletedAt,      // 13
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *SQLite) GetEAVRecordByReference(referenceID string) (*EAVRecord, error) {
	const sqlSelect = `SELECT
            id,              -- 1
            reference_id,    -- 2
            form_id,         -- 3
            workspace_id,    -- 4
            owner_user_id,   -- 5
            status,          -- 6
            tags_json,       -- 7
            rev,             -- 8
            parent_record_id,-- 9
            parent_field_id, -- 10
            created_at,      -- 11
            updated_at,      -- 12
            deleted_at       -- 13
        FROM eav_records
        WHERE reference_id = ? AND deleted_at IS NULL;`

	var r EAVRecord
	err := s.QueryRow(sqlSelect,
		referenceID, // 1
	).Scan(
		&r.ID,             // 1
		&r.ReferenceID,    // 2
		&r.FormID,         // 3
		&r.WorkspaceID,    // 4
		&r.OwnerUserID,    // 5
		&r.Status,         // 6
		&r.TagsJSON,       // 7
		&r.Rev,            // 8
		&r.ParentRecordID, // 9
		&r.ParentFieldID,  // 10
		&r.CreatedAt,      // 11
		&r.UpdatedAt,      // 12
		&r.DeletedAt,      // 13
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *SQLite) UpdateEAVRecord(
	id int64,
	rev int,
	status, tagsJSON string,
) (*EAVRecord, error) {
	const sqlUpdate = `UPDATE eav_records
        SET
            status = ?,          -- 1
            tags_json = ?,       -- 2
            rev = rev + 1,
            updated_at = CURRENT_TIMESTAMP
        WHERE id = ?            -- 3
            AND rev = ?         -- 4
        RETURNING
            id,             -- 1
            reference_id,   -- 2
            form_id,        -- 3
            workspace_id,   -- 4
            owner_user_id,  -- 5
            status,         -- 6
            tags_json,      -- 7
            rev,            -- 8
            parent_record_id, -- 9
            parent_field_id, -- 10
            created_at,     -- 11
            updated_at,     -- 12
            deleted_at      -- 13
        ;`

	var r EAVRecord
	err := s.QueryRowRW(sqlUpdate,
		status,   // 1
		tagsJSON, // 2
		id,       // 3
		rev,      // 4
	).Scan(
		&r.ID,             // 1
		&r.ReferenceID,    // 2
		&r.FormID,         // 3
		&r.WorkspaceID,    // 4
		&r.OwnerUserID,    // 5
		&r.Status,         // 6
		&r.TagsJSON,       // 7
		&r.Rev,            // 8
		&r.ParentRecordID, // 9
		&r.ParentFieldID,  // 10
		&r.CreatedAt,      // 11
		&r.UpdatedAt,      // 12
		&r.DeletedAt,      // 13
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Check if record exists but rev mismatch
			existing, getErr := s.GetEAVRecord(id)
			if getErr == nil && existing.Rev != rev {
				return nil, errors.New("optimistic lock conflict")
			}
		}
		return nil, err
	}
	return &r, nil
}

func (s *SQLite) SoftDeleteEAVRecord(id int64) error {
	const sqlUpdate = `UPDATE eav_records
        SET deleted_at = CURRENT_TIMESTAMP
        WHERE id = ? AND deleted_at IS NULL;`
	return s.Exec(sqlUpdate,
		id, // 1
	)
}

func (s *SQLite) ListEAVRecords(formID int64, limit, offset int) ([]EAVRecord, error) {
	const sqlSelect = `SELECT
            id,              -- 1
            reference_id,    -- 2
            form_id,         -- 3
            workspace_id,    -- 4
            owner_user_id,   -- 5
            status,          -- 6
            tags_json,       -- 7
            rev,             -- 8
            parent_record_id,-- 9
            parent_field_id, -- 10
            created_at,      -- 11
            updated_at,      -- 12
            deleted_at       -- 13
        FROM eav_records
        WHERE form_id = ? AND deleted_at IS NULL
        ORDER BY updated_at DESC
        LIMIT ? OFFSET ?;`

	rows, err := s.Query(sqlSelect,
		formID, // 1
		limit,  // 2
		offset, // 3
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []EAVRecord
	for rows.Next() {
		var r EAVRecord
		if err := rows.Scan(
			&r.ID,             // 1
			&r.ReferenceID,    // 2
			&r.FormID,         // 3
			&r.WorkspaceID,    // 4
			&r.OwnerUserID,    // 5
			&r.Status,         // 6
			&r.TagsJSON,       // 7
			&r.Rev,            // 8
			&r.ParentRecordID, // 9
			&r.ParentFieldID,  // 10
			&r.CreatedAt,      // 11
			&r.UpdatedAt,      // 12
			&r.DeletedAt,      // 13
		); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, nil
}

// EAVValue Methods

func (s *SQLite) SetEAVValue(
	recordID, fieldID, formID int64,
	valBool *bool,
	valDatetime *time.Time,
	valFloat *float64,
	valInt *int64,
	valText *string,
) error {
	const sqlUpsert = `INSERT INTO eav_values (
            record_id,      -- 1
            field_id,       -- 2
            form_id,        -- 3
            value_bool,     -- 4
            value_datetime, -- 5
            value_float,    -- 6
            value_int,      -- 7
            value_text,     -- 8
            updated_at
        ) VALUES (
            ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP
        ) ON CONFLICT(record_id, field_id) DO UPDATE SET
            value_bool = excluded.value_bool,
            value_datetime = excluded.value_datetime,
            value_float = excluded.value_float,
            value_int = excluded.value_int,
            value_text = excluded.value_text,
            updated_at = CURRENT_TIMESTAMP;`

	// value_datetime needs to be stored as string (ISO-8601) if not nil
	var valDatetimeStr *string
	if valDatetime != nil {
		s := valDatetime.Format(time.RFC3339)
		valDatetimeStr = &s
	}

	return s.Exec(sqlUpsert,
		recordID,       // 1
		fieldID,        // 2
		formID,         // 3
		valBool,        // 4
		valDatetimeStr, // 5
		valFloat,       // 6
		valInt,         // 7
		valText,        // 8
	)
}

func (s *SQLite) DeleteEAVValue(recordID, fieldID int64) error {
	const sqlDelete = `DELETE FROM eav_values
        WHERE record_id = ? AND field_id = ?;`
	return s.Exec(sqlDelete,
		recordID, // 1
		fieldID,  // 2
	)
}

func (s *SQLite) GetEAVValues(recordID int64) ([]EAVValue, error) {
	const sqlSelect = `SELECT
            record_id,      -- 1
            field_id,       -- 2
            form_id,        -- 3
            value_bool,     -- 4
            value_datetime, -- 5
            value_float,    -- 6
            value_int,      -- 7
            value_text,     -- 8
            updated_at      -- 9
        FROM eav_values
        WHERE record_id = ?;`

	rows, err := s.Query(sqlSelect,
		recordID, // 1
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []EAVValue
	for rows.Next() {
		var v EAVValue
		var valDatetimeStr *string
		if err := rows.Scan(
			&v.RecordID,     // 1
			&v.FieldID,      // 2
			&v.FormID,       // 3
			&v.ValueBool,    // 4
			&valDatetimeStr, // 5
			&v.ValueFloat,   // 6
			&v.ValueInt,     // 7
			&v.ValueText,    // 8
			&v.UpdatedAt,    // 9
		); err != nil {
			return nil, err
		}
		if valDatetimeStr != nil {
			t, err := time.Parse(time.RFC3339, *valDatetimeStr)
			if err == nil {
				v.ValueDatetime = &t
			}
		}
		values = append(values, v)
	}
	return values, nil
}
