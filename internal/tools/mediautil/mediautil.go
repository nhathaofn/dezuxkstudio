// Package mediautil provides shared FFprobe parsing, codec checks, and media extension helpers for tool nodes.
package mediautil

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/runner"
)

// FFprobeResult holds normalized container and stream metadata extracted from ffprobe JSON output.
type FFprobeResult struct {
	Probed      bool
	FormatName  string
	VideoCodec  string
	AudioCodec  string
	HasAudio    bool
	Width       int
	Height      int
	FPS         float64
	DurationSec float64
}

type ffprobeRaw struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		RFrameRate   string `json:"r_frame_rate"`
		AvgFrameRate string `json:"avg_frame_rate"`
		Duration     string `json:"duration"`
	} `json:"streams"`
}

var imageExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".webp": {}, ".bmp": {}, ".gif": {},
}

var videoExts = map[string]struct{}{
	".mp4": {}, ".mov": {}, ".webm": {}, ".mkv": {}, ".avi": {}, ".m4v": {}, ".mpeg": {}, ".mpg": {}, ".ts": {},
}

var audioExts = map[string]struct{}{
	".mp3": {}, ".wav": {}, ".aac": {}, ".m4a": {}, ".ogg": {}, ".opus": {}, ".flac": {}, ".wma": {},
}

// IsSupportedImageExt reports whether path has a supported image file extension.
func IsSupportedImageExt(path string) bool {
	_, ok := imageExts[strings.ToLower(filepath.Ext(path))]
	return ok
}

// IsSupportedVideoExt reports whether path has a supported video file extension.
func IsSupportedVideoExt(path string) bool {
	_, ok := videoExts[strings.ToLower(filepath.Ext(path))]
	return ok
}

// IsSupportedAudioExt reports whether path has a supported audio file extension.
func IsSupportedAudioExt(path string) bool {
	_, ok := audioExts[strings.ToLower(filepath.Ext(path))]
	return ok
}

// ParseFFprobeRate parses rational frame rate strings like "24000/1001" or "30".
func ParseFFprobeRate(raw string) float64 {
	s := strings.TrimSpace(raw)
	if s == "" || s == "0/0" {
		return 0
	}
	if numStr, denStr, ok := strings.Cut(s, "/"); ok {
		num, err1 := strconv.ParseFloat(numStr, 64)
		den, err2 := strconv.ParseFloat(denStr, 64)
		if err1 == nil && err2 == nil && den > 0 {
			return num / den
		}
		return 0
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return f
	}
	return 0
}

// ProbeFile runs ffprobe on filePath and returns normalized stream/container metadata.
func ProbeFile(ctx context.Context, ffprobeExe, filePath string) (FFprobeResult, error) {
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	}
	res, err := runner.RunWithOptions(ctx, ffprobeExe, args, runner.RunOptions{})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return FFprobeResult{}, err
		}
		return FFprobeResult{}, apperr.Wrap(apperr.CodeIntegrity, "Media file could not be inspected or is corrupted", err).
			WithMsg("errors.studio.media_corrupt", nil)
	}

	var raw ffprobeRaw
	if uErr := json.Unmarshal([]byte(res.Stdout), &raw); uErr != nil {
		if sec, perr := strconv.ParseFloat(strings.TrimSpace(res.Stdout), 64); perr == nil && sec > 0 {
			return FFprobeResult{Probed: true, DurationSec: sec}, nil
		}
		return FFprobeResult{}, apperr.Wrap(apperr.CodeIntegrity, "Media probe output was unreadable or corrupted", uErr).
			WithMsg("errors.studio.media_corrupt", nil)
	}

	out := FFprobeResult{
		Probed:     true,
		FormatName: raw.Format.FormatName,
	}
	if d, err := strconv.ParseFloat(strings.TrimSpace(raw.Format.Duration), 64); err == nil && d > 0 {
		out.DurationSec = d
	}

	for _, st := range raw.Streams {
		switch st.CodecType {
		case "video":
			if out.VideoCodec == "" {
				out.VideoCodec = st.CodecName
			}
			if out.Width == 0 && st.Width > 0 {
				out.Width = st.Width
				out.Height = st.Height
			}
			if out.FPS == 0 {
				out.FPS = ParseFFprobeRate(st.AvgFrameRate)
				if out.FPS == 0 {
					out.FPS = ParseFFprobeRate(st.RFrameRate)
				}
			}
			if out.DurationSec == 0 {
				if d, err := strconv.ParseFloat(strings.TrimSpace(st.Duration), 64); err == nil && d > 0 {
					out.DurationSec = d
				}
			}
		case "audio":
			out.HasAudio = true
			if out.AudioCodec == "" {
				out.AudioCodec = st.CodecName
			}
			if out.DurationSec == 0 {
				if d, err := strconv.ParseFloat(strings.TrimSpace(st.Duration), 64); err == nil && d > 0 {
					out.DurationSec = d
				}
			}
		}
	}
	if len(raw.Streams) == 0 && out.FormatName == "" && out.DurationSec == 0 {
		return FFprobeResult{}, apperr.New(apperr.CodeIntegrity, "Media file contains no readable streams").
			WithMsg("errors.studio.media_corrupt", nil)
	}
	return out, nil
}

// IsMP4CompatibleAudioCodec reports whether codec can be stream-copied cleanly into an MP4 container.
func IsMP4CompatibleAudioCodec(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "aac", "mp3", "alac", "ac3", "eac3":
		return true
	default:
		return false
	}
}

// MP4AudioEncodeArgs returns FFmpeg audio flags that copy MP4-safe audio codecs, omit missing audio, or transcode to AAC.
func MP4AudioEncodeArgs(meta FFprobeResult, aacBitrate string) []string {
	if !meta.Probed {
		return []string{"-c:a", "copy"}
	}
	if !meta.HasAudio {
		return []string{"-an"}
	}
	if IsMP4CompatibleAudioCodec(meta.AudioCodec) {
		return []string{"-c:a", "copy"}
	}
	if strings.TrimSpace(aacBitrate) == "" {
		aacBitrate = "192k"
	}
	return []string{"-c:a", "aac", "-b:a", aacBitrate}
}
