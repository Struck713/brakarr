package crawler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Struck713/brakarr/internal/database"
	"github.com/Struck713/brakarr/internal/media"
)

type env struct {
	t       *testing.T
	db      *database.Database
	root    string
	crawler *Crawler
	onNew   int
}

func newEnv(t *testing.T, skipHardlinked bool) *env {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{t: t, db: db, root: t.TempDir()}
	e.crawler = New(db, Options{
		Root:           e.root,
		Extensions:     []string{".mkv"},
		SkipHardlinked: skipHardlinked,
		OnNew:          func() { e.onNew++ },
	})
	// The file's content is its hash, so a moved file keeps its identity.
	e.crawler.hash = func(path string) ([]byte, error) { return os.ReadFile(path) }
	e.crawler.probe = func(path string, hash []byte) (*media.Media, error) {
		return &media.Media{Hash: hash, Path: path, Name: filepath.Base(path), Format: "AVC", BitDepth: "8", Size: 10, Duration: 1}, nil
	}
	e.crawler.now = func() time.Time { return time.Now().Add(time.Hour) }
	return e
}

func (e *env) write(name, content string) string {
	e.t.Helper()
	path := filepath.Join(e.root, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return path
}

// seed inserts a row for content stored at path, in the given status.
func (e *env) seed(content, path string, status media.Status) {
	e.t.Helper()
	m := &media.Media{Hash: []byte(content), Path: path, Name: filepath.Base(path), Format: "AVC", BitDepth: "8", Size: 10, Duration: 1, Status: status}
	if _, err := e.db.Insert(context.Background(), m); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) row(content string) *media.Media {
	e.t.Helper()
	m, err := e.db.Get(context.Background(), []byte(content))
	if err != nil || m == nil {
		e.t.Fatalf("row %q: %v, %v", content, m, err)
	}
	return m
}

func TestRunReturnsAfterOneScanWhenIntervalIsZero(t *testing.T) {
	e := newEnv(t, true)
	done := make(chan struct{})
	go func() { e.crawler.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestMovedFileUpdatesPath(t *testing.T) {
	e := newEnv(t, true)
	newPath := e.write("new.mkv", "aaa")
	e.seed("aaa", filepath.Join(e.root, "gone.mkv"), media.StatusComplete)
	e.crawler.scan(context.Background())
	if got := e.row("aaa"); got.Path != newPath || got.Name != "new.mkv" || got.Status != media.StatusComplete {
		t.Errorf("row = %+v", got)
	}
	if e.onNew != 0 {
		t.Errorf("OnNew fired %d times for a complete row", e.onNew)
	}
}

func TestMovedFailedFileIsRequeued(t *testing.T) {
	e := newEnv(t, true)
	newPath := e.write("new.mkv", "aaa")
	e.seed("aaa", filepath.Join(e.root, "gone.mkv"), media.StatusFailed)
	e.crawler.scan(context.Background())
	if got := e.row("aaa"); got.Path != newPath || got.Status != media.StatusPending || got.Error != "" {
		t.Errorf("row = %+v", got)
	}
	if e.onNew != 1 {
		t.Errorf("OnNew fired %d times, want 1", e.onNew)
	}
}

func TestSecondCopyDoesNotMoveRow(t *testing.T) {
	e := newEnv(t, true)
	old := e.write("old.mkv", "aaa")
	e.write("copy.mkv", "aaa")
	e.seed("aaa", old, media.StatusFailed)
	e.crawler.scan(context.Background())
	if got := e.row("aaa"); got.Path != old || got.Status != media.StatusFailed {
		t.Errorf("row = %+v", got)
	}
}

func TestRunningRowIsUntouched(t *testing.T) {
	e := newEnv(t, true)
	e.write("new.mkv", "aaa")
	old := filepath.Join(e.root, "gone.mkv")
	e.seed("aaa", old, media.StatusPending)
	if _, err := e.db.ClaimNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.crawler.scan(context.Background())
	if got := e.row("aaa"); got.Path != old || got.Status != media.StatusRunning {
		t.Errorf("row = %+v", got)
	}
}

func TestDeferredRowRequeuedWhenLinkIsGone(t *testing.T) {
	e := newEnv(t, true)
	path := e.write("a.mkv", "aaa")
	e.seed("aaa", path, media.StatusDeferred)
	e.crawler.scan(context.Background())
	if got := e.row("aaa"); got.Status != media.StatusPending {
		t.Errorf("status = %s, want pending", got.Status)
	}
	if e.onNew != 1 {
		t.Errorf("OnNew fired %d times, want 1", e.onNew)
	}
}

func TestDeferredRowStaysWhileHardlinked(t *testing.T) {
	e := newEnv(t, true)
	path := e.write("a.mkv", "aaa")
	if err := os.Link(path, filepath.Join(t.TempDir(), "seed.mkv")); err != nil {
		t.Skip("hardlinks not supported:", err)
	}
	e.seed("aaa", path, media.StatusDeferred)
	e.crawler.scan(context.Background())
	if got := e.row("aaa"); got.Status != media.StatusDeferred {
		t.Errorf("status = %s, want deferred", got.Status)
	}
	if e.onNew != 0 {
		t.Errorf("OnNew fired %d times, want 0", e.onNew)
	}
}
