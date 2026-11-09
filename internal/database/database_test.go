package database

import (
	"database/sql"
	"io/fs"
	"path"
	"path/filepath"
	"testing"
)

func TestMigrationRequeuesNonHEVCComplete(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db")
	raw, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)

	// Build the version 2 schema using the first two embedded migrations.
	for _, name := range []string{"001-initial.sql", "002-files-change.sql"} {
		b, err := fs.ReadFile(migrationFS, path.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(b)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO files (hash, name, path, format, bit_depth, size, duration, status) VALUES
		(x'01', 'a.mkv', '/a.mkv', 'AVC', '8', 10, 1, 'complete'),
		(x'02', 'b.mkv', '/b.mkv', 'HEVC', '8', 10, 1, 'complete'),
		(x'03', 'c.mkv', '/c.mkv', 'AVC', '8', 10, 1, 'failed')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(raw); err != nil {
		t.Fatal(err)
	}

	want := map[byte]string{1: "pending", 2: "complete", 3: "failed"}
	for id, status := range want {
		var got string
		if err := raw.QueryRow(`SELECT status FROM files WHERE hash = ?`, []byte{id}).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != status {
			t.Errorf("hash %d: status = %q, want %q", id, got, status)
		}
	}
	if _, err := raw.Exec(`UPDATE files SET status = 'deferred' WHERE hash = x'01'`); err != nil {
		t.Errorf("deferred status rejected: %v", err)
	}
}
