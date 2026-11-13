package database

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"path/filepath"
	"testing"

	"github.com/Struck713/brakarr/internal/media"
)

func openTest(t *testing.T) *Database {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// insert adds a file that has been through one transcode ending in status, with the given VMAF.
func insert(t *testing.T, db *Database, name, format string, status media.Status, kind media.SkipKind, vmaf *float64) []byte {
	t.Helper()
	ctx := context.Background()
	hash := []byte(name)
	m := &media.Media{Hash: hash, Name: name, Path: "/media/" + name, Format: format, BitDepth: "8", Size: 100}
	if _, err := db.Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	var err error
	switch status {
	case media.StatusSkipped:
		err = db.Skip(ctx, hash, kind, "skipped", 90, vmaf)
	case media.StatusFailed:
		err = db.Fail(ctx, hash, "failed")
	case media.StatusPending:
	default:
		err = db.SetStatus(ctx, hash, status)
	}
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func score(v float64) *float64 { return &v }

func status(t *testing.T, db *Database, hash []byte) media.Status {
	t.Helper()
	m, err := db.Get(context.Background(), hash)
	if err != nil || m == nil {
		t.Fatalf("get %s: %v", hash, err)
	}
	return m.Status
}

func TestRequeue(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	tests := []struct {
		name   string
		format string
		status media.Status
		kind   media.SkipKind
		want   bool
	}{
		{"failed", "AVC", media.StatusFailed, "", true},
		{"quality", "AVC", media.StatusSkipped, media.SkipQuality, true},
		{"not-smaller", "AVC", media.StatusSkipped, media.SkipNotSmaller, true},
		{"hevc", "HEVC", media.StatusSkipped, media.SkipHEVC, false},
		{"complete", "HEVC", media.StatusComplete, "", false},
		{"running", "AVC", media.StatusRunning, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := insert(t, db, tt.name, tt.format, tt.status, tt.kind, nil)
			got, err := db.Requeue(ctx, hash)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Requeue = %v, want %v", got, tt.want)
			}
			if got != media.Requeueable(tt.status, tt.kind) {
				t.Fatalf("database and media.Requeueable disagree")
			}
			if tt.want && status(t, db, hash) != media.StatusPending {
				t.Fatalf("status = %s, want pending", status(t, db, hash))
			}
		})
	}
}

func TestPassingQualitySkips(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	low := insert(t, db, "low", "AVC", media.StatusSkipped, media.SkipQuality, score(85))
	near := insert(t, db, "near", "AVC", media.StatusSkipped, media.SkipQuality, score(92.5))
	insert(t, db, "bigger", "AVC", media.StatusSkipped, media.SkipNotSmaller, nil)
	insert(t, db, "failed", "AVC", media.StatusFailed, "", nil)

	// A later failed attempt means the old score no longer describes the file.
	retried := insert(t, db, "retried", "AVC", media.StatusSkipped, media.SkipQuality, score(93))
	if err := db.Fail(ctx, retried, "later failure"); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		min  float64
		want int
	}{{94, 0}, {92.5, 1}, {90, 1}, {80, 2}, {0, 2}} {
		n, err := db.CountPassingQualitySkips(ctx, tt.min)
		if err != nil {
			t.Fatal(err)
		}
		if n != tt.want {
			t.Errorf("CountPassingQualitySkips(%v) = %d, want %d", tt.min, n, tt.want)
		}
	}

	n, err := db.RequeuePassingQualitySkips(ctx, 90)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || status(t, db, near) != media.StatusPending || status(t, db, low) != media.StatusSkipped {
		t.Fatalf("requeued %d; near=%s low=%s", n, status(t, db, near), status(t, db, low))
	}
}

// Migration 5 fills in skip_kind for files skipped before it existed.
func TestMigrateSkipKind(t *testing.T) {
	file := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite3", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"001-initial.sql", "002-files-change.sql", "003-deferred.sql", "004-history-table.sql"} {
		b, err := migrationFS.ReadFile(path.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := raw.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	rows := map[string]string{
		"hevc":        "already HEVC",
		"quality":     "quality too low (VMAF 90.1 < 94)",
		"not_smaller": "output not smaller (10 >= 9 bytes)",
	}
	for kind, reason := range rows {
		_, err := raw.Exec(`INSERT INTO files (hash, name, path, format, bit_depth, duration, status, error)
			VALUES (?, ?, '/x', 'AVC', '8', 0, 'skipped', ?)`, []byte(kind), kind, reason)
		if err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	db, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for kind := range rows {
		m, err := db.Get(context.Background(), []byte(kind))
		if err != nil {
			t.Fatal(err)
		}
		if string(m.SkipKind) != kind {
			t.Errorf("%s: skip_kind = %q", kind, m.SkipKind)
		}
	}
}
