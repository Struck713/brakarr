package transcode

import (
	"slices"
	"strings"
	"testing"
)

func newTestFFmpeg(t *testing.T, encoder, device string) *FFmpeg {
	t.Helper()
	e, err := NewEncoder(encoder, device)
	if err != nil {
		t.Fatal(err)
	}
	return NewFFmpeg(e, 24)
}

// TestArgsVAAPI is a snapshot of the full ffmpeg command for an 8-bit VAAPI
// transcode to .mkv, so any change to the flags shows up as a test diff.
func TestArgsVAAPI(t *testing.T) {
	got := newTestFFmpeg(t, "vaapi", "").Args("in.mkv", "out.mkv", 8)
	want := []string{
		"-nostdin", "-y",
		"-init_hw_device", "vaapi=va:/dev/dri/renderD128",
		"-filter_hw_device", "va",
		"-hwaccel", "vaapi", "-hwaccel_device", "va",
		"-hwaccel_output_format", "vaapi",
		"-i", "in.mkv",
		"-loglevel", "error",
		"-nostats", "-progress", "pipe:1",
		"-map", "0:v:0", "-map", "0:a?", "-map", "0:s?", "-map", "0:t?",
		"-vf", "format=nv12|vaapi,hwupload",
		"-c:v", "hevc_vaapi", "-profile:v", "main", "-rc_mode", "ICQ", "-global_quality", "24",
		"-c:a", "copy", "-c:s", "copy", "-c:t", "copy",
		"out.mkv",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Args:\n got  %q\n want %q", got, want)
	}
}

// TestArgsNVENC is a snapshot of the full ffmpeg command for a 10-bit NVENC
// transcode on GPU 1, checking the device index, main10/p010 and the
// constant-quality flags.
func TestArgsNVENC(t *testing.T) {
	got := newTestFFmpeg(t, "nvenc", "1").Args("in.mkv", "out.mkv", 10)
	want := []string{
		"-nostdin", "-y",
		"-init_hw_device", "cuda=nv:1", "-filter_hw_device", "nv",
		"-hwaccel", "cuda", "-hwaccel_device", "nv",
		"-hwaccel_output_format", "cuda",
		"-i", "in.mkv",
		"-loglevel", "error",
		"-nostats", "-progress", "pipe:1",
		"-map", "0:v:0", "-map", "0:a?", "-map", "0:s?", "-map", "0:t?",
		"-vf", "format=p010|cuda,hwupload",
		"-c:v", "hevc_nvenc", "-profile:v", "main10", "-rc", "vbr", "-cq", "24", "-b:v", "0",
		"-c:a", "copy", "-c:s", "copy", "-c:t", "copy",
		"out.mkv",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Args:\n got  %q\n want %q", got, want)
	}
}

// TestArgsMP4Tag checks that .mp4 output (in any case) gets the hvc1 tag
// Apple players need, placed just before the output path, and .mkv does not.
func TestArgsMP4Tag(t *testing.T) {
	f := newTestFFmpeg(t, "vaapi", "")
	for _, out := range []string{"out.mp4", "OUT.MP4"} {
		args := f.Args("in.mkv", out, 8)
		if tail := args[len(args)-3:]; !slices.Equal(tail, []string{"-tag:v", "hvc1", out}) {
			t.Errorf("%s: args end with %q", out, tail)
		}
	}
	if slices.Contains(f.Args("in.mkv", "out.mkv", 8), "hvc1") {
		t.Error("mkv output has hvc1 tag")
	}
}

// TestNewEncoder checks encoder construction:
//   - every listed encoder ID can be built and reports its own Name,
//   - VAAPI defaults to renderD128 when no device is given, and the device is trimmed,
//   - NVENC accepts a blank or non-negative integer GPU index and rejects anything else,
//   - an unknown ID is an error that names the valid choices.
func TestNewEncoder(t *testing.T) {
	for _, info := range Encoders() {
		e, err := NewEncoder(info.ID, "")
		if err != nil {
			t.Fatalf("%s: %v", info.ID, err)
		}
		if e.Name() != info.ID {
			t.Errorf("%s: Name() = %s", info.ID, e.Name())
		}
	}

	e, _ := NewEncoder("vaapi", "  /dev/dri/renderD129 ")
	if !slices.Contains(e.InputArgs(), "vaapi=va:/dev/dri/renderD129") {
		t.Errorf("vaapi device not used/trimmed: %q", e.InputArgs())
	}

	for _, dev := range []string{"", "0", "3"} {
		if _, err := NewEncoder("nvenc", dev); err != nil {
			t.Errorf("nvenc device %q: %v", dev, err)
		}
	}
	for _, dev := range []string{"-1", "gpu0", "/dev/nvidia0"} {
		if _, err := NewEncoder("nvenc", dev); err == nil {
			t.Errorf("nvenc device %q accepted; want error", dev)
		}
	}

	_, err := NewEncoder("qsv", "")
	if err == nil || !strings.Contains(err.Error(), "vaapi") || !strings.Contains(err.Error(), "nvenc") {
		t.Errorf("unknown encoder error = %v; want it to list vaapi and nvenc", err)
	}
}

// TestTail checks that tail leaves short strings alone and keeps only the
// last n bytes (prefixed with "...") of long ones.
func TestTail(t *testing.T) {
	if got := tail("short", 10); got != "short" {
		t.Errorf("tail(short) = %q", got)
	}
	if got := tail("0123456789", 3); got != "...789" {
		t.Errorf("tail(long) = %q", got)
	}
}
