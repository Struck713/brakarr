package pool

import (
	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/transcode"
)

type Job struct {
	Media  *media.Media        `json:"media"`
	Status *transcode.Progress `json:"status"`
}

func NewJob(media *media.Media) *Job {
	return &Job{
		Media:  media,
		Status: nil,
	}
}

func (j *Job) SetStatus(status transcode.Progress) {
	j.Status = &status
}
