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

func get(t *testing.T, db *Database, hash []byte) *media.Media {
	t.Helper()
	m, err := db.Get(context.Background(), hash)
	if err != nil || m == nil {
		t.Fatalf("get %s: %v (found=%v)", hash, err, m != nil)
	}
	return m
}

func status(t *testing.T, db *Database, hash []byte) media.Status {
	t.Helper()
	return get(t, db, hash).Status
}

// TestInsert checks that Insert:
//   - defaults an empty status to pending,
//   - reports true for a new row and false for a duplicate hash,
//   - leaves the existing row untouched on a duplicate (INSERT OR IGNORE).
func TestInsert(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	m := &media.Media{Hash: []byte("a"), Name: "a.mkv", Path: "/media/a.mkv", Format: "AVC", BitDepth: "8", Size: 10}

	if ok, err := db.Insert(ctx, m); err != nil || !ok {
		t.Fatalf("first Insert = %v, %v; want true", ok, err)
	}
	dup := *m
	dup.Name = "changed.mkv"
	if ok, err := db.Insert(ctx, &dup); err != nil || ok {
		t.Fatalf("duplicate Insert = %v, %v; want false", ok, err)
	}
	got := get(t, db, m.Hash)
	if got.Status != media.StatusPending || got.Name != "a.mkv" {
		t.Fatalf("got status=%s name=%s; want pending a.mkv", got.Status, got.Name)
	}
}

// TestGetMissing checks that Get returns nil (not an error) for an unknown hash.
func TestGetMissing(t *testing.T) {
	db := openTest(t)
	m, err := db.Get(context.Background(), []byte("nope"))
	if err != nil || m != nil {
		t.Fatalf("Get = %v, %v; want nil, nil", m, err)
	}
}

// TestClaimNext checks the queue's claim behaviour:
//   - only pending rows are claimed (failed/skipped/complete/deferred are ignored),
//   - rows created at the same time are claimed in name order,
//   - a claimed row becomes running, and its skip_kind and error are cleared,
//   - nil is returned once nothing is pending.
func TestClaimNext(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	insert(t, db, "b", "AVC", media.StatusPending, "", nil)
	insert(t, db, "a", "AVC", media.StatusPending, "", nil)
	insert(t, db, "failed", "AVC", media.StatusFailed, "", nil)
	insert(t, db, "skipped", "AVC", media.StatusSkipped, media.SkipQuality, nil)
	insert(t, db, "complete", "HEVC", media.StatusComplete, "", nil)
	insert(t, db, "deferred", "AVC", media.StatusDeferred, "", nil)
	if err := db.Defer(ctx, []byte("deferred"), "hardlinked"); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"a", "b"} {
		m, err := db.ClaimNext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if m == nil || m.Name != want {
			t.Fatalf("ClaimNext = %v; want %s", m, want)
		}
		if m.Status != media.StatusRunning || m.SkipKind != "" || m.Error != "" {
			t.Fatalf("claimed %s: status=%s kind=%q error=%q", want, m.Status, m.SkipKind, m.Error)
		}
		if status(t, db, m.Hash) != media.StatusRunning {
			t.Fatalf("stored status of %s is not running", want)
		}
	}
	if m, err := db.ClaimNext(ctx); err != nil || m != nil {
		t.Fatalf("ClaimNext on empty queue = %v, %v; want nil", m, err)
	}
}

// TestFailAndSkipRecordHistory checks that Fail and Skip each:
//   - set the file's status, error and (for Skip) skip_kind,
//   - append one history row with the source size, output size and VMAF
//     (Fail stores no output size or VMAF; a nil VMAF on Skip is stored as NULL).
//
// It also checks that recording history for an unknown hash is an error,
// so a typo'd hash can't silently drop a job from the history.
func TestFailAndSkipRecordHistory(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	failed := insert(t, db, "failed", "AVC", media.StatusFailed, "", nil)
	skipped := insert(t, db, "skipped", "AVC", media.StatusSkipped, media.SkipNotSmaller, nil)
	insert(t, db, "scored", "AVC", media.StatusSkipped, media.SkipQuality, new(float64(88.5)))

	if m := get(t, db, failed); m.Status != media.StatusFailed || m.Error != "failed" {
		t.Errorf("failed row: status=%s error=%q", m.Status, m.Error)
	}
	if m := get(t, db, skipped); m.Status != media.StatusSkipped || m.SkipKind != media.SkipNotSmaller {
		t.Errorf("skipped row: status=%s kind=%q", m.Status, m.SkipKind)
	}

	hist, err := db.ListHistory(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("history has %d rows; want 3", len(hist))
	}
	byName := map[string]media.History{}
	for _, h := range hist {
		byName[h.Name] = h
	}
	if h := byName["failed"]; h.Status != media.StatusFailed || h.SourceSize != 100 || h.OutputSize != nil || h.VMAF != nil {
		t.Errorf("failed history: %+v", h)
	}
	if h := byName["skipped"]; h.OutputSize == nil || *h.OutputSize != 90 || h.VMAF != nil {
		t.Errorf("skipped history: %+v", h)
	}
	if h := byName["scored"]; h.VMAF == nil || *h.VMAF != 88.5 {
		t.Errorf("scored history: %+v", h)
	}

	if err := db.Fail(ctx, []byte("unknown"), "x"); err == nil {
		t.Error("Fail on unknown hash succeeded; want error")
	}
}

