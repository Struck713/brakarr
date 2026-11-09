package pool

import (
	"sync"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/transcode"
)

type Job struct {
	Media *media.Media

	mu       sync.RWMutex
	progress *transcode.Progress
}

func NewJob(media *media.Media) *Job {
	return &Job{
		Media: media,
	}
}

func (j *Job) SetProgress(progress transcode.Progress) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.progress = &progress
}

func (j *Job) Progress() *transcode.Progress {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.progress
}
