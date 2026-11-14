package media

import "testing"

// TestRequeueable is the truth table for which file states can go back to the
// queue: failed files always; skipped files only when skipped after a transcode
// (quality or not smaller), never for already-HEVC; and never pending, running,
// complete or deferred files. The database's SQL filter is tested against this
// function in internal/database.
func TestRequeueable(t *testing.T) {
	tests := []struct {
		status Status
		kind   SkipKind
		want   bool
	}{
		{StatusFailed, SkipNone, true},
		{StatusSkipped, SkipQuality, true},
		{StatusSkipped, SkipNotSmaller, true},
		{StatusSkipped, SkipHEVC, false},
		{StatusSkipped, SkipNone, false},
		{StatusPending, SkipNone, false},
		{StatusRunning, SkipNone, false},
		{StatusComplete, SkipNone, false},
		{StatusDeferred, SkipNone, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status)+"/"+string(tt.kind), func(t *testing.T) {
			if got := Requeueable(tt.status, tt.kind); got != tt.want {
				t.Errorf("Requeueable = %v, want %v", got, tt.want)
			}
			m := Media{Status: tt.status, SkipKind: tt.kind}
			h := History{FileStatus: tt.status, FileSkipKind: tt.kind}
			if m.Requeueable() != tt.want || h.Requeueable() != tt.want {
				t.Errorf("Media/History.Requeueable disagree with Requeueable")
			}
		})
	}
}

// TestIsHEVC checks that the HEVC format check ignores case and rejects other formats.
func TestIsHEVC(t *testing.T) {
	for format, want := range map[string]bool{"HEVC": true, "hevc": true, "AVC": false, "": false} {
		if got := (&Media{Format: format}).IsHEVC(); got != want {
			t.Errorf("IsHEVC(%q) = %v, want %v", format, got, want)
		}
	}
}

// TestParseDurationUs checks mediainfo's millisecond duration is converted to
// microseconds, including fractional milliseconds, and that an empty or
// non-numeric value becomes 0 instead of failing the probe.
func TestParseDurationUs(t *testing.T) {
	tests := map[string]int64{
		"1000":     1_000_000,
		"1234.567": 1_234_567,
		"0":        0,
		"":         0,
		"N/A":      0,
	}
	for in, want := range tests {
		if got := parseDurationUs(in); got != want {
			t.Errorf("parseDurationUs(%q) = %d, want %d", in, got, want)
		}
	}
}
