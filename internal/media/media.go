package media

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kalafut/imohash"
	"github.com/zelenin/go-mediainfo"
)

// Media describes a video file tracked by the application.
type Media struct {
	Hash      []byte
	Path      string
	Name      string
	Format    string
	BitDepth  string
	Duration  int64 // microseconds
	Size      int64 // bytes
	CreatedAt int64 // unix seconds
	Status    Status
	Error     string // last failure reason, if any
}

// IsHEVC reports whether the file is already HEVC encoded.
func (m *Media) IsHEVC() bool {
	return strings.EqualFold(m.Format, "HEVC")
}

// Hash returns a content hash identifying the file at path.
func Hash(path string) ([]byte, error) {
	sum, err := imohash.SumFile(path)
	if err != nil {
		return nil, err
	}
	return sum[:], nil
}

// Probe inspects the file with ffprobe and returns its metadata.
func Probe(path string, hash []byte) (*Media, error) {
	info, err := mediainfo.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mediainfo open: %w", err)
	}
	defer info.Close()

	size, _ := strconv.ParseInt(info.Parameter(mediainfo.StreamGeneral, 0, "FileSize"), 10, 64)
	m := &Media{
		Hash:     hash,
		Path:     path,
		Name:     filepath.Base(path),
		Size:     size,
		Format:   info.Parameter(mediainfo.StreamVideo, 0, "Format"),
		BitDepth: info.Parameter(mediainfo.StreamVideo, 0, "BitDepth"),
		Duration: parseDurationUs(info.Parameter(mediainfo.StreamGeneral, 0, "Duration")),
	}
	if m.Format == "" {
		return m, ErrNoVideo
	}
	return m, nil
}

var ErrNoVideo = errors.New("no video stream found")

// parseDurationUs converts a millisecond string into microseconds.
func parseDurationUs(milliseconds string) int64 {
	value, err := strconv.ParseFloat(milliseconds, 64)
	if err != nil {
		return 0
	}
	return int64(value * 1000)
}