// TestRequeue checks Requeue on representative file states: failed files and
// files skipped for quality or size go back to pending; already-HEVC skips,
// completed files and running files do not.
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
			if tt.want {
				m := get(t, db, hash)
				if m.Status != media.StatusPending || m.SkipKind != "" || m.Error != "" {
					t.Fatalf("after requeue: status=%s kind=%q error=%q", m.Status, m.SkipKind, m.Error)
				}
			}
		})
	}
}

// TestRequeueMatchesMediaRequeueable keeps the SQL `requeueable` filter in sync
// with media.Requeueable, which the web UI uses to decide whether to show a
// requeue button. It tries every status × skip kind × (HEVC, non-HEVC) and
// expects the database to requeue exactly when media.Requeueable says so and
// the file is not already HEVC.
func TestRequeueMatchesMediaRequeueable(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	statuses := []media.Status{media.StatusPending, media.StatusRunning, media.StatusFailed,
		media.StatusSkipped, media.StatusComplete, media.StatusDeferred}
	kinds := []media.SkipKind{media.SkipNone, media.SkipHEVC, media.SkipQuality, media.SkipNotSmaller}

	for _, st := range statuses {
		for _, kind := range kinds {
			for _, format := range []string{"AVC", "HEVC"} {
				name := fmt.Sprintf("%s-%s-%s", st, kind, format)
				t.Run(name, func(t *testing.T) {
					hash := insert(t, db, name, format, media.StatusPending, "", nil)
					if _, err := db.db.ExecContext(ctx, `UPDATE files SET status = ?, skip_kind = ? WHERE hash = ?`,
						st, kind, hash); err != nil {
						t.Fatal(err)
					}
					got, err := db.Requeue(ctx, hash)
					if err != nil {
						t.Fatal(err)
					}
					want := media.Requeueable(st, kind) && format != "HEVC"
					if got != want {
						t.Fatalf("Requeue = %v; media.Requeueable && !HEVC = %v", got, want)
					}
				})
			}
		}
	}
}

