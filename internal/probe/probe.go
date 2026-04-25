// Package probe runs ffprobe against a URL and returns structured metadata.
package probe

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Result holds the metadata extracted by ffprobe.
type Result struct {
	Title    string  // from format or stream tags
	Duration float64 // seconds; 0 if unknown

	// Codec info (populated when ffprobe can read the stream)
	VideoCodec    string // e.g. "h264", "hevc", "vp9", "av1"
	VideoProfile  string // e.g. "High", "Main", "Baseline"
	VideoLevel    int    // H.264/HEVC level × 10, e.g. 40 = 4.0, 41 = 4.1
	Width         int
	Height        int
	AudioCodec    string // e.g. "aac", "ac3", "dts", "opus", "mp3"
	AudioChannels int    // 1=mono 2=stereo 6=5.1 etc.
}

// CanCopyVideo returns true when the video stream can be remuxed into MPEG-TS
// without re-encoding (H.264, any profile/level — modern browsers + hls.js
// handle all common levels).
func (r *Result) CanCopyVideo() bool {
	return r != nil && r.VideoCodec == "h264"
}

// CanCopyAudio returns true when the audio stream can be remuxed without
// re-encoding (AAC stereo or mono only; surround needs downmix).
func (r *Result) CanCopyAudio() bool {
	return r != nil && r.AudioCodec == "aac" && r.AudioChannels <= 2
}

// ffprobeOutput mirrors the JSON ffprobe emits for -show_format -show_streams.
type ffprobeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Profile   string `json:"profile"`
		Level     int    `json:"level"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Channels  int    `json:"channels"`
		Tags      map[string]string `json:"tags"`
	} `json:"streams"`
}

// URL runs ffprobe against the given URL and returns extracted metadata
// including codec details.  The call is bounded by a 30 s timeout.
func URL(ctx context.Context, ffprobeBin, url string) (*Result, error) {
	if ffprobeBin == "" {
		ffprobeBin = "ffprobe"
	}
	bin, err := exec.LookPath(ffprobeBin)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		url,
	).Output()
	if err != nil {
		return nil, err
	}

	var raw ffprobeOutput
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}

	res := &Result{}

	// Duration.
	if d := raw.Format.Duration; d != "" && d != "N/A" {
		res.Duration, _ = strconv.ParseFloat(strings.TrimSpace(d), 64)
	}

	// Title: prefer format-level tag, fall back to first stream with a title.
	if t := tagCI(raw.Format.Tags, "title"); t != "" {
		res.Title = t
	}

	for _, s := range raw.Streams {
		if res.Title == "" {
			if t := tagCI(s.Tags, "title"); t != "" {
				res.Title = t
			}
		}
		switch s.CodecType {
		case "video":
			if res.VideoCodec == "" { // take the first video stream
				res.VideoCodec = s.CodecName
				res.VideoProfile = s.Profile
				res.VideoLevel = s.Level
				res.Width = s.Width
				res.Height = s.Height
			}
		case "audio":
			if res.AudioCodec == "" { // take the first audio stream
				res.AudioCodec = s.CodecName
				res.AudioChannels = s.Channels
			}
		}
	}

	return res, nil
}

// tagCI does a case-insensitive lookup in a tag map.
func tagCI(m map[string]string, key string) string {
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}
