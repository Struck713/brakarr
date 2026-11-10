package transcode

import (
	"context"
	"fmt"
	"os"
	"strconv"
)

const defaultVAAPIDevice = "/dev/dri/renderD128"

// vaapi encodes with hevc_vaapi on Intel and AMD GPUs.
type vaapi struct {
	device string // render node, such as /dev/dri/renderD128
}

// newVAAPI creates a VAAPI encoder for the given render node.
func newVAAPI(device string) (Encoder, error) {
	if device == "" {
		device = defaultVAAPIDevice
	}
	return &vaapi{device: device}, nil
}

func (v *vaapi) Name() string { return "vaapi" }

func (v *vaapi) Preflight(context.Context) error {
	if _, err := os.Stat(v.device); err != nil {
		return fmt.Errorf("vaapi device: %w", err)
	}
	return nil
}

func (v *vaapi) InputArgs() []string {
	return []string{
		"-init_hw_device", "vaapi=va:" + v.device,
		"-filter_hw_device", "va",
		"-hwaccel", "vaapi", "-hwaccel_device", "va",
		"-hwaccel_output_format", "vaapi",
	}
}

func (v *vaapi) VideoArgs(bitDepth, quality int) []string {
	profile, swFormat := pixelFormat(bitDepth)
	return []string{
		// Accepts hardware frames as they are, and uploads software frames
		// when the GPU could not decode the source.
		"-vf", "format=" + swFormat + "|vaapi,hwupload",
		"-c:v", "hevc_vaapi", "-profile:v", profile, "-rc_mode", "ICQ", "-global_quality", strconv.Itoa(quality),
	}
}
