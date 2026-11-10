package transcode

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
)

// nvenc encodes with hevc_nvenc on NVIDIA GPUs.
type nvenc struct {
	device string // GPU index, or empty for the first GPU
}

// newNVENC creates an NVENC encoder for the GPU with the given index.
func newNVENC(device string) (Encoder, error) {
	if device != "" {
		if n, err := strconv.Atoi(device); err != nil || n < 0 {
			return nil, fmt.Errorf("nvenc device %q must be a GPU index such as 0, or blank for the first GPU", device)
		}
	}
	return &nvenc{device: device}, nil
}

func (n *nvenc) Name() string { return "nvenc" }

// Preflight encodes a few blank frames, which fails when the ffmpeg build lacks NVENC, the
// driver is missing or too old, or the GPU index does not exist.
func (n *nvenc) Preflight(ctx context.Context) error {
	args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error"}, n.deviceArgs()...)
	args = append(args, "-f", "lavfi", "-i", "color=size=256x256:rate=25:duration=0.2")
	args = append(args, n.VideoArgs(8, 23)...)
	args = append(args, "-f", "null", "-")

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nvenc test encode failed: %w: %s", err, tail(stderr.String(), 2000))
	}
	return nil
}

// deviceArgs opens the GPU and makes it available to filters.
func (n *nvenc) deviceArgs() []string {
	device := "cuda=nv"
	if n.device != "" {
		device += ":" + n.device
	}
	return []string{"-init_hw_device", device, "-filter_hw_device", "nv"}
}

func (n *nvenc) InputArgs() []string {
	return append(n.deviceArgs(),
		"-hwaccel", "cuda", "-hwaccel_device", "nv",
		"-hwaccel_output_format", "cuda",
	)
}

func (n *nvenc) VideoArgs(bitDepth, quality int) []string {
	profile, swFormat := pixelFormat(bitDepth)
	return []string{
		// Accepts hardware frames as they are, and uploads software frames
		// when the GPU could not decode the source.
		"-vf", "format=" + swFormat + "|cuda,hwupload",
		// A VBR with no bitrate target makes -cq a constant quality target.
		"-c:v", "hevc_nvenc", "-profile:v", profile, "-rc", "vbr", "-cq", strconv.Itoa(quality), "-b:v", "0",
	}
}
