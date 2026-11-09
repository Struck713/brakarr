package transcode

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

type FFmpeg struct {
	Device  string
	Quality int
}

func NewFFmpeg(device string, quality int) *FFmpeg {
	return &FFmpeg{Device: device, Quality: quality}
}

func (f *FFmpeg) Preflight() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}
	if _, err := os.Stat(f.Device); err != nil {
		return fmt.Errorf("vaapi device: %w", err)
	}
	return nil
}

func (f *FFmpeg) Args(input, output string, bitDepth int) []string {
	profile := "main"
	if bitDepth > 8 {
		profile = "main10"
	}
	return []string{
		"-nostdin", "-y",
		"-hwaccel", "vaapi", "-hwaccel_device", f.Device,
		"-hwaccel_output_format", "vaapi",
		"-i", input,
		"-loglevel", "error",
		"-nostats", "-progress", "pipe:1",
		"-map", "0:v:0", "-map", "0:a?", "-map", "0:s?", "-map", "0:t?",
		"-c:v", "hevc_vaapi", "-profile:v", profile, "-qp", strconv.Itoa(f.Quality),
		"-c:a", "copy", "-c:s", "copy", "-c:t", "copy",
		output,
	}
}

func (f *FFmpeg) Transcode(ctx context.Context, input, output string, bitDepth int, onProgress func(Progress)) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", f.Args(input, output, bitDepth)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 15 * time.Second

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	parseErr := ParseProgress(stdout, onProgress)

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg failed: %w: %s", err, tail(stderr.String(), 2000))
	}
	return parseErr
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
