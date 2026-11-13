package pool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/transcode"
)

// fakeStore hands out queued items and records failures.
type fakeStore struct {
	mu     sync.Mutex
	queue  []*media.Media
	failed chan string
}

func newFakeStore(names ...string) *fakeStore {
	s := &fakeStore{failed: make(chan string, len(names))}
	for _, n := range names {
		s.queue = append(s.queue, &media.Media{Hash: []byte(n), Name: n, Path: "/nonexistent/" + n})
	}
	return s
}

func (s *fakeStore) ClaimNext(context.Context) (*media.Media, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil, nil
	}
	m := s.queue[0]
	s.queue = s.queue[1:]
	return m, nil
}

func (s *fakeStore) Fail(_ context.Context, hash []byte, _ string) error {
	s.failed <- string(hash)
	return nil
}

func (s *fakeStore) RequeueRunning(context.Context) error                          { return nil }
func (s *fakeStore) Defer(context.Context, []byte, string) error                   { return nil }
func (s *fakeStore) Release(context.Context, []byte) error                         { return nil }
func (s *fakeStore) Replace(context.Context, []byte, *media.Media, *float64) error { return nil }
func (s *fakeStore) Skip(context.Context, []byte, string, int64, *float64) error {
	return nil
}

// blockingTranscoder reports each transcode it starts and blocks until released.
type blockingTranscoder struct {
	name    string
	started chan string // transcoder name + ":" + input
	release chan struct{}
}

func (b *blockingTranscoder) Transcode(ctx context.Context, input, _ string, _ int, _ func(transcode.Progress)) error {
	b.started <- b.name + ":" + input
	select {
	case <-b.release:
		return errors.New("done") // fail fast so the job finishes without touching files
	case <-ctx.Done():
		return ctx.Err()
	}
}

func wait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		panic("unreachable")
	}
}

// A job keeps the settings it was claimed with; the next job uses the new ones.
func TestConfigureAppliesToNextJob(t *testing.T) {
	store := newFakeStore("a", "b")
	started := make(chan string, 2)
	release := make(chan struct{})
	oldTC := &blockingTranscoder{name: "old", started: started, release: release}
	newTC := &blockingTranscoder{name: "new", started: started, release: release}

	p := NewPool(store, Options{Workers: 1})
	if err := p.Configure(Settings{Transcoder: oldTC, WorkDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	if got := wait(t, started); got != "old:/nonexistent/a" {
		t.Fatalf("first job = %q", got)
	}
	if err := p.Configure(Settings{Transcoder: newTC, WorkDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	if got := wait(t, store.failed); got != "a" {
		t.Fatalf("finished %q, want a", got)
	}
	if got := wait(t, started); got != "new:/nonexistent/b" {
		t.Fatalf("second job = %q", got)
	}
	release <- struct{}{}
	wait(t, store.failed)
}

// Shrinking the pool lets running jobs finish, and growing it starts new workers.
func TestResize(t *testing.T) {
	store := newFakeStore("a", "b", "c")
	started := make(chan string, 3)
	release := make(chan struct{})
	tc := &blockingTranscoder{name: "tc", started: started, release: release}

	p := NewPool(store, Options{Workers: 2})
	if err := p.Configure(Settings{Transcoder: tc, WorkDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	wait(t, started)
	wait(t, started)
	p.Resize(1)

	// Both running jobs finish normally instead of being cancelled.
	release <- struct{}{}
	release <- struct{}{}
	wait(t, store.failed)
	wait(t, store.failed)

	// One worker is left, so the third job runs.
	if got := wait(t, started); got != "tc:/nonexistent/c" {
		t.Fatalf("third job = %q", got)
	}
	if n := len(p.Jobs()); n != 1 {
		t.Fatalf("%d jobs running, want 1", n)
	}
	release <- struct{}{}
	wait(t, store.failed)

	p.Resize(3)
	p.mu.RLock()
	workers := len(p.retire)
	p.mu.RUnlock()
	if workers != 3 {
		t.Fatalf("%d workers, want 3", workers)
	}
}
