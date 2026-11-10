package pool

import (
	"sync"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/transcode"
)

// Job tracks a media item being transcoded and its latest progress.
type Job struct {
	Media *media.Media

	mu       sync.RWMutex
	progress *transcode.Progress
}

// NewJob creates a Job for the given media item.
func NewJob(media *media.Media) *Job {
	return &Job{Media: media}
}

// SetProgress stores the latest progress snapshot.
func (j *Job) SetProgress(progress transcode.Progress) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.progress = &progress
}

// Progress returns the latest progress snapshot.
func (j *Job) Progress() *transcode.Progress {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.progress
}
