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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/transcode"
)

const pollInterval = 5 * time.Second
const maxDurationDrift = int64(time.Second / time.Microsecond)

// storeTimeout bounds each individual store update.
const storeTimeout = 30 * time.Second

// Store is the persistence the pool needs to claim and update jobs.
type Store interface {
	ClaimNext(ctx context.Context) (*media.Media, error)
	RequeueRunning(ctx context.Context) error
	Fail(ctx context.Context, hash []byte, reason string) error
	Defer(ctx context.Context, hash []byte, reason string) error
	Skip(ctx context.Context, hash []byte, kind media.SkipKind, reason string, outputSize int64, vmaf *float64) error
	Release(ctx context.Context, hash []byte) error
	Replace(ctx context.Context, oldHash []byte, next *media.Media, vmaf *float64) error
}

// Transcoder converts a media file into a new output file.
type Transcoder interface {
	Transcode(ctx context.Context, input, output string, bitDepth int, onProgress func(transcode.Progress)) error
}

// Scorer measures how visually close a transcoded file is to its source.
type Scorer interface {
	// Score returns a VMAF score from 0 to 100, comparing samples segments of the two files.
	Score(ctx context.Context, source, output string, durationUs int64, bitDepth, samples int) (float64, error)
}

// Settings are the pool options that can change while it runs. Each job uses the settings in
// effect when it was claimed, so a change never interrupts a running transcode.
type Settings struct {
	Transcoder     Transcoder
	WorkDir        string
	SkipHardlinked bool

	// Scorer and MinVMAF enable the quality check: outputs scoring below MinVMAF are
	// discarded and the original is kept. A nil Scorer or zero MinVMAF disables it.
	Scorer      Scorer
	MinVMAF     float64
	VMAFSamples int
}

// Options configures a Pool.
type Options struct {
	Workers int

	// OnReplaced is called with the path of every original that was replaced.
	OnReplaced func(path string)
}

// Pool runs transcode jobs on a set of concurrent workers.
type Pool struct {
	store    Store
	opts     Options
	settings atomic.Pointer[Settings]
	wake     chan struct{}

	probe func(path string, hash []byte) (*media.Media, error)
	hash  func(path string) ([]byte, error)

	mu     sync.RWMutex
	jobs   map[int]*Job // active jobs by worker ID
	ctx    context.Context
	cancel context.CancelFunc
	size   int             // wanted number of workers
	retire []chan struct{} // one per running worker; closed to retire it after its job
	nextID int
	wg     sync.WaitGroup
}

// NewPool creates a Pool using the given store and options. It needs settings, from Configure,
// before it can start.
func NewPool(store Store, opts Options) *Pool {
	return &Pool{
		store: store,
		opts:  opts,
		size:  opts.Workers,
		wake:  make(chan struct{}, 1),
		probe: media.Probe,
		hash:  media.Hash,
		jobs:  make(map[int]*Job),
	}
}

