package permit

import (
	"database/sql"
	"edev/db"
	"errors"
	"strings"
)

// nullIfEmpty converts empty string to nil, keeping non-empty as string pointer.
// This matches the convention: empty scope in Go means "global" (NULL in DB).
// scopeValue returns nil for empty scope (global) or the scope string.
func scopeValue(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// checkOnce checks a single (tenant, user, resource, scopeRef) combination
// without fallback logic. scopeRef == nil means "scope IS NULL".
func checkOnce(tenantID, userID int64, resource string, scopeRef *string) (bool, error) {
	// Query pattern:
	// We look at permits, joining group_members to account for
	// group-based permissions in the same tenant.
	//
	// Conditions:
	//  - tenant_id match
	//  - resource match
	//  - scope match (or IS NULL)
	//  - allowed = 1
	//  - subject is either:
	//      * direct user permit (p.user_id = :user_id)
	//      * group-based (gm.user_id = :user_id)
	//
	// Using EXISTS semantics via LIMIT 1.

	base := `SELECT 1
	FROM permits p
	LEFT JOIN group_members gm
	  ON gm.tenant_id = p.tenant_id
	 AND gm.group_id  = p.group_id
	 AND gm.user_id   = ?    -- 1 user_id (group membership)
	WHERE p.tenant_id = ?    -- 2 tenant_id
	  AND p.resource  = ?    -- 3 resource token
	  AND p.allowed   = 1
	  AND (p.user_id = ? OR gm.user_id IS NOT NULL) -- 4 direct user or via group`

	args := []any{
		userID,   // 1
		tenantID, // 2
		resource, // 3
		userID,   // 4
	}

	if scopeRef == nil {
		base += "  AND p.scope IS NULL\n" // global permission (NULL scope)
	} else {
		base += "  AND p.scope = ?    -- 5 scope\n"
		args = append(args, *scopeRef) // 5
	}

	base += "LIMIT 1"

	var dummy int
	err := db.Storage.QueryRow(base, args...).Scan(&dummy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Check returns true if the given user has the specified resource permission
// in the given tenant, considering:
//  1. scoped permission (resource + scope)
//  2. fallback to global permission (resource + scope IS NULL)
//
// It considers both direct user permits and group-derived permits.
//
// Scope rules:
//   - If scope is empty string, only GLOBAL (scope IS NULL) is checked.
//   - If scope is non-empty, it first checks that scope, then global.
func Check(tenantID, userID int64, resource, scope string) (bool, error) {
	if tenantID <= 0 || userID <= 0 || resource == "" {
		return false, errors.New("permissions: invalid tenant, user or resource")
	}

	// 1) If scope is non-empty, check scoped permission first.
	if scope != "" {
		allowed, err := checkOnce(tenantID, userID, resource, &scope)
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}

	// 2) Fallback global (scope IS NULL).
	allowed, err := checkOnce(tenantID, userID, resource, nil)
	if err != nil {
		return false, err
	}
	return allowed, nil
}

// GrantUser sets or creates a permit for a user in a tenant.
// It sets allowed = true (1). If a row already exists, it is updated.
func GrantUser(tenantID, userID int64, resource, scope string) error {
	if tenantID <= 0 || userID <= 0 || resource == "" {
		return errors.New("permissions: invalid tenant, user or resource")
	}

	// NOTE: Cannot use ON CONFLICT due to partial UNIQUE index (WHERE user_id IS NOT NULL).
	// Strategy: attempt INSERT; on UNIQUE failure, run UPDATE to set allowed=1.
	const insertQ = `
INSERT INTO permits (
	tenant_id,  -- 1
	user_id,    -- 2
	resource,   -- 3
	scope,      -- 4 (NULL => global)
	allowed
) VALUES (
	?,  -- 1 tenant_id
	?,  -- 2 user_id
	?,  -- 3 resource token
	?,  -- 4 scope (may be NULL)
	1   -- allowed flag
);`
	if err := db.Storage.Exec(insertQ,
		tenantID,          // 1
		userID,            // 2
		resource,          // 3
		scopeValue(scope), // 4
	); err != nil {
		// If it's a uniqueness violation, fallback to UPDATE.
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "constraint") {
			const updateQ = `
UPDATE permits
   SET allowed = 1
 WHERE tenant_id = ?    -- 1
   AND user_id   = ?    -- 2
   AND resource  = ?    -- 3
   AND scope IS ?       -- 4 (NULL/global match)
;`
			return db.Storage.Exec(updateQ,
				tenantID,          // 1
				userID,            // 2
				resource,          // 3
				scopeValue(scope), // 4
			)
		}
		return err
	}
	return nil
}

// RevokeUser disables a user permit by setting allowed = 0.
// The row remains in the table, which makes toggling cheap.
func RevokeUser(tenantID, userID int64, resource, scope string) error {
	if tenantID <= 0 || userID <= 0 || resource == "" {
		return errors.New("permissions: invalid tenant, user or resource")
	}

	const q = `
UPDATE permits
   SET allowed = 0
 WHERE tenant_id = ?    -- 1
   AND user_id   = ?    -- 2
   AND resource  = ?    -- 3
   AND scope IS ?       -- 4 (NULL/global match)
;`
	// "scope IS ?" handles NULL vs non-NULL.
	err := db.Storage.Exec(q,
		tenantID,          // 1
		userID,            // 2
		resource,          // 3
		scopeValue(scope), // 4
	)
	return err
}

// GrantGroup sets or creates a permit for a group in a tenant.
// It sets allowed = true (1). If a row already exists, it is updated.
func GrantGroup(tenantID, groupID int64, resource, scope string) error {
	if tenantID <= 0 || groupID <= 0 || resource == "" {
		return errors.New("permissions: invalid tenant, group or resource")
	}

	const insertQ = `
INSERT INTO permits (
	tenant_id,  -- 1
	group_id,   -- 2
	resource,   -- 3
	scope,      -- 4
	allowed
) VALUES (
	?,  -- 1 tenant_id
	?,  -- 2 group_id
	?,  -- 3 resource token
	?,  -- 4 scope (NULL => global)
	1
);`
	if err := db.Storage.Exec(insertQ,
		tenantID,          // 1
		groupID,           // 2
		resource,          // 3
		scopeValue(scope), // 4
	); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "constraint") {
			const updateQ = `
UPDATE permits
   SET allowed = 1
 WHERE tenant_id = ?    -- 1
   AND group_id  = ?    -- 2
   AND resource  = ?    -- 3
   AND scope IS ?       -- 4
;`
			return db.Storage.Exec(updateQ,
				tenantID,          // 1
				groupID,           // 2
				resource,          // 3
				scopeValue(scope), // 4
			)
		}
		return err
	}
	return nil
}

