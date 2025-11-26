package db

import (
	"strings"
	"testing"
	"testing/fstest"

	"edev/config"
)

func TestFindMigrationFile(t *testing.T) {
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		version int
		want    string
		wantErr string
	}{
		{
			name: "match with dot suffix",
			fsys: fstest.MapFS{
				"001_initial.up.sql": &fstest.MapFile{Mode: 0o644, Data: []byte("-- migration")},
			},
			version: 1,
			want:    "001_initial.up.sql",
		},
		{
			name: "match with underscore suffix",
			fsys: fstest.MapFS{
				"002_update.up.sql": &fstest.MapFile{Mode: 0o644, Data: []byte("-- migration")},
			},
			version: 2,
			want:    "002_update.up.sql",
		},
		{
			name: "missing version",
			fsys: fstest.MapFS{
				"004_extra.up.sql": &fstest.MapFile{Mode: 0o644, Data: []byte("-- migration")},
			},
			version: 1,
			wantErr: "no migration file",
		},
		{
			name: "multiple matches",
			fsys: fstest.MapFS{
				"005_a.up.sql": &fstest.MapFile{Mode: 0o644, Data: []byte("-- migration")},
				"005_b.up.sql": &fstest.MapFile{Mode: 0o644, Data: []byte("-- migration")},
			},
			version: 5,
			wantErr: "multiple migration files",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findMigrationFile(tc.fsys, tc.version)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q but got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q but got %q", tc.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestRunMigrations(t *testing.T) {
	var err error
	config.Cfg.DBFile = ":memory:"

	Storage, err = New()
	if err != nil {
		t.Fatalf("Error on db: %s", err)
	}

	err = RunMigration()
	if err != nil {
		t.Fatalf("Migration error: %v", err)
	}

	// run again to test idempotency
	err = RunMigration()
	if err != nil {
		t.Fatalf("Migration error: %v", err)
	}

}
