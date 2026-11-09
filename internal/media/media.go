package media

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/zelenin/go-mediainfo"
)

type Media struct {
	Hash      []byte `json:"hash"`
	Path      string `json:"path"`
	Name      string `json:"title"`
	Format    string `json:"format"`
	BitDepth  string `json:"bit_depth" db:"bit_depth"`
	Duration  int64  `json:"duration" db:"duration"`
	Size      string `json:"size"`
	CreatedAt int64  `json:"created_at" db:"created_at" goqu:"omitempty"`
	Status    Status `json:"status" goqu:"omitempty"`
}

func NewMedia(path string, hash []byte) (*Media, error) {
	info, err := mediainfo.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mediainfo open: %s", err)
	}
	defer info.Close()

	size := info.Parameter(mediainfo.StreamGeneral, 0, "FileSize")
	format := info.Parameter(mediainfo.StreamVideo, 0, "Format")
	bit_depth := info.Parameter(mediainfo.StreamVideo, 0, "BitDepth")
	duration := info.Parameter(mediainfo.StreamGeneral, 0, "Duration")
	return &Media{
		Hash:     hash,
		Path:     path,
		Name:     filepath.Base(path),
		Size:     size,
		Format:   format,
		BitDepth: bit_depth,
		Duration: parseDurationUs(duration),
	}, nil
}

func parseDurationUs(milliseconds string) int64 {
	value, err := strconv.ParseFloat(milliseconds, 64)
	if err != nil {
		return 0
	}
	return int64(value * 1000)
}
