package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Struck713/brakarr/internal/config"
	"github.com/Struck713/brakarr/internal/crawler"
	"github.com/Struck713/brakarr/internal/database"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/transcode"
	"github.com/Struck713/brakarr/internal/web"
)

//go:generate templ generate

func main() {
	if err := run(); err != nil {
		slog.Error("brakarr exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ffmpeg := transcode.NewFFmpeg(cfg.VaapiDevice, cfg.Quality)
	if err := ffmpeg.Preflight(); err != nil {
		return err
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	jobs := pool.NewPool(db, ffmpeg, pool.Options{
		Workers: cfg.Workers,
		WorkDir: cfg.WorkDir,
	})
	if err := jobs.Start(ctx); err != nil {
		return err
	}
	defer jobs.Stop()

	crawl := crawler.New(db, crawler.Options{
		Root:       cfg.CrawlPath,
		Extensions: cfg.Extensions,
		Interval:   cfg.CrawlInterval,
		MinAge:     cfg.MinFileAge,
		OnNew:      jobs.Notify,
	})
	go crawl.Run(ctx)

	slog.Info("brakarr started", "listen", cfg.ListenAddr, "crawl", cfg.CrawlPath,
		"workers", cfg.Workers)

	server := web.NewWeb(db, jobs)
	if err := server.Run(ctx, cfg.ListenAddr); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	slog.Info("shutting down")
	return nil
}
