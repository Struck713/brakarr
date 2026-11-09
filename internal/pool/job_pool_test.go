package pool

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/transcode"
)

type fakeStore struct {
	next     *media.Media
	failed   map[string]string
	skipped  map[string]string
	deferred map[string]string
	replaced *media.Media
}

func newFakeStore(next *media.Media) *fakeStore {
	return &fakeStore{next: next, failed: map[string]string{}, skipped: map[string]string{}, deferred: map[string]string{}}
}

func (f *fakeStore) ClaimNext(context.Context) (*media.Media, error) {
	n := f.next
	f.next = nil
	return n, nil
}
func (f *fakeStore) RequeueRunning(context.Context) error { return nil }
func (f *fakeStore) Fail(_ context.Context, h []byte, r string) error {
	f.failed[string(h)] = r
	return nil
}
func (f *fakeStore) Skip(_ context.Context, h []byte, r string) error {
	f.skipped[string(h)] = r
	return nil
}
func (f *fakeStore) Defer(_ context.Context, h []byte, r string) error {
	f.deferred[string(h)] = r
	return nil
}
func (f *fakeStore) Release(context.Context, []byte) error { return nil }
func (f *fakeStore) Replace(_ context.Context, _ []byte, next *media.Media) error {
	f.replaced = next
	return nil
}

type fakeTranscoder struct {
	calls  int
	output string // path passed to the last call
	data   string
}

func (f *fakeTranscoder) Transcode(_ context.Context, _, output string, _ int, _ func(transcode.Progress)) error {
	f.calls++
	f.output = output
	return os.WriteFile(output, []byte(f.data), 0o644)
}

type fixture struct {
	pool    *Pool
	store   *fakeStore
	tc      *fakeTranscoder
	src     string
	workDir string
	item    *media.Media
}

func newFixture(t *testing.T, srcName string, opts Options) *fixture {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, srcName)
	if err := os.WriteFile(src, []byte("original original original"), 0o640); err != nil {
		t.Fatal(err)
	}
	item := &media.Media{Hash: []byte("0123456789abcdef"), Path: src, Name: srcName, Format: "AVC", BitDepth: "8", Size: int64(len("original original original"))}
	opts.WorkDir = filepath.Join(dir, "work")
	if err := os.MkdirAll(opts.WorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := newFakeStore(item)
	tc := &fakeTranscoder{data: "small"}
	p := NewPool(store, tc, opts)
	p.probe = func(path string, _ []byte) (*media.Media, error) {
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		return &media.Media{Format: "HEVC", Size: st.Size()}, nil
	}
	p.hash = func(string) ([]byte, error) { return []byte("newhash"), nil }
	return &fixture{pool: p, store: store, tc: tc, src: src, workDir: opts.WorkDir, item: item}
}

func (f *fixture) workFiles(t *testing.T) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(f.workDir)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestSuccessfulJobReplacesOriginal(t *testing.T) {
	f := newFixture(t, "movie.mkv", Options{})
	if !f.pool.runNext(context.Background(), 0) {
		t.Fatal("runNext returned false")
	}
	got, err := os.ReadFile(f.src)
	if err != nil || string(got) != "small" {
		t.Fatalf("source = %q, %v; want replaced", got, err)
	}
	if f.store.replaced == nil || string(f.store.replaced.Hash) != "newhash" {
		t.Errorf("store.Replace not called with new hash: %+v", f.store.replaced)
	}
	if n := len(f.workFiles(t)); n != 0 {
		t.Errorf("work dir has %d files, want 0", n)
	}
	if st, _ := os.Stat(f.src); st.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", st.Mode().Perm())
	}
}

func TestFailedReplaceKeepsOriginalAndRemovesOutput(t *testing.T) {
	f := newFixture(t, "movie.mkv", Options{})
	f.item.Size = 999 // makes replaceOriginal report that the source changed
	f.pool.probe = func(path string, _ []byte) (*media.Media, error) {
		return &media.Media{Format: "HEVC", Size: 5}, nil
	}
	f.pool.runNext(context.Background(), 0)
	got, _ := os.ReadFile(f.src)
	if string(got) != "original original original" {
		t.Errorf("source changed: %q", got)
	}
	if len(f.store.failed) != 1 {
		t.Errorf("failed = %v, want one entry", f.store.failed)
	}
	if n := len(f.workFiles(t)); n != 0 {
		t.Errorf("work dir has %d files, want 0", n)
	}
}

func TestCleanWorkDirOnlyRemovesHashNamedFiles(t *testing.T) {
	f := newFixture(t, "movie.mkv", Options{})
	names := map[string]bool{
		"0123456789abcdef0123456789abcdef.mkv": true,
		"0123456789abcdef0123456789abcdef.mp4": true,
		"notes.txt":                            false,
		"movie.mkv":                            false,
		"0123456789abcdef.mkv":                 false,
	}
	for name := range names {
		if err := os.WriteFile(filepath.Join(f.workDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.pool.cleanWorkDir()
	for name, removed := range names {
		_, err := os.Stat(filepath.Join(f.workDir, name))
		if removed != os.IsNotExist(err) {
			t.Errorf("%s: removed = %v, want %v", name, os.IsNotExist(err), removed)
		}
	}
}
