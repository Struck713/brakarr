package transcode

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FFmpeg transcodes video to HEVC using the ffmpeg binary.
type FFmpeg struct {
	Encoder Encoder
	Quality int // quality target, 1-51, lower is better
}

// NewFFmpeg creates an FFmpeg transcoder for the given encoder and quality.
func NewFFmpeg(encoder Encoder, quality int) *FFmpeg {
	return &FFmpeg{Encoder: encoder, Quality: quality}
}

// Preflight checks that ffmpeg and the configured encoder are usable.
func (f *FFmpeg) Preflight(ctx context.Context) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}
	return f.Encoder.Preflight(ctx)
}

// Args builds the ffmpeg command-line arguments for a transcode.
func (f *FFmpeg) Args(input, output string, bitDepth int) []string {
	args := []string{"-nostdin", "-y"}
	args = append(args, f.Encoder.InputArgs()...)
	args = append(args,
		"-i", input,
		"-loglevel", "error",
		"-nostats", "-progress", "pipe:1",
		"-map", "0:v:0", "-map", "0:a?", "-map", "0:s?", "-map", "0:t?",
	)
	args = append(args, f.Encoder.VideoArgs(bitDepth, f.Quality)...)
	args = append(args, "-c:a", "copy", "-c:s", "copy", "-c:t", "copy")
	if strings.EqualFold(filepath.Ext(output), ".mp4") {
		args = append(args, "-tag:v", "hvc1") // required by Apple players
	}
	return append(args, output)
}

// Transcode runs ffmpeg on input, writing output and reporting progress.
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

// tail returns the last n bytes of s.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
