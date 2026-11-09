package transcode

import "strconv"

type Progress struct {
	Frame      int64   `json:"frame"`
	Fps        float64 `json:"fps"`
	Bitrate    string  `json:"bitrate"`
	TotalSize  int64   `json:"total_size"`
	OutTimeUs  int64   `json:"out_time_us"`
	OutTimeMs  int64   `json:"out_time_ms"`
	OutTime    string  `json:"out_time"`
	DupFrames  int64   `json:"dup_frames"`
	DropFrames int64   `json:"drop_frames"`
	Speed      string  `json:"speed"`
	Done       bool    `json:"done"`
}

func (o *Progress) set(key, value string) {
	switch key {
	case "frame":
		o.Frame, _ = strconv.ParseInt(value, 10, 64)
	case "fps":
		o.Fps, _ = strconv.ParseFloat(value, 64)
	case "bitrate":
		o.Bitrate = value
	case "total_size":
		o.TotalSize, _ = strconv.ParseInt(value, 10, 64)
	case "out_time_us":
		o.OutTimeUs, _ = strconv.ParseInt(value, 10, 64)
	case "out_time_ms":
		o.OutTimeMs, _ = strconv.ParseInt(value, 10, 64)
	case "out_time":
		o.OutTime = value
	case "progress":
		o.Done = value == "end"
	case "dup_frames":
		o.DupFrames, _ = strconv.ParseInt(value, 10, 64)
	case "drop_frames":
		o.DropFrames, _ = strconv.ParseInt(value, 10, 64)
	case "speed":
		o.Speed = value
	}
}

func (o *Progress) Percent(durationUs int64) float64 {
	if o.Done {
		return 100
	}
	if durationUs <= 0 {
		return 0
	}
	return min(float64(o.OutTimeUs)/float64(durationUs)*100, 100)
}