// Configure sets the settings used by jobs claimed from now on. Running jobs keep theirs.
func (p *Pool) Configure(s Settings) error {
	if s.Transcoder == nil {
		return errors.New("pool: no transcoder")
	}
	if err := os.MkdirAll(s.WorkDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	p.settings.Store(&s)
	return nil
}

// Start launches the workers and returns once they are running.
func (p *Pool) Start(parent context.Context) error {
	s := p.settings.Load()
	if s == nil {
		return errors.New("pool: started before it was configured")
	}
	if err := p.store.RequeueRunning(parent); err != nil {
		return fmt.Errorf("requeue orphaned jobs: %w", err)
	}
	p.cleanWorkDir(s.WorkDir)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.ctx, p.cancel = context.WithCancel(parent)
	p.resize()
	return nil
}

// Resize changes the number of workers. Extra workers finish their current job before exiting.
func (p *Pool) Resize(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n == p.size {
		return
	}
	slog.Info("pool: workers changed", "from", p.size, "to", n)
	p.size = n
	p.resize()
}

// resize starts or retires workers to match the wanted size. The caller must hold p.mu.
func (p *Pool) resize() {
	if p.ctx == nil || p.ctx.Err() != nil {
		return // not started, or stopping
	}
	for len(p.retire) < p.size {
		retire := make(chan struct{})
		p.retire = append(p.retire, retire)
		p.wg.Add(1)
		go p.worker(p.ctx, p.nextID, retire)
		p.nextID++
	}
	for len(p.retire) > p.size {
		last := len(p.retire) - 1
		close(p.retire[last])
		p.retire = p.retire[:last]
	}
}

// Stop cancels the workers and waits for them to exit.
func (p *Pool) Stop() {
	p.mu.RLock()
	cancel := p.cancel
	p.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	p.wg.Wait()
}

// Notify wakes the workers because new work may be available.
func (p *Pool) Notify() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Jobs returns the jobs currently being processed.
func (p *Pool) Jobs() []*Job {
	p.mu.RLock()
	defer p.mu.RUnlock()

	jobs := make([]*Job, 0, len(p.jobs))
	for _, job := range p.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

// worker processes jobs until the context is cancelled or it is retired.
func (p *Pool) worker(ctx context.Context, id int, retire <-chan struct{}) {
	defer p.wg.Done()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	retired := func() bool {
		select {
		case <-retire:
			return true
		default:
			return false
		}
	}
	for {
		for ctx.Err() == nil && !retired() && p.runNext(ctx, id) {
		}

		select {
		case <-ctx.Done():
			return
		case <-retire:
			return
		case <-ticker.C:
		case <-p.wake:
		}
	}
}

// runNext claims and transcodes the next pending item, reporting whether one was found.
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

	s := p.settings.Load()
	job := NewJob(item)
	p.setJob(id, job)
	defer p.clearJob(id)

	if s.SkipHardlinked {
		if st, err := os.Stat(item.Path); err == nil {
			if links := media.LinkCount(st); links > 1 {
				reason := fmt.Sprintf("hardlinked (%d links), waiting until only one remains", links)
				sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
				err := p.store.Defer(sctx, item.Hash, reason)
				cancel()
				if err != nil {
					slog.Error("pool: defer", "name", item.Name, "error", err)
				}
				return true
			}
		}
	}

	ext := strings.ToLower(filepath.Ext(item.Path))
	output := filepath.Join(s.WorkDir, hex.EncodeToString(item.Hash)+ext)
	os.Remove(output)

	bitDepth, _ := strconv.Atoi(item.BitDepth) // unknown = treat as 8-bit
	err = s.Transcoder.Transcode(ctx, item.Path, output, bitDepth, job.SetProgress)

	switch {
	case ctx.Err() != nil:
		os.Remove(output)
		p.release(ctx, item)
	case err != nil:
		os.Remove(output)
		p.fail(ctx, item, fmt.Errorf("transcode: %w", err))
	default:
		p.finish(ctx, s, item, output)
	}
	return true
}

// release returns the item to the queue after an interrupted job.
func (p *Pool) release(ctx context.Context, item *media.Media) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	if err := p.store.Release(ctx, item.Hash); err != nil {
		slog.Error("pool: requeue after shutdown", "name", item.Name, "error", err)
	}
}

// fail logs the cause and marks the item as failed.
func (p *Pool) fail(ctx context.Context, item *media.Media, cause error) {
	slog.Error("pool: job failed", "name", item.Name, "error", cause)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	if err := p.store.Fail(ctx, item.Hash, cause.Error()); err != nil {
		slog.Error("pool: mark failed", "name", item.Name, "error", err)
	}
}