// TestPassingQualitySkips checks CountPassingQualitySkips and RequeuePassingQualitySkips:
//   - only files skipped for quality count (not-smaller skips and failures don't),
//   - the threshold is inclusive (score >= minimum passes),
//   - a minimum of 0 means the check is off, so every quality skip passes,
//   - only the file's latest history row counts: a later failure hides an old passing score,
//   - Requeue moves exactly the counted files to pending and leaves the rest skipped.
func TestPassingQualitySkips(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	low := insert(t, db, "low", "AVC", media.StatusSkipped, media.SkipQuality, new(float64(85)))
	near := insert(t, db, "near", "AVC", media.StatusSkipped, media.SkipQuality, new(float64(92.5)))
	insert(t, db, "bigger", "AVC", media.StatusSkipped, media.SkipNotSmaller, nil)
	insert(t, db, "failed", "AVC", media.StatusFailed, "", nil)

	// A later failed attempt means the old score no longer describes the file.
	retried := insert(t, db, "retried", "AVC", media.StatusSkipped, media.SkipQuality, new(float64(93)))
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

// TestDeferUndefer checks that Defer stores the reason, Undefer returns a
// deferred file to pending with the error cleared, and Undefer reports false
// (and changes nothing) for a file that isn't deferred.
func TestDeferUndefer(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	hash := insert(t, db, "linked", "AVC", media.StatusPending, "", nil)
	other := insert(t, db, "failed", "AVC", media.StatusFailed, "", nil)

	if err := db.Defer(ctx, hash, "hardlinked"); err != nil {
		t.Fatal(err)
	}
	if m := get(t, db, hash); m.Status != media.StatusDeferred || m.Error != "hardlinked" {
		t.Fatalf("after Defer: status=%s error=%q", m.Status, m.Error)
	}
	if ok, err := db.Undefer(ctx, hash); err != nil || !ok {
		t.Fatalf("Undefer = %v, %v; want true", ok, err)
	}
	if m := get(t, db, hash); m.Status != media.StatusPending || m.Error != "" {
		t.Fatalf("after Undefer: status=%s error=%q", m.Status, m.Error)
	}
	if ok, err := db.Undefer(ctx, other); err != nil || ok {
		t.Fatalf("Undefer on failed file = %v, %v; want false", ok, err)
	}
	if status(t, db, other) != media.StatusFailed {
		t.Fatal("Undefer changed a non-deferred file")
	}
}

// TestRelocate checks that Relocate updates path and name, gives a failed
// file another try (pending, error cleared), and leaves any other status and
// error alone.
func TestRelocate(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	failed := insert(t, db, "failed", "AVC", media.StatusFailed, "", nil)
	skipped := insert(t, db, "skipped", "AVC", media.StatusSkipped, media.SkipQuality, nil)

	if err := db.Relocate(ctx, failed, "/new/failed.mkv", "failed.mkv"); err != nil {
		t.Fatal(err)
	}
	if m := get(t, db, failed); m.Path != "/new/failed.mkv" || m.Name != "failed.mkv" ||
		m.Status != media.StatusPending || m.Error != "" {
		t.Fatalf("relocated failed file: %+v", m)
	}

	if err := db.Relocate(ctx, skipped, "/new/skipped.mkv", "skipped.mkv"); err != nil {
		t.Fatal(err)
	}
	if m := get(t, db, skipped); m.Path != "/new/skipped.mkv" || m.Status != media.StatusSkipped || m.Error != "skipped" {
		t.Fatalf("relocated skipped file: %+v", m)
	}
}

// TestRequeueRunning checks the startup recovery: files left running by a
// previous process go back to pending, and no other status is touched.
func TestRequeueRunning(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	running := insert(t, db, "running", "AVC", media.StatusRunning, "", nil)
	failed := insert(t, db, "failed", "AVC", media.StatusFailed, "", nil)

	if err := db.RequeueRunning(ctx); err != nil {
		t.Fatal(err)
	}
	if status(t, db, running) != media.StatusPending {
		t.Error("running file was not requeued")
	}
	if status(t, db, failed) != media.StatusFailed {
		t.Error("failed file was changed")
	}
}

// TestReplace checks swapping a file for its transcoded output:
//   - the row moves to the new hash with the output's format, size and duration, marked complete,
//   - the old hash no longer exists,
//   - the history row records the original's size as source_size and follows the
//     file to the new hash (ON UPDATE CASCADE), with the output size and VMAF,
//   - an existing row already at the new hash (e.g. the crawler saw the output first)
//     is replaced rather than causing a primary-key conflict.
func TestReplace(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	oldHash := insert(t, db, "old", "AVC", media.StatusRunning, "", nil)
	insert(t, db, "new", "HEVC", media.StatusSkipped, media.SkipHEVC, nil)

	next := &media.Media{Hash: []byte("new"), Format: "HEVC", BitDepth: "10", Size: 40, Duration: 5}
	if err := db.Replace(ctx, oldHash, next, new(float64(95))); err != nil {
		t.Fatal(err)
	}

	if m, err := db.Get(ctx, oldHash); err != nil || m != nil {
		t.Fatalf("old hash still present: %v, %v", m, err)
	}
	m := get(t, db, next.Hash)
	if m.Name != "old" || m.Format != "HEVC" || m.BitDepth != "10" || m.Size != 40 ||
		m.Duration != 5 || m.Status != media.StatusComplete || m.Error != "" {
		t.Fatalf("replaced row: %+v", m)
	}
	if n, _ := db.Count(ctx); n != 1 {
		t.Fatalf("Count = %d; want 1", n)
	}

	hist, err := db.ListHistory(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 {
		t.Fatalf("history has %d rows; want 1", len(hist))
	}
	h := hist[0]
	if string(h.Hash) != "new" || h.Status != media.StatusComplete || h.SourceSize != 100 ||
		h.OutputSize == nil || *h.OutputSize != 40 || h.VMAF == nil || *h.VMAF != 95 {
		t.Fatalf("history row: %+v", h)
	}
}

// TestListHistory checks that history is listed newest first, that limit and
// offset page through it, and that each row carries the file's *current*
// status, which is what the history tab's requeue button is based on.
func TestListHistory(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	hash := insert(t, db, "a", "AVC", media.StatusFailed, "", nil)
	if _, err := db.Requeue(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if err := db.Skip(ctx, hash, media.SkipQuality, "low", 50, new(float64(80))); err != nil {
		t.Fatal(err)
	}

	if n, err := db.CountHistory(ctx); err != nil || n != 2 {
		t.Fatalf("CountHistory = %d, %v; want 2", n, err)
	}
	page, err := db.ListHistory(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Status != media.StatusSkipped {
		t.Fatalf("first page: %+v", page)
	}
	page, err = db.ListHistory(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Status != media.StatusFailed {
		t.Fatalf("second page: %+v", page)
	}
	if page[0].FileStatus != media.StatusSkipped || page[0].FileSkipKind != media.SkipQuality {
		t.Fatalf("old history row should show current file state, got %s/%s", page[0].FileStatus, page[0].FileSkipKind)
	}
}

// TestMigrateSkipKind checks that migration 5 fills in skip_kind for files
// skipped before the column existed, by matching their stored skip reason:
// "already HEVC" → hevc, "quality too low" → quality, "output not smaller" → not_smaller.
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
