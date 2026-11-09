package transcode

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type FFmpeg struct {
	Device string
}

func NewFFmpeg(device string) *FFmpeg {
	return &FFmpeg{
		Device: device,
	}
}

func (ffmpeg *FFmpeg) Transcode(context context.Context, input, output string, quality int, onProgress func(Progress)) error {
	args := []string{
		"-hwaccel", "vaapi", "-hwaccel_device", ffmpeg.Device,
		"-hwaccel_output_format", "vaapi",
		"-i", input, "-y",
		"-loglevel", "error",
		"-nostats", "-progress", "pipe:1",
		"-map", "0", "-c:v", "hevc_vaapi", "-qp", strconv.Itoa(quality),
		"-c:a", "copy", "-c:s", "copy",
		output,
	}

	cmd := exec.Command("ffmpeg", args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	current := Progress{}
	scanner := bufio.NewScanner(stdout)

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

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w: %s", err, stderr.String())
	}
	return nil
}
