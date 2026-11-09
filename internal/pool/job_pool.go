package pool

import (
	"log"
	"time"

	"github.com/Struck713/brakarr/internal/database"
	"github.com/Struck713/brakarr/internal/transcode"
)

type Pool struct {
	Job *Job

	ticker *time.Ticker
	db     *database.Database
	ffmpeg *transcode.FFmpeg
}

func NewPool(db *database.Database, ffmpeg *transcode.FFmpeg) *Pool {
	return &Pool{
		db:     db,
		ticker: time.NewTicker(5 * time.Second),
		ffmpeg: ffmpeg,
	}
}

func (t *Pool) Start() {
	log.Println("pool: starting..")
	for range t.ticker.C {
		log.Println("pool: checking job..")
		if t.Job == nil {
			t.Next()
		}
	}
}

func (t *Pool) Next() {
	media, err := t.db.GetNextNotProcessed()
	if err != nil {
		log.Printf("failed to get next: %s", err)
		return
	}

	t.Job = NewJob(media)
	if err := t.ffmpeg.Transcode(media.Path, "./out.mkv", 22, t.Job.SetStatus); err != nil {
		log.Printf("failed to transcode: %s", err)
		return
	}

	if err := t.db.Process(media); err != nil {
		log.Printf("failed to save: %s", err)
		return
	}

	t.Job = nil

	log.Printf("complete transcoding: %s", media.Name)
}

func (t *Pool) Stop() {
	t.ticker.Stop()
}
