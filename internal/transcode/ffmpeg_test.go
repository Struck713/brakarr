package transcode

import (
	"slices"
	"testing"
)

func value(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

func TestArgsEightBit(t *testing.T) {
	args := NewFFmpeg("/dev/dri/renderD128", 22).Args("in.mkv", "out.mkv", 8)
	if got := value(args, "-profile:v"); got != "main" {
		t.Errorf("profile = %q, want main", got)
	}
	if got := value(args, "-vf"); got != "format=nv12|vaapi,hwupload" {
		t.Errorf("vf = %q", got)
	}
	if got := value(args, "-qp"); got != "22" {
		t.Errorf("qp = %q", got)
	}
}

func TestArgsTenBit(t *testing.T) {
	args := NewFFmpeg("/dev/dri/renderD128", 22).Args("in.mkv", "out.mkv", 10)
	if got := value(args, "-profile:v"); got != "main10" {
		t.Errorf("profile = %q, want main10", got)
	}
	if got := value(args, "-vf"); got != "format=p010|vaapi,hwupload" {
		t.Errorf("vf = %q", got)
	}
}

func TestArgsHvc1TagOnlyForMP4(t *testing.T) {
	f := NewFFmpeg("/dev/dri/renderD128", 22)
	for output, want := range map[string]bool{"out.mp4": true, "out.MP4": true, "out.mkv": false} {
		args := f.Args("in", output, 8)
		if got := slices.Contains(args, "-tag:v"); got != want {
			t.Errorf("%s: has -tag:v = %v, want %v", output, got, want)
		}
		if want && value(args, "-tag:v") != "hvc1" {
			t.Errorf("%s: tag = %q", output, value(args, "-tag:v"))
		}
		if args[len(args)-1] != output {
			t.Errorf("%s: output is not the last argument: %v", output, args)
		}
	}
}

func TestArgsUseNamedDevice(t *testing.T) {
	args := NewFFmpeg("/dev/dri/renderD129", 22).Args("in", "out.mkv", 8)
	if got := value(args, "-init_hw_device"); got != "vaapi=va:/dev/dri/renderD129" {
		t.Errorf("init_hw_device = %q", got)
	}
}
