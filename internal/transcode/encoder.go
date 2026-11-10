package transcode

import (
	"context"
	"fmt"
	"strings"
)

// Encoder describes how ffmpeg decodes and encodes HEVC on one kind of GPU. To support another
// GPU, implement this interface and add it to the encoders list.
type Encoder interface {
	// Name is the encoder's ID, as stored in the settings file.
	Name() string
	// Preflight checks that the GPU is usable with the ffmpeg on PATH.
	Preflight(ctx context.Context) error
	// InputArgs are placed before -i. They set up the hardware device and hardware decoding.
	InputArgs() []string
	// VideoArgs are the video filter and encoder options for a source of the given bit depth.
	// They must accept both hardware frames and software frames, since ffmpeg falls back to
	// software decoding when the GPU cannot decode the source. Quality is 1-51, lower is better.
	VideoArgs(bitDepth, quality int) []string
}

// EncoderInfo describes an available encoder for the settings page.
type EncoderInfo struct {
	ID    string
	Label string

	new func(device string) (Encoder, error)
}

var encoders = []EncoderInfo{
	{ID: "vaapi", Label: "VAAPI (Intel, AMD)", new: newVAAPI},
	{ID: "nvenc", Label: "NVENC (NVIDIA)", new: newNVENC},
}

// DefaultEncoder is the encoder used when none is configured.
const DefaultEncoder = "vaapi"

// Encoders lists the supported encoders.
func Encoders() []EncoderInfo { return encoders }

// NewEncoder creates the encoder with the given ID. The meaning of device depends on the
// encoder; an empty device selects the encoder's default.
func NewEncoder(id, device string) (Encoder, error) {
	ids := make([]string, len(encoders))
	for i, e := range encoders {
		if e.ID == id {
			return e.new(strings.TrimSpace(device))
		}
		ids[i] = e.ID
	}
	return nil, fmt.Errorf("unknown encoder %q (use %s)", id, strings.Join(ids, " or "))
}

// pixelFormat returns the HEVC profile and the software pixel format for a bit depth.
func pixelFormat(bitDepth int) (profile, swFormat string) {
	if bitDepth > 8 {
		return "main10", "p010"
	}
	return "main", "nv12"
}
