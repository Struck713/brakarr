package pool

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/transcode"
)

const pollInterval = 5 * time.Second
const maxDurationDrift = int64(time.Second / time.Microsecond)

type Store interface {
	ClaimNext(ctx context.Context) (*media.Media, error)
	RequeueRunning(ctx context.Context) error
	Fail(ctx context.Context, hash []byte, reason string) error
	Defer(ctx context.Context, hash []byte, reason string) error
	Skip(ctx context.Context, hash []byte, reason string) error
	Release(ctx context.Context, hash []byte) error
	Replace(ctx context.Context, oldHash []byte, next *media.Media) error
}

type Transcoder interface {
	Transcode(ctx context.Context, input, output string, bitDepth int, onProgress func(transcode.Progress)) error
}

type Options struct {
	Workers        int
	WorkDir        string
	SkipHardlinked bool
	// OnReplaced is called with the path of every original that was replaced.
	OnReplaced func(path string)
}

type Pool struct {
	store Store
	tc    Transcoder
	opts  Options
	wake  chan struct{}

	// Overridable for tests.
	probe func(path string, hash []byte) (*media.Media, error)
	hash  func(path string) ([]byte, error)

	mu     sync.RWMutex
	jobs   map[int]*Job // active jobs by worker ID
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewPool(store Store, tc Transcoder, opts Options) *Pool {
	return &Pool{
		store: store,
		tc:    tc,
		opts:  opts,
		wake:  make(chan struct{}, 1),
		probe: media.Probe,
		hash:  media.Hash,
		jobs:  make(map[int]*Job),
	}
}

func (p *Pool) Start(parent context.Context) error {
	if err := os.MkdirAll(p.opts.WorkDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	if err := p.store.RequeueRunning(parent); err != nil {
		return fmt.Errorf("requeue orphaned jobs: %w", err)
	}
	p.cleanWorkDir()

	ctx, cancel := context.WithCancel(parent)
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	for id := range p.opts.Workers {
		p.wg.Add(1)
		go p.worker(ctx, id)
	}
	return nil
}

func (p *Pool) Stop() {
	p.mu.RLock()
	cancel := p.cancel
	p.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	p.wg.Wait()
}

func (p *Pool) Notify() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Pool) Jobs() []*Job {
	p.mu.RLock()
	defer p.mu.RUnlock()

	jobs := make([]*Job, 0, len(p.jobs))
	for _, job := range p.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

func (p *Pool) worker(ctx context.Context, id int) {
	defer p.wg.Done()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		for ctx.Err() == nil && p.runNext(ctx, id) {
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.wake:
		}
	}
}

func (p *Pool) runNext(ctx context.Context, id int) bool {
	item, err := p.store.ClaimNext(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("pool: claim job", "error", err)
		}
		return false
	}
	if item == nil {
		return false
	}

	job := NewJob(item)
	p.setJob(id, job)
	defer p.clearJob(id)

	if p.opts.SkipHardlinked {
		if st, err := os.Stat(item.Path); err == nil {
			if links := media.LinkCount(st); links > 1 {
				reason := fmt.Sprintf("hardlinked (%d links), waiting until only one remains", links)
				if err := p.store.Defer(context.WithoutCancel(ctx), item.Hash, reason); err != nil {
					slog.Error("pool: defer", "name", item.Name, "error", err)
				}
				return true
			}
		}
		// A stat error falls through: ffmpeg reports it and the job fails normally.
	}

	ext := strings.ToLower(filepath.Ext(item.Path))
	output := filepath.Join(p.opts.WorkDir, hex.EncodeToString(item.Hash)+ext)
	os.Remove(output)

	bitDepth, _ := strconv.Atoi(item.BitDepth) // unknown = treat as 8-bit
	err = p.tc.Transcode(ctx, item.Path, output, bitDepth, job.SetProgress)

	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	switch {
	case ctx.Err() != nil:
		os.Remove(output)
		if err := p.store.Release(dbCtx, item.Hash); err != nil {
			slog.Error("pool: requeue after shutdown", "name", item.Name, "error", err)
		}
	case err != nil:
		os.Remove(output)
		p.fail(dbCtx, item, fmt.Errorf("transcode: %w", err))
	default:
		p.finish(dbCtx, item, output)
	}
	return true
}

