// Package transcode manages ffmpeg-based transcoding for nstream.
package transcode

// Profile describes how ffmpeg should encode output.
type Profile struct {
	Name      string
	VideoArgs []string
	AudioArgs []string
	// HLS-specific
	HLSTime    string
	HLSFlags   string
}

// Profiles is the registry of known codec profiles.
var Profiles = map[string]Profile{
	"hls-h264": {
		Name:      "hls-h264",
		VideoArgs: []string{"-c:v", "libx264", "-preset", "fast", "-crf", "23", "-profile:v", "main", "-level", "4.0"},
		AudioArgs: []string{"-c:a", "aac", "-b:a", "128k", "-ac", "2"},
		HLSTime:   "4",
		HLSFlags:  "delete_segments",
	},
}

// DefaultProfile is used when no profile is specified.
const DefaultProfile = "hls-h264"
