package transcode

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"nstream/internal/probe"
)

// TranscodeMode describes how a video should be processed for HLS output.
type TranscodeMode int

const (
	// ModeCopy: H.264 video + AAC audio → remux only, zero re-encoding.
	ModeCopy TranscodeMode = iota
	// ModeAudioXcode: H.264 video (copy) but audio needs re-encoding.
	ModeAudioXcode
	// ModeVAAPI: GPU-accelerated H.264 via VAAPI (Intel/AMD on Linux).
	ModeVAAPI
	// ModeNVENC: GPU-accelerated H.264 via NVENC (NVIDIA).
	ModeNVENC
	// ModeSoftware: CPU libx264, slowest but always available.
	ModeSoftware
)

func (m TranscodeMode) String() string {
	switch m {
	case ModeCopy:
		return "stream-copy"
	case ModeAudioXcode:
		return "audio-transcode (video copy)"
	case ModeVAAPI:
		return "vaapi"
	case ModeNVENC:
		return "nvenc"
	default:
		return "software (libx264)"
	}
}

// Capabilities holds hardware-acceleration support detected at startup.
type Capabilities struct {
	VAAPI       bool
	VAAPIDevice string // e.g. "/dev/dri/renderD128"
	NVENC       bool
}

// DetectCapabilities checks for VAAPI and NVENC support by inspecting DRI
// device nodes and querying the installed ffmpeg encoder list.  Called once
// at startup; results are cached in Manager.
func DetectCapabilities(ffmpegBin string, log *slog.Logger) Capabilities {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	var caps Capabilities

	// Fetch encoder list once and reuse for both checks.
	encoders, _ := exec.Command(ffmpegBin, "-hide_banner", "-encoders").Output()

	// ── VAAPI (Intel / AMD on Linux) ─────────────────────────────────────
	for _, dev := range []string{
		"/dev/dri/renderD128",
		"/dev/dri/renderD129",
		"/dev/dri/renderD130",
	} {
		if _, err := os.Stat(dev); err == nil {
			caps.VAAPIDevice = dev
			break
		}
	}
	if caps.VAAPIDevice != "" {
		if strings.Contains(string(encoders), "h264_vaapi") {
			if testHardwareEncoder(ffmpegBin, "vaapi", caps.VAAPIDevice) {
				caps.VAAPI = true
				log.Info("hls: VAAPI hardware encoding available", "device", caps.VAAPIDevice)
			} else {
				log.Warn("hls: VAAPI present but unusable, disabling")
				caps.VAAPIDevice = ""
			}
		} else {
			log.Debug("hls: VAAPI device found but ffmpeg lacks h264_vaapi encoder", "device", caps.VAAPIDevice)
			caps.VAAPIDevice = ""
		}
	}

	// ── NVENC (NVIDIA) ───────────────────────────────────────────────────
	if strings.Contains(string(encoders), "h264_nvenc") {
		if testHardwareEncoder(ffmpegBin, "nvenc", "") {
			caps.NVENC = true
			log.Info("hls: NVENC hardware encoding available")
		} else {
			log.Warn("hls: NVENC present but unusable, disabling")
		}
	}

	if !caps.VAAPI && !caps.NVENC {
		log.Debug("hls: no hardware encoder found, using software (libx264)")
	}

	return caps
}

// testHardwareEncoder runs a tiny synthetic encode to verify runtime usability.
// Many systems advertise encoders in `ffmpeg -encoders` that still fail at
// actual encode startup due to missing driver/entrypoint support.
func testHardwareEncoder(ffmpegBin, mode, vaapiDevice string) bool {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	nullTarget := "/dev/null"
	if _, err := os.Stat("/dev/null"); err != nil {
		nullTarget = filepath.Join(os.TempDir(), "nstream-encoder-test-null.ts")
		defer os.Remove(nullTarget) //nolint:errcheck
	}

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=128x72:rate=1",
		"-t", "0.5",
	}
	switch mode {
	case "vaapi":
		if vaapiDevice == "" {
			return false
		}
		args = append(args,
			"-vaapi_device", vaapiDevice,
			"-vf", "format=nv12,hwupload",
			"-c:v", "h264_vaapi",
		)
	case "nvenc":
		args = append(args, "-c:v", "h264_nvenc")
	default:
		return false
	}
	args = append(args, "-f", "null", nullTarget)
	return exec.Command(ffmpegBin, args...).Run() == nil
}

// SelectMode picks the fastest transcoding mode that produces a browser-
// compatible H.264/AAC HLS stream:
//
//	stream-copy  →  audio-transcode  →  vaapi  →  nvenc  →  software
func SelectMode(info *probe.Result, caps Capabilities) TranscodeMode {
	if info != nil {
		if info.CanCopyVideo() {
			if info.CanCopyAudio() {
				return ModeCopy      // already H.264+AAC stereo → just remux
			}
			return ModeAudioXcode  // H.264 video OK, audio needs conversion
		}
	}
	// Video needs re-encoding; use the best available encoder.
	if caps.VAAPI {
		return ModeVAAPI
	}
	if caps.NVENC {
		return ModeNVENC
	}
	return ModeSoftware
}

// BuildFFmpegArgs constructs the ffmpeg codec/filter arguments for the chosen
// mode.  It returns:
//   - preInput:  args that must appear BEFORE -i (e.g. -vaapi_device)
//   - postInput: codec / filter args that follow -i
//
// The caller is responsible for assembling the full command:
//
//	ffmpeg -y [preInput] -i pipe:0 [postInput] [hls muxer args]
func BuildFFmpegArgs(mode TranscodeMode, info *probe.Result, caps Capabilities) (preInput, postInput []string) {
	// Audio: copy when already AAC stereo/mono, otherwise downmix to AAC.
	var audioArgs []string
	if info != nil && info.CanCopyAudio() {
		audioArgs = []string{"-c:a", "copy"}
	} else {
		audioArgs = []string{"-c:a", "aac", "-b:a", "128k", "-ac", "2"}
	}

	switch mode {
	case ModeCopy, ModeAudioXcode:
		// Video stream copy; audio may or may not be copied (see above).
		return nil, append([]string{"-c:v", "copy"}, audioArgs...)

	case ModeVAAPI:
		pre := []string{"-vaapi_device", caps.VAAPIDevice}
		post := append([]string{
			"-vf", "format=nv12,hwupload",
			"-c:v", "h264_vaapi",
			// Some VAAPI drivers expose only Main/Baseline encode entrypoints.
			// Leaving profile implicit lets ffmpeg pick High, which then fails
			// with "No usable encoding entrypoint found for VAProfileH264High".
			"-profile:v", "main",
			"-level", "4.0",
			"-qp", "23",
		}, audioArgs...)
		return pre, post

	case ModeNVENC:
		post := append([]string{
			"-c:v", "h264_nvenc",
			"-preset", "p4",
			"-cq", "23",
			"-profile:v", "main",
		}, audioArgs...)
		return nil, post

	default: // ModeSoftware
		// Limit to 2 threads so one session doesn't saturate all CPU cores.
		post := append([]string{
			"-c:v", "libx264",
			"-preset", "fast",
			"-crf", "23",
			"-profile:v", "main",
			"-level", "4.0",
			"-threads", "2",
		}, audioArgs...)
		return nil, post
	}
}
