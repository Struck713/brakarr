package transcode

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Progress holds a snapshot of ffmpeg's reported progress.
type Progress struct {
	Frame      int64
	Fps        float64
	Bitrate    string
	TotalSize  int64
	OutTimeUs  int64 // ffmpeg's out_time_ms is also microseconds, so it is not tracked separately
	OutTime    string
	DupFrames  int64
	DropFrames int64
	Speed      string
	Done       bool
}

// set stores a single key/value pair from ffmpeg's progress output.
func (o *Progress) set(key, value string) {
	parseInt := func(dst *int64) {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			*dst = n
		}
	}
	switch key {
	case "frame":
		parseInt(&o.Frame)
	case "fps":
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			o.Fps = f
		}
	case "bitrate":
		o.Bitrate = value
	case "total_size":
		parseInt(&o.TotalSize)
	case "out_time_us":
		parseInt(&o.OutTimeUs)
	case "out_time":
		o.OutTime = value
	case "dup_frames":
		parseInt(&o.DupFrames)
	case "drop_frames":
		parseInt(&o.DropFrames)
	case "speed":
		o.Speed = value
	}
}

// Percent returns completion as a percentage of the given total duration in microseconds.
func (o *Progress) Percent(durationUs int64) float64 {
	if o.Done {
		return 100
	}
	if durationUs <= 0 {
		return 0
	}
	return min(float64(o.OutTimeUs)/float64(durationUs)*100, 100)
}

// ParseProgress reads ffmpeg progress output from r, calling onProgress for each update.
func ParseProgress(r io.Reader, onProgress func(Progress)) error {
	current := Progress{}
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if key != "progress" {
			current.set(key, value)
			continue
		}

		current.Done = value == "end"
		if onProgress != nil {
			onProgress(current)
		}
		current = Progress{}
	}
	return scanner.Err()
}
