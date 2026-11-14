package transcode

import (
	"strings"
	"testing"
)

// TestParseProgress checks parsing of ffmpeg's -progress output:
//   - each "progress=" line emits one snapshot holding the keys seen since the last one,
//   - numeric fields are parsed and string fields kept verbatim,
//   - fields reset between snapshots (a key missing from a block reads as zero),
//   - "progress=end" sets Done; "progress=continue" does not,
//   - lines without "=" and unknown keys are ignored, and whitespace is trimmed,
//   - an unparsable number is left at zero instead of failing the parse.
func TestParseProgress(t *testing.T) {
	input := strings.Join([]string{
		"frame=120",
		"fps=29.97",
		"bitrate=1234.5kbits/s",
		"total_size=1048576",
		"out_time_us=4000000",
		"out_time=00:00:04.000000",
		"dup_frames=1",
		"drop_frames=2",
		"speed=1.5x",
		"garbage line",
		"unknown_key=whatever",
		"progress=continue",
		" frame = 240 ",
		"fps=N/A",
		"progress=end",
	}, "\n")

	var got []Progress
	if err := ParseProgress(strings.NewReader(input), func(p Progress) { got = append(got, p) }); err != nil {
		t.Fatal(err)
	}
	want := []Progress{
		{Frame: 120, Fps: 29.97, Bitrate: "1234.5kbits/s", TotalSize: 1048576, OutTimeUs: 4000000,
			OutTime: "00:00:04.000000", DupFrames: 1, DropFrames: 2, Speed: "1.5x"},
		{Frame: 240, Done: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d snapshots, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("snapshot %d:\n got  %+v\n want %+v", i, got[i], want[i])
		}
	}
}

// TestParseProgressNilCallback checks that a nil callback is allowed and the
// input is still consumed without error.
func TestParseProgressNilCallback(t *testing.T) {
	if err := ParseProgress(strings.NewReader("frame=1\nprogress=end\n"), nil); err != nil {
		t.Fatal(err)
	}
}

// TestPercent checks Percent's edge cases: a finished job is always 100, an
// unknown (zero or negative) duration is 0, progress is out_time/duration,
// and an out_time past the duration is capped at 100.
func TestPercent(t *testing.T) {
	tests := []struct {
		name       string
		p          Progress
		durationUs int64
		want       float64
	}{
		{"done", Progress{Done: true}, 0, 100},
		{"zero duration", Progress{OutTimeUs: 5}, 0, 0},
		{"negative duration", Progress{OutTimeUs: 5}, -1, 0},
		{"halfway", Progress{OutTimeUs: 50}, 100, 50},
		{"overshoot", Progress{OutTimeUs: 150}, 100, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Percent(tt.durationUs); got != tt.want {
				t.Errorf("Percent = %v, want %v", got, tt.want)
			}
		})
	}
}
