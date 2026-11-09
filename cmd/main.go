package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/Struck713/brakarr/internal/crawler"
	"github.com/Struck713/brakarr/internal/database"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/transcode"
	"github.com/Struck713/brakarr/internal/web"
)

//go:generate templ generate

func main() {

	context := context.Background()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	ffmpeg := transcode.NewFFmpeg("/dev/dri/renderD128")
	db := database.NewDatabase()

	crawler := crawler.NewCrawler(db, os.Getenv("CRAWL_PATH"), []string{".mkv"})
	go crawler.Crawl()

	pool := pool.NewPool(db, ffmpeg, 1, "./work")
	go pool.Start(context)

	web := web.NewWeb(db, pool)
	web.Init()

	<-context.Done()
	pool.Stop()
}
