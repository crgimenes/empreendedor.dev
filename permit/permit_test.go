package permit

import (
	"path/filepath"
	"testing"

	"edev/config"
	"edev/db"
	"edev/migration"
)

// initPermitsTestDB initializes a SQLite database and runs migrations.
// It uses a temp file path to avoid the special ":memory:" handling.
func initPermitsTestDB(t *testing.T) {
	t.Helper()

	tmp := t.TempDir()
	config.Cfg.DBFile = filepath.Join(tmp, "permits_test.db")

	var err error
	db.Storage, err = db.New()
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	if err := migration.Run(); err != nil {
		t.Fatalf("migration.Run: %v", err)
	}
}

func TestCheckScopedThenGlobal(t *testing.T) {

	initPermitsTestDB(t)

	if err := db.Storage.Exec(`INSERT INTO edev_core_tenants (name) VALUES ('t1')`); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := db.Storage.Exec(`INSERT INTO edev_core_users (email, created_at, updated_at) VALUES ('u@example.com', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var tenantID, userID int64
	if err := db.Storage.QueryRow(`SELECT id FROM edev_core_tenants WHERE name='t1'`).Scan(&tenantID); err != nil {
		t.Fatalf("select tenant id: %v", err)
	}
	if err := db.Storage.QueryRow(`SELECT id FROM edev_core_users WHERE email='u@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("select user id: %v", err)
	}

	if err := GrantUser(tenantID, userID, "menu.admin", ""); err != nil {
		t.Fatalf("GrantUser global: %v", err)
	}
	if err := GrantUser(tenantID, userID, "mesa.participar", "mesa:t1"); err != nil {
		t.Fatalf("GrantUser scoped: %v", err)
	}

	ok, err := Check(tenantID, userID, "mesa.participar", "mesa:t1")
	if err != nil {
		t.Fatalf("Check scoped: %v", err)
	}
	if !ok {
		t.Fatalf("expected scoped permit to be allowed")
	}

	ok, err = Check(tenantID, userID, "menu.admin", "")
	if err != nil {
		t.Fatalf("Check global: %v", err)
	}
	if !ok {
		t.Fatalf("expected global permit to be allowed")
	}
}

func TestGrantAndRevokeUser(t *testing.T) {

	initPermitsTestDB(t)

	if err := db.Storage.Exec(`INSERT INTO edev_core_tenants (name) VALUES ('t2')`); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := db.Storage.Exec(`INSERT INTO edev_core_users (email, created_at, updated_at) VALUES ('user2@example.com', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var tenantID, userID int64
	if err := db.Storage.QueryRow(`SELECT id FROM edev_core_tenants WHERE name='t2'`).Scan(&tenantID); err != nil {
		t.Fatalf("select tenant id: %v", err)
	}
	if err := db.Storage.QueryRow(`SELECT id FROM edev_core_users WHERE email='user2@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("select user id: %v", err)
	}

	if err := GrantUser(tenantID, userID, "forum.postar", "forum:f1"); err != nil {
		t.Fatalf("GrantUser: %v", err)
	}

	var allowed int
	if err := db.Storage.QueryRow(`SELECT allowed FROM edev_core_permits WHERE tenant_id=? AND user_id=? AND resource='forum.postar' AND scope='forum:f1'`, tenantID, userID).Scan(&allowed); err != nil {
		t.Fatalf("select permit: %v", err)
	}
	if allowed != 1 {
		t.Fatalf("expected allowed=1, got %d", allowed)
	}

	if err := RevokeUser(tenantID, userID, "forum.postar", "forum:f1"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}
	if err := db.Storage.QueryRow(`SELECT allowed FROM edev_core_permits WHERE tenant_id=? AND user_id=? AND resource='forum.postar' AND scope='forum:f1'`, tenantID, userID).Scan(&allowed); err != nil {
		t.Fatalf("select permit after revoke: %v", err)
	}
	if allowed != 0 {
		t.Fatalf("expected allowed=0 after revoke, got %d", allowed)
	}
}

