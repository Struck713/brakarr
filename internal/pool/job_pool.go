package pool

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Struck713/brakarr/internal/database"
	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/transcode"
)

const pollInterval = 5 * time.Second

type Pool struct {
	db      *database.Database
	ffmpeg  *transcode.FFmpeg
	workers int
	workDir string
	wake    chan struct{}

	mu   sync.RWMutex
	jobs map[int]*Job // active jobs by worker ID

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewPool(db *database.Database, ffmpeg *transcode.FFmpeg, workers int, workDir string) *Pool {
	return &Pool{
		db:      db,
		ffmpeg:  ffmpeg,
		workers: workers,
		workDir: workDir,
		wake:    make(chan struct{}, 1),
		jobs:    make(map[int]*Job),
	}
}

func (p *Pool) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)

	if err := p.db.RequeueRunning(ctx); err != nil {
		cancel()
		return fmt.Errorf("requeue orphaned jobs: %w", err)
	}

	p.cancel = cancel
	for id := range p.workers {
		p.wg.Add(1)
		go p.worker(ctx, id)
	}
	return nil
}

func (p *Pool) Stop() {
	if p.cancel != nil {
		p.cancel()
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

// runNext reports whether it claimed a job.
func (p *Pool) runNext(ctx context.Context, id int) bool {
	item, err := p.db.GetJobByStatus(ctx, media.StatusPending)
	if err != nil {
		return false
	}

	if err := p.db.UpdateStatus(ctx, item, media.StatusRunning); err != nil {
		return false
	}

	job := NewJob(item)
	p.setJob(id, job)
	defer p.clearJob(id)

	output := filepath.Join(p.workDir, fmt.Sprintf("%v.mkv", hex.EncodeToString(item.Hash)))
	err = p.ffmpeg.Transcode(ctx, item.Path, output, 22, job.SetProgress)

	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	switch {
	case ctx.Err() != nil:
		os.Remove(output)
		if err := p.db.UpdateStatus(dbCtx, item, media.StatusPending); err != nil {
			log.Printf("pool: requeue %s: %v", item.Name, err)
		}
	case err != nil:
		os.Remove(output)
		log.Printf("pool: transcode %s: %v", item.Name, err)
		if err := p.db.UpdateStatus(dbCtx, item, media.StatusFailed); err != nil {
			log.Printf("pool: mark failed %s: %v", item.Name, err)
		}
	default:
		if err := p.db.UpdateStatus(dbCtx, item, media.StatusComplete); err != nil {
			log.Printf("pool: save %s: %v", item.Name, err)
			return true
		}
		log.Printf("pool: completed %s", item.Name)
	}
	return true
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