// RevokeGroup disables a group permit by setting allowed = 0.
func RevokeGroup(tenantID, groupID int64, resource, scope string) error {
	if tenantID <= 0 || groupID <= 0 || resource == "" {
		return errors.New("permissions: invalid tenant, group or resource")
	}

	const q = `
UPDATE permits
   SET allowed = 0
 WHERE tenant_id = ?    -- 1
   AND group_id  = ?    -- 2
   AND resource  = ?    -- 3
   AND scope IS ?       -- 4
;`
	err := db.Storage.Exec(q,
		tenantID,          // 1
		groupID,           // 2
		resource,          // 3
		scopeValue(scope), // 4
	)
	return err
}

// DeleteUserPermit physically deletes a user permit row.
// This is useful for cleanup, but not required for normal toggling.
func DeleteUserPermit(tenantID, userID int64, resource, scope string) error {
	const q = `
DELETE FROM permits
 WHERE tenant_id = ?    -- 1
   AND user_id   = ?    -- 2
   AND resource  = ?    -- 3
   AND scope IS ?       -- 4
;`
	err := db.Storage.Exec(q,
		tenantID,          // 1
		userID,            // 2
		resource,          // 3
		scopeValue(scope), // 4
	)
	return err
}

// DeleteGroupPermit physically deletes a group permit row.
func DeleteGroupPermit(tenantID, groupID int64, resource, scope string) error {
	const q = `
DELETE FROM permits
 WHERE tenant_id = ?    -- 1
   AND group_id  = ?    -- 2
   AND resource  = ?    -- 3
   AND scope IS ?       -- 4
;`
	err := db.Storage.Exec(q,
		tenantID,          // 1
		groupID,           // 2
		resource,          // 3
		scopeValue(scope), // 4
	)
	return err
}

// ListUserPermits returns all effective permits for a user in a tenant,
// combining direct and group-derived entries.
// This is useful for admin screens and debugging.
type UserPermit struct {
	Resource  string
	Scope     string
	Allowed   bool
	CreatedAt string // UTC timestamp string
}

func ListUserPermits(tenantID, userID int64) ([]UserPermit, error) {
	if tenantID <= 0 || userID <= 0 {
		return nil, errors.New("permissions: invalid tenant or user")
	}

	const q = `
SELECT DISTINCT
  p.resource,                -- 1 resource token
  COALESCE(p.scope,'') AS scope, -- 2 scope (empty string for global)
  p.allowed,                 -- 3 allowed flag (0/1)
  p.created_at               -- 4 creation timestamp
FROM permits p
LEFT JOIN group_members gm
  ON gm.tenant_id = p.tenant_id
 AND gm.group_id  = p.group_id
 AND gm.user_id   = ?  -- 1 user_id for group expansion
WHERE p.tenant_id = ?  -- 2 tenant_id
  AND (p.user_id = ? OR gm.user_id IS NOT NULL) -- 3 direct or via group
ORDER BY p.resource, p.scope;
`
	rows, err := db.Storage.Query(q,
		userID,   // 1
		tenantID, // 2
		userID,   // 3
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UserPermit
	for rows.Next() {
		var up UserPermit
		var allowedInt int
		if err := rows.Scan(&up.Resource, &up.Scope, &allowedInt, &up.CreatedAt); err != nil {
			return nil, err
		}
		up.Allowed = allowedInt == 1
		out = append(out, up)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
