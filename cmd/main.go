package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Struck713/brakarr/internal/config"
	"github.com/Struck713/brakarr/internal/database"
	"github.com/Struck713/brakarr/internal/supervisor"
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
	listenAddr := cfg.ListenAddr
	if *listen != "" {
		listenAddr = *listen
	}
	level := new(slog.LevelVar)
	level.Set(cfg.Level())
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	// Unusable settings leave transcoding disabled, with the UI up so they can be fixed;
	// saving working settings starts it.
	sup := supervisor.New(ctx, store, db, level)
	sup.Start()
	defer sup.Stop()

	slog.Info("brakarr started", "listen", listenAddr, "config", store.Path())

	server := web.NewWeb(db, sup.Pool(), store, sup)
	if err := server.Run(ctx, listenAddr); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	slog.Info("shutting down")
	return nil
}
