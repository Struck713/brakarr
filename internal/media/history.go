package media

// History is a finished transcode job, with the name and path of the file it belongs to.
type History struct {
	ID         int64
	Hash       []byte
	Name       string
	Path       string
	Status     Status // failed, skipped or complete
	Reason     string
	SourceSize int64    // bytes
	OutputSize *int64   // bytes; nil when there was no verified output
	VMAF       *float64 // nil when the quality check did not run
	CreatedAt  int64    // unix seconds
	FinishedAt *int64   // unix seconds

	// The file's current state, which may have changed since this job.
	FileStatus   Status
	FileSkipKind SkipKind
	FileFormat   string
}

// Requeueable reports whether the job's file can go back to the queue now.
func (h *History) Requeueable() bool { return Requeueable(h.FileStatus, h.FileSkipKind, h.FileFormat) }