func (p *Pool) fail(ctx context.Context, item *media.Media, cause error) {
	slog.Error("pool: job failed", "name", item.Name, "error", cause)
	if err := p.store.Fail(ctx, item.Hash, cause.Error()); err != nil {
		slog.Error("pool: mark failed", "name", item.Name, "error", err)
	}
}

func (p *Pool) finish(ctx context.Context, item *media.Media, output string) {
	next, skipReason, err := p.verify(item, output)
	switch {
	case err != nil:
		os.Remove(output)
		p.fail(ctx, item, fmt.Errorf("verify output: %w", err))
		return
	case skipReason != "":
		os.Remove(output)
		slog.Info("pool: skipped", "name", item.Name, "reason", skipReason)
		if err := p.store.Skip(ctx, item.Hash, skipReason); err != nil {
			slog.Error("pool: mark skipped", "name", item.Name, "error", err)
		}
		return
	}

	if err := p.replaceOriginal(ctx, item, output, next); err != nil {
		os.Remove(output)
		p.fail(ctx, item, fmt.Errorf("replace original: %w", err))
		return
	}
	slog.Info("pool: replaced original", "name", item.Name, "before", item.Size, "after", next.Size)
}

func (p *Pool) verify(item *media.Media, output string) (next *media.Media, skipReason string, err error) {
	next, err = p.probe(output, nil)
	if err != nil {
		return nil, "", err
	}
	if !next.IsHEVC() {
		return nil, "", fmt.Errorf("output is %q, expected HEVC", next.Format)
	}
	if item.Duration > 0 {
		drift := next.Duration - item.Duration
		if drift < 0 {
			drift = -drift
		}
		if next.Duration == 0 || drift > maxDurationDrift {
			return nil, "", fmt.Errorf("duration mismatch: source %dus, output %dus", item.Duration, next.Duration)
		}
	}
	if next.Size <= 0 {
		return nil, "", errors.New("output is empty")
	}
	if item.Size > 0 && next.Size >= item.Size {
		return nil, fmt.Sprintf("output not smaller (%d >= %d bytes)", next.Size, item.Size), nil
	}
	return next, "", nil
}

func (p *Pool) replaceOriginal(ctx context.Context, item *media.Media, output string, next *media.Media) error {
	st, err := os.Stat(item.Path)
	if err != nil {
		return err
	}
	if item.Size > 0 && st.Size() != item.Size {
		return errors.New("source changed while transcoding")
	}
	if err := moveFile(output, item.Path); err != nil {
		return err
	}
	if p.opts.OnReplaced != nil {
		p.opts.OnReplaced(item.Path)
	}

	hash, err := p.hash(item.Path)
	if err != nil {
		return fmt.Errorf("original replaced but re-hash failed: %w", err)
	}
	next.Hash, next.Path, next.Name = hash, item.Path, item.Name
	if err := p.store.Replace(ctx, item.Hash, next); err != nil {
		return fmt.Errorf("original replaced but database update failed: %w", err)
	}
	return nil
}

func moveFile(src, dst string) error {
	if st, err := os.Stat(dst); err == nil {
		defer func() {
			media.CopyOwner(st, dst)
			os.Chmod(dst, st.Mode().Perm())
		}()
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	tmp := dst + ".brakarr.tmp"
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Remove(src)
}

func (p *Pool) setJob(id int, job *Job) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.jobs[id] = job
}

func (p *Pool) clearJob(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.jobs, id)
}

// Only ever deletes Brakarr's own hash-named outputs, in case WORK_DIR is shared.
var workFile = regexp.MustCompile(`^[0-9a-f]{32}\.(mkv|mp4)$`)

func (p *Pool) cleanWorkDir() {
	entries, err := os.ReadDir(p.opts.WorkDir)
	if err != nil {
		slog.Warn("pool: read work dir", "error", err)
		return
	}
	for _, e := range entries {
		if e.Type().IsRegular() && workFile.MatchString(e.Name()) {
			os.Remove(filepath.Join(p.opts.WorkDir, e.Name()))
		}
	}
}
