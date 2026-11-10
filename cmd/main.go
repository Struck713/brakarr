package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Struck713/brakarr/internal/arr"
	"github.com/Struck713/brakarr/internal/config"
	"github.com/Struck713/brakarr/internal/crawler"
	"github.com/Struck713/brakarr/internal/database"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/transcode"
	"github.com/Struck713/brakarr/internal/web"
)

//go:generate templ generate -path ../internal/web/templates

// main runs the application and exits non-zero on error.
func main() {
	if err := run(); err != nil {
		slog.Error("brakarr exited", "error", err)
		os.Exit(1)
	}
}

// run wires up the application's components and blocks until shutdown.
func run() error {
	configPath := flag.String("config", "config.json", "path to the JSON settings file")
	listen := flag.String("listen", "", "override the listen address from the settings file")
	flag.Parse()

	store, err := config.Open(*configPath)
	if err != nil {
		return err
	}
	cfg := store.Get()
	if *listen != "" {
		cfg.ListenAddr = *listen
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Level()})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	settingsErr := cfg.Validate()
	if settingsErr != nil {
		slog.Error("invalid settings: transcoding is disabled until they are fixed and brakarr is restarted",
			"file", store.Path(), "error", settingsErr)
	}

	// Validate has already reported a bad encoder; it is nil then, and never used because
	// transcoding stays disabled.
	encoder, _ := transcode.NewEncoder(cfg.Encoder, cfg.Device)
	ffmpeg := transcode.NewFFmpeg(encoder, cfg.Quality)
	poolOpts := pool.Options{
		Workers:        cfg.Workers,
		WorkDir:        cfg.WorkDir,
		SkipHardlinked: cfg.SkipHardlinked,
		MinVMAF:        float64(cfg.MinVMAF),
		VMAFSamples:    cfg.VMAFSamples,
	}
	if cfg.MinVMAF > 0 {
		poolOpts.Scorer = ffmpeg
	}
	if apps := cfg.Apps(); settingsErr == nil && len(apps) > 0 {
		notifier := arr.NewNotifier(apps, cfg.ArrPathMap)
		poolOpts.OnReplaced = notifier.Replaced
		go notifier.Run(ctx)
	}
	jobs := pool.NewPool(db, ffmpeg, poolOpts)

	if settingsErr == nil {
		if err := ffmpeg.Preflight(ctx); err != nil {
			return err
		}
		if cfg.MinVMAF > 0 {
			if err := ffmpeg.PreflightVMAF(); err != nil {
				return err
			}
		}
		if err := jobs.Start(ctx); err != nil {
			return err
		}
		defer jobs.Stop()

		crawl := crawler.New(db, crawler.Options{
			Root:           cfg.CrawlPath,
			Extensions:     cfg.Extensions,
			Interval:       time.Duration(cfg.CrawlInterval),
			MinAge:         time.Duration(cfg.MinFileAge),
			SkipHardlinked: cfg.SkipHardlinked,
			OnNew:          jobs.Notify,
		})
		go crawl.Run(ctx)
	}

	slog.Info("brakarr started", "listen", cfg.ListenAddr, "crawl", cfg.CrawlPath,
		"encoder", cfg.Encoder, "workers", cfg.Workers, "config", store.Path())

	server := web.NewWeb(db, jobs, store)
	if err := server.Run(ctx, cfg.ListenAddr); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	slog.Info("shutting down")
	return nil
}
