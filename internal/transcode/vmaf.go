package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	// sampleSeconds is the length of each compared segment.
	sampleSeconds = 20.0
	// frameSubsample scores one frame in every N, which keeps the check cheap on a weak CPU.
	frameSubsample = 5
)

var vmafScore = regexp.MustCompile(`VMAF score:\s*([0-9.]+)`)

// window is a span of the video, in seconds, that is compared.
type window struct{ start, length float64 }

// PreflightVMAF checks that this ffmpeg build includes the libvmaf filter.
func (f *FFmpeg) PreflightVMAF() error {
	out, err := exec.Command("ffmpeg", "-hide_banner", "-filters").Output()
	if err != nil {
		return fmt.Errorf("list ffmpeg filters: %w", err)
	}
	if !strings.Contains(string(out), " libvmaf ") {
		return errors.New("this ffmpeg build lacks the libvmaf filter; install one that has it or set the minimum VMAF to 0")
	}
	return nil
}

// Score returns the lowest VMAF score across evenly spread segments of the video, comparing
// output against source. Using the lowest score means one badly degraded scene is enough to fail.
func (f *FFmpeg) Score(ctx context.Context, source, output string, durationUs int64, bitDepth, samples int) (float64, error) {
	pixFmt := "yuv420p"
	if bitDepth > 8 {
		pixFmt = "yuv420p10le"
	}
	lowest := 101.0
	for _, w := range sampleWindows(durationUs, samples) {
		score, err := scoreWindow(ctx, source, output, pixFmt, w)
		if err != nil {
			return 0, err
		}
		lowest = min(lowest, score)
	}
	return lowest, nil
}

// sampleWindows picks segments spread evenly through the video, avoiding the very start and end
// where intros and credits are. A video too short for separate segments is compared whole.
func sampleWindows(durationUs int64, samples int) []window {
	total := float64(durationUs) / 1e6
	if total <= 0 {
		return []window{{0, sampleSeconds}}
	}
	if samples < 1 || total <= float64(samples)*sampleSeconds {
		return []window{{0, total}}
	}
	windows := make([]window, samples)
	for i := range windows {
		center := total * float64(i+1) / float64(samples+1)
		start := min(max(center-sampleSeconds/2, 0), total-sampleSeconds)
		windows[i] = window{start, sampleSeconds}
	}
	return windows
}

// scoreWindow runs libvmaf over one segment of both files.
func scoreWindow(ctx context.Context, source, output, pixFmt string, w window) (float64, error) {
	seek := []string{"-ss", ftoa(w.start), "-t", ftoa(w.length)}
	// libvmaf takes the distorted video first and the reference second. Frames are paired by
	// index, not timestamp: the two files round their timestamps differently, and pairing by
	// timestamp then matches the wrong frames and scores far too low.
	graph := fmt.Sprintf(
		"[0:v:0]settb=1,setpts=N,format=%[1]s[dist];[1:v:0]settb=1,setpts=N,format=%[1]s[ref];"+
			"[dist][ref]libvmaf=n_threads=%[2]d:n_subsample=%[3]d",
		pixFmt, runtime.NumCPU(), frameSubsample)

	args := []string{"-nostdin", "-hide_banner", "-nostats", "-loglevel", "info"}
	args = append(args, seek...)
	args = append(args, "-i", output)
	args = append(args, seek...)
	args = append(args, "-i", source, "-lavfi", graph, "-f", "null", "-")

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("ffmpeg vmaf failed: %w: %s", err, tail(stderr.String(), 2000))
	}
	return parseScore(stderr.String())
}

// parseScore extracts the VMAF score from ffmpeg's log output.
func parseScore(log string) (float64, error) {
	m := vmafScore.FindStringSubmatch(log)
	if m == nil {
		return 0, fmt.Errorf("no VMAF score in ffmpeg output: %s", tail(log, 500))
	}
	return strconv.ParseFloat(m[1], 64)
}

// ftoa formats seconds for ffmpeg.
func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
