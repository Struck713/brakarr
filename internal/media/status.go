package media

// Status is the processing state of a media file.
type Status string

const (
	StatusPending  Status = "pending"
	StatusRunning  Status = "running"
	StatusFailed   Status = "failed"
	StatusSkipped  Status = "skipped"
	StatusComplete Status = "complete"
	StatusDeferred Status = "deferred" // waiting for hardlinks to go away
)

// SkipKind is why a file was skipped.
type SkipKind string

const (
	SkipNone       SkipKind = ""
	SkipHEVC       SkipKind = "hevc"        // already HEVC, never transcoded
	SkipQuality    SkipKind = "quality"     // transcode scored below the minimum VMAF
	SkipNotSmaller SkipKind = "not_smaller" // transcode was not smaller than the original
)

// Requeueable reports whether a file in this state can go back to the queue: failed files, and
// files skipped after a transcode that other settings might make worth keeping.
func Requeueable(status Status, kind SkipKind) bool {
	switch status {
	case StatusFailed:
		return true
	case StatusSkipped:
		return kind == SkipQuality || kind == SkipNotSmaller
	}
	return false
}
