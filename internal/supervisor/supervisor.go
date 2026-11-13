// Package supervisor runs the transcoding components and applies settings changes to them
// without restarting the application.
package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Struck713/brakarr/internal/arr"
	"github.com/Struck713/brakarr/internal/config"
	"github.com/Struck713/brakarr/internal/crawler"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/transcode"
)

// Store is the persistence the pool and crawler need.
type Store interface {
	pool.Store
	crawler.Store
}

// Supervisor owns the pool, crawler and notifier, and reconfigures them whenever the settings
// are saved. Only the listen address and database path still need a restart.
type Supervisor struct {
	ctx      context.Context
	cancel   context.CancelFunc
	settings *config.Store
	db       Store
	level    *slog.LevelVar
	boot     config.Config // as loaded at startup, for settings that need a restart
	pool     *pool.Pool
	notifier *arr.Notifier

	mu         sync.Mutex
	started    bool           // the pool is running
	applied    *config.Config // last configuration applied to the running components
	disabled   error          // why transcoding is not running, nil once it is
	verified   map[string]bool
	vmafOK     bool
	stopCrawl  context.CancelFunc
	crawlDone  chan struct{}
	notifyDone chan struct{}
}

// New creates a Supervisor for the given settings and store, and subscribes it to settings
// changes. It sets level from the log level setting. Call Start to begin transcoding.
func New(ctx context.Context, settings *config.Store, db Store, level *slog.LevelVar) *Supervisor {
	ctx, cancel := context.WithCancel(ctx)
	s := &Supervisor{
		ctx:        ctx,
		cancel:     cancel,
		settings:   settings,
		db:         db,
		level:      level,
		boot:       settings.Get(),
		notifier:   arr.NewNotifier(nil, nil),
		verified:   map[string]bool{},
		notifyDone: make(chan struct{}),
	}
	s.pool = pool.NewPool(db, pool.Options{Workers: s.boot.Workers, OnReplaced: s.notifier.Replaced})
	settings.SetCheck(s.check)
	settings.Subscribe(s.reload)
	return s
}

// Pool returns the job pool, which exists even while transcoding is disabled.
func (s *Supervisor) Pool() *pool.Pool { return s.pool }

// Start applies the current settings and starts transcoding if they are usable.
func (s *Supervisor) Start() {
	go func() {
		defer close(s.notifyDone)
		s.notifier.Run(s.ctx)
	}()
	s.reload()
}

// Stop shuts down the crawler, notifier and pool and waits for them to exit. Running jobs are
// interrupted and return to the queue.
func (s *Supervisor) Stop() {
	s.cancel()
	s.mu.Lock()
	s.stopCrawler()
	s.mu.Unlock()
	s.pool.Stop()
	<-s.notifyDone
}

// Disabled returns why transcoding is not running, or nil if it is.
func (s *Supervisor) Disabled() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disabled
}

// NeedsRestart lists the saved settings that only take effect after a restart.
func (s *Supervisor) NeedsRestart() []string {
	cur := s.settings.Get()
	var out []string
	if cur.ListenAddr != s.boot.ListenAddr {
		out = append(out, "listen address")
	}
	if cur.DBPath != s.boot.DBPath {
		out = append(out, "database path")
	}
	return out
}

// check makes sure a configuration about to be saved can run on this machine, so a bad GPU
// setting is rejected instead of breaking transcoding.
func (s *Supervisor) check(cfg config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.transcoder(cfg)
	return err
}

// reload applies the current settings to the running components.
func (s *Supervisor) reload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apply(s.settings.Get())
}

// apply reconfigures everything for cfg. The caller must hold s.mu.
func (s *Supervisor) apply(cfg config.Config) {
	s.level.Set(cfg.Level())
	s.notifier.SetApps(cfg.Apps(), cfg.ArrPathMap)

	if err := s.applyTranscoding(cfg); err != nil {
		if s.started {
			slog.Error("settings not applied: keeping the previous transcoding settings", "error", err)
			return
		}
		s.disabled = err
		slog.Error("transcoding is disabled until the settings are fixed",
			"file", s.settings.Path(), "error", err)
		return
	}
	s.disabled = nil
	slog.Info("settings applied", "crawl", cfg.CrawlPath, "encoder", cfg.Encoder,
		"workers", cfg.Workers, "quality", cfg.Quality, "minVmaf", cfg.MinVMAF)
}