func TestGrantAndRevokeGroup(t *testing.T) {

	initPermitsTestDB(t)

	if err := db.Storage.Exec(`INSERT INTO edev_core_tenants (name) VALUES ('t3')`); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := db.Storage.Exec(`INSERT INTO edev_core_users (email, created_at, updated_at) VALUES ('user3@example.com', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var tenantID, userID int64
	if err := db.Storage.QueryRow(`SELECT id FROM edev_core_tenants WHERE name='t3'`).Scan(&tenantID); err != nil {
		t.Fatalf("select tenant id: %v", err)
	}
	if err := db.Storage.QueryRow(`SELECT id FROM edev_core_users WHERE email='user3@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("select user id: %v", err)
	}

	if err := db.Storage.Exec(`INSERT INTO edev_core_groups (
		tenant_id, -- 1
		name
	) VALUES (
		?,       -- 1 tenant_id
		'g1'
	)`, tenantID); err != nil {
		t.Fatalf("insert group: %v", err)
	}

	var groupID int64
	if err := db.Storage.QueryRow(`SELECT id
	FROM edev_core_groups
	WHERE tenant_id = ?  -- 1
	  AND name = 'g1'`, tenantID).Scan(&groupID); err != nil {
		t.Fatalf("select group id: %v", err)
	}

	if err := db.Storage.Exec(`INSERT INTO edev_core_group_members (
		tenant_id, -- 1
		group_id,  -- 2
		user_id    -- 3
	) VALUES (
		?, -- 1 tenant_id
		?, -- 2 group_id
		?  -- 3 user_id
	)`, tenantID, groupID, userID); err != nil {
		t.Fatalf("insert group member: %v", err)
	}

	if err := GrantGroup(tenantID, groupID, "menu.admin", ""); err != nil {
		t.Fatalf("GrantGroup: %v", err)
	}

	ok, err := Check(tenantID, userID, "menu.admin", "")
	if err != nil {
		t.Fatalf("Check via group: %v", err)
	}
	if !ok {
		t.Fatalf("expected user to inherit group permission")
	}

	if err := RevokeGroup(tenantID, groupID, "menu.admin", ""); err != nil {
		t.Fatalf("RevokeGroup: %v", err)
	}

	ok, err = Check(tenantID, userID, "menu.admin", "")
	if err != nil {
		t.Fatalf("Check after revoke: %v", err)
	}
	if ok {
		t.Fatalf("expected user to lose group permission after revoke")
	}
}

func TestListUserPermits(t *testing.T) {

	initPermitsTestDB(t)

	if err := db.Storage.Exec(`INSERT INTO edev_core_tenants (name) VALUES ('t4')`); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := db.Storage.Exec(`INSERT INTO edev_core_users (email, created_at, updated_at) VALUES ('user4@example.com', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var tenantID, userID int64
	if err := db.Storage.QueryRow(`SELECT id FROM edev_core_tenants WHERE name='t4'`).Scan(&tenantID); err != nil {
		t.Fatalf("select tenant id: %v", err)
	}
	if err := db.Storage.QueryRow(`SELECT id FROM edev_core_users WHERE email='user4@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("select user id: %v", err)
	}

	if err := db.Storage.Exec(`INSERT INTO edev_core_groups (
		tenant_id, -- 1
		name
	) VALUES (
		?,       -- 1 tenant_id
		'g2'
	)`, tenantID); err != nil {
		t.Fatalf("insert group: %v", err)
	}

	var groupID int64
	if err := db.Storage.QueryRow(`SELECT id
	FROM edev_core_groups
	WHERE tenant_id = ?  -- 1
	  AND name = 'g2'`, tenantID).Scan(&groupID); err != nil {
		t.Fatalf("select group id: %v", err)
	}

	if err := db.Storage.Exec(`INSERT INTO edev_core_group_members (
		tenant_id, -- 1
		group_id,  -- 2
		user_id    -- 3
	) VALUES (
		?, -- 1 tenant_id
		?, -- 2 group_id
		?  -- 3 user_id
	)`, tenantID, groupID, userID); err != nil {
		t.Fatalf("insert group member: %v", err)
	}

	if err := GrantUser(tenantID, userID, "mesa.participar", "mesa:t1"); err != nil {
		t.Fatalf("GrantUser: %v", err)
	}
	if err := GrantGroup(tenantID, groupID, "forum.postar", "forum:f1"); err != nil {
		t.Fatalf("GrantGroup: %v", err)
	}

	list, err := ListUserPermits(tenantID, userID)
	if err != nil {
		t.Fatalf("ListUserPermits: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 permits, got %d", len(list))
	}

	seen := map[string]bool{}
	for _, up := range list {
		seen[up.Resource] = true
		if !up.Allowed {
			t.Fatalf("expected allowed=true for resource %s", up.Resource)
		}
	}
	if !seen["mesa.participar"] || !seen["forum.postar"] {
		t.Fatalf("unexpected resources in permits: %+v", list)
	}
}