// finish verifies the transcoded output and replaces the original, failing or skipping as needed.
func (p *Pool) finish(ctx context.Context, s *Settings, item *media.Media, output string) {
	next, vmaf, skip, err := p.verify(ctx, s, item, output)
	switch {
	case err != nil && ctx.Err() != nil:
		// Shutdown interrupted the quality check; try again on the next start.
		os.Remove(output)
		p.release(ctx, item)
		return
	case err != nil:
		os.Remove(output)
		p.fail(ctx, item, fmt.Errorf("verify output: %w", err))
		return
	case skip != nil:
		os.Remove(output)
		slog.Info("pool: skipped", "name", item.Name, "reason", skip.reason)
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
		defer cancel()
		if err := p.store.Skip(sctx, item.Hash, skip.kind, skip.reason, next.Size, vmaf); err != nil {
			slog.Error("pool: mark skipped", "name", item.Name, "error", err)
		}
		return
	}

	if err := p.replaceOriginal(ctx, item, output, next, vmaf); err != nil {
		os.Remove(output)
		p.fail(ctx, item, fmt.Errorf("replace original: %w", err))
		return
	}
	slog.Info("pool: replaced original", "name", item.Name, "before", item.Size, "after", next.Size)
}

// skipped is why a verified transcode was discarded.
type skipped struct {
	kind   media.SkipKind
	reason string
}

// verify probes the output and checks it is valid HEVC of matching duration, smaller than the
// source, and (when enabled) visually close enough to it. On a skip it still returns the probed
// output and any VMAF score, so the discarded transcode can be recorded.
func (p *Pool) verify(ctx context.Context, s *Settings, item *media.Media, output string) (next *media.Media, vmaf *float64, skip *skipped, err error) {
	next, err = p.probe(output, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	if !next.IsHEVC() {
		return nil, nil, nil, fmt.Errorf("output is %q, expected HEVC", next.Format)
	}
	if item.Duration > 0 {
		drift := next.Duration - item.Duration
		if drift < 0 {
			drift = -drift
		}
		if next.Duration == 0 || drift > maxDurationDrift {
			return nil, nil, nil, fmt.Errorf("duration mismatch: source %dus, output %dus", item.Duration, next.Duration)
		}
	}
	if next.Size <= 0 {
		return nil, nil, nil, errors.New("output is empty")
	}
	if item.Size > 0 && next.Size >= item.Size {
		return next, nil, &skipped{media.SkipNotSmaller,
			fmt.Sprintf("output not smaller (%d >= %d bytes)", next.Size, item.Size)}, nil
	}
	if s.Scorer != nil && s.MinVMAF > 0 {
		bitDepth, _ := strconv.Atoi(item.BitDepth)
		score, err := s.Scorer.Score(ctx, item.Path, output, next.Duration, bitDepth, s.VMAFSamples)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("measure quality: %w", err)
		}
		slog.Info("pool: quality check", "name", item.Name, "vmaf", score, "min", s.MinVMAF)
		if score < s.MinVMAF {
			return next, &score, &skipped{media.SkipQuality,
				fmt.Sprintf("quality too low (VMAF %.1f < %.0f)", score, s.MinVMAF)}, nil
		}
		vmaf = &score
	}
	return next, vmaf, nil, nil
}

// replaceOriginal swaps the source file for the transcoded output and updates the store.
func (p *Pool) replaceOriginal(ctx context.Context, item *media.Media, output string, next *media.Media, vmaf *float64) error {
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
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	if err := p.store.Replace(sctx, item.Hash, next, vmaf); err != nil {
		return fmt.Errorf("original replaced but database update failed: %w", err)
	}
	return nil
}

// moveFile moves src to dst, falling back to copying when a rename is not possible.
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

// setJob records the job a worker is running.
func (p *Pool) setJob(id int, job *Job) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.jobs[id] = job
}

// clearJob removes the job record for a worker.
func (p *Pool) clearJob(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.jobs, id)
}

// cleanWorkDir removes leftover files from the work directory.
func (p *Pool) cleanWorkDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("pool: read work dir", "error", err)
		return
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