// applyTranscoding reconfigures the pool and crawler for cfg, starting them the first time it
// is usable. The caller must hold s.mu.
func (s *Supervisor) applyTranscoding(cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	ffmpeg, err := s.transcoder(cfg)
	if err != nil {
		return err
	}
	settings := pool.Settings{
		Transcoder:     ffmpeg,
		WorkDir:        cfg.WorkDir,
		SkipHardlinked: cfg.SkipHardlinked,
		MinVMAF:        float64(cfg.MinVMAF),
		VMAFSamples:    cfg.VMAFSamples,
	}
	if cfg.MinVMAF > 0 {
		settings.Scorer = ffmpeg
	}
	if err := s.pool.Configure(settings); err != nil {
		return err
	}
	s.pool.Resize(cfg.Workers)
	if !s.started {
		if err := s.pool.Start(s.ctx); err != nil {
			return err
		}
		s.started = true
	}
	if s.applied == nil || crawlerChanged(*s.applied, cfg) {
		s.startCrawler(cfg)
	}
	s.applied = &cfg
	return nil
}

// transcoder builds the ffmpeg transcoder for cfg, checking the encoder and VMAF support the
// first time each is used. The caller must hold s.mu.
func (s *Supervisor) transcoder(cfg config.Config) (*transcode.FFmpeg, error) {
	encoder, err := transcode.NewEncoder(cfg.Encoder, cfg.Device)
	if err != nil {
		return nil, err
	}
	ffmpeg := transcode.NewFFmpeg(encoder, cfg.Quality)
	key := cfg.Encoder + "|" + cfg.Device
	if !s.verified[key] {
		ctx, cancel := context.WithTimeout(s.ctx, time.Minute)
		defer cancel()
		if err := ffmpeg.Preflight(ctx); err != nil {
			return nil, fmt.Errorf("encoder check: %w", err)
		}
		s.verified[key] = true
	}
	if cfg.MinVMAF > 0 && !s.vmafOK {
		if err := ffmpeg.PreflightVMAF(); err != nil {
			return nil, err
		}
		s.vmafOK = true
	}
	return ffmpeg, nil
}

// crawlerChanged reports whether any setting the crawler uses differs between a and b.
func crawlerChanged(a, b config.Config) bool {
	return a.CrawlPath != b.CrawlPath ||
		!slices.Equal(a.Extensions, b.Extensions) ||
		a.CrawlInterval != b.CrawlInterval ||
		a.MinFileAge != b.MinFileAge ||
		a.SkipHardlinked != b.SkipHardlinked
}

// startCrawler replaces any running crawler with one for cfg, which scans immediately.
// The caller must hold s.mu.
func (s *Supervisor) startCrawler(cfg config.Config) {
	s.stopCrawler()
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	s.stopCrawl, s.crawlDone = cancel, done

	crawl := crawler.New(s.db, crawler.Options{
		Root:           cfg.CrawlPath,
		Extensions:     cfg.Extensions,
		Interval:       time.Duration(cfg.CrawlInterval),
		MinAge:         time.Duration(cfg.MinFileAge),
		SkipHardlinked: cfg.SkipHardlinked,
		OnNew:          s.pool.Notify,
	})
	go func() {
		defer close(done)
		crawl.Run(ctx)
	}()
}

// stopCrawler cancels the running crawler, if any, and waits for it to exit.
// The caller must hold s.mu.
func (s *Supervisor) stopCrawler() {
	if s.stopCrawl == nil {
		return
	}
	s.stopCrawl()
	<-s.crawlDone
	s.stopCrawl, s.crawlDone = nil, nil
}
