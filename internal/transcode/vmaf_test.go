package transcode

import (
	"math"
	"testing"
)

// TestSampleWindows checks how VMAF segments are chosen:
//   - an unknown (zero) duration falls back to one sampleSeconds window from the start,
//   - a video too short for separate samples (or samples < 1) is compared whole,
//   - otherwise there are exactly `samples` windows of sampleSeconds, centred at
//     evenly spaced points (avoiding the very start and end), in order, and inside the video.
func TestSampleWindows(t *testing.T) {
	if got := sampleWindows(0, 3); len(got) != 1 || got[0] != (window{0, sampleSeconds}) {
		t.Errorf("zero duration: %v", got)
	}
	if got := sampleWindows(50e6, 3); len(got) != 1 || got[0] != (window{0, 50}) {
		t.Errorf("short video: %v", got)
	}
	if got := sampleWindows(3600e6, 0); len(got) != 1 || got[0] != (window{0, 3600}) {
		t.Errorf("zero samples: %v", got)
	}

	got := sampleWindows(3600e6, 3) // centres at 900s, 1800s, 2700s
	want := []window{{890, sampleSeconds}, {1790, sampleSeconds}, {2690, sampleSeconds}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i].start-want[i].start) > 1e-9 || got[i].length != want[i].length {
			t.Errorf("window %d = %v, want %v", i, got[i], want[i])
		}
	}

	// Just over the "too short" boundary, windows must still fit inside the video.
	total := 61.0
	for _, w := range sampleWindows(int64(total*1e6), 3) {
		if w.start < 0 || w.start+w.length > total+1e-9 {
			t.Errorf("window %v falls outside a %vs video", w, total)
		}
	}
}

// TestParseScore checks that the VMAF score is pulled out of ffmpeg's log
// wherever it appears, and that a log without one is an error.
func TestParseScore(t *testing.T) {
	log := "[Parsed_libvmaf_4 @ 0x55] VMAF score: 94.512345\nother output\n"
	got, err := parseScore(log)
	if err != nil || got != 94.512345 {
		t.Fatalf("parseScore = %v, %v; want 94.512345", got, err)
	}
	if _, err := parseScore("no score here"); err == nil {
		t.Fatal("parseScore without a score succeeded; want error")
	}
}
