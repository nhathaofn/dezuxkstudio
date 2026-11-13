package mediagen

import (
	"encoding/binary"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"app/internal/apperr"
)

const (
	maxVideoDurationSec = 60
	minTTSSpeed         = 0.25
	maxTTSSpeed         = 4.0
)

func validationErr(key, msg string, params map[string]any) error {
	return apperr.Validation(msg).WithMsg(key, params)
}

// NormalizeImageRequest validates and applies defaults to an image generation request.
func NormalizeImageRequest(req ImageRequest) (ImageRequest, error) {
	req.ModelRef = strings.TrimSpace(req.ModelRef)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.AspectRatio = strings.TrimSpace(req.AspectRatio)
	req.Resolution = strings.TrimSpace(req.Resolution)
	req.OutputDir = strings.TrimSpace(req.OutputDir)
	req.ReferenceImages = trimNonEmpty(req.ReferenceImages)

	if req.Prompt == "" {
		return req, validationErr("errors.mediagen.prompt_required", "Prompt is required", nil)
	}
	if req.AspectRatio == "" {
		req.AspectRatio = DefaultImageAspectRatio
	} else if !slices.Contains(ImageAspectRatios(), req.AspectRatio) {
		if _, _, ok := ParseWxH(req.AspectRatio); !ok {
			return req, validationErr("errors.mediagen.invalid_aspect_ratio", "Unsupported aspect ratio: "+req.AspectRatio, map[string]any{"value": req.AspectRatio})
		}
	}

	if req.Resolution == "" {
		req.Resolution = DefaultImageResolution
	} else {
		upper := strings.ToUpper(req.Resolution)
		if slices.Contains(ImageResolutions(), upper) {
			req.Resolution = upper
		} else if _, _, ok := ParseWxH(req.Resolution); !ok {
			return req, validationErr("errors.mediagen.invalid_resolution", "Unsupported image resolution: "+req.Resolution, map[string]any{"value": req.Resolution})
		}
	}

	if req.Seed != nil && *req.Seed < 0 {
		return req, validationErr("errors.mediagen.invalid_seed", "Seed must be zero or positive", nil)
	}
	return req, nil
}

// NormalizeVideoRequest validates and applies defaults to a video generation request.
func NormalizeVideoRequest(req VideoRequest) (VideoRequest, error) {
	req.ModelRef = strings.TrimSpace(req.ModelRef)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.StartImage = strings.TrimSpace(req.StartImage)
	req.EndImage = strings.TrimSpace(req.EndImage)
	req.AspectRatio = strings.TrimSpace(req.AspectRatio)
	req.Resolution = strings.ToLower(strings.TrimSpace(req.Resolution))
	req.OutputDir = strings.TrimSpace(req.OutputDir)
	req.ReferenceImages = trimNonEmpty(req.ReferenceImages)

	if req.Prompt == "" && req.StartImage == "" && len(req.ReferenceImages) == 0 {
		return req, validationErr("errors.mediagen.prompt_or_image_required", "Prompt or at least one input image is required", nil)
	}
	if req.AspectRatio == "" {
		req.AspectRatio = DefaultVideoAspectRatio
	} else if !slices.Contains(VideoAspectRatios(), req.AspectRatio) {
		return req, validationErr("errors.mediagen.invalid_aspect_ratio", "Unsupported aspect ratio: "+req.AspectRatio, map[string]any{"value": req.AspectRatio})
	}

	if req.Resolution == "" {
		req.Resolution = DefaultVideoResolution
	} else if !slices.Contains(VideoResolutions(), req.Resolution) {
		return req, validationErr("errors.mediagen.invalid_resolution", "Unsupported video resolution: "+req.Resolution, map[string]any{"value": req.Resolution})
	}

	if req.DurationSec == 0 {
		req.DurationSec = DefaultVideoDurationSec
	} else if req.DurationSec < 1 || req.DurationSec > maxVideoDurationSec {
		return req, validationErr("errors.mediagen.invalid_duration", fmt.Sprintf("Duration must be between 1 and %d seconds", maxVideoDurationSec), map[string]any{"max": maxVideoDurationSec})
	}

	if req.Seed != nil && *req.Seed < 0 {
		return req, validationErr("errors.mediagen.invalid_seed", "Seed must be zero or positive", nil)
	}
	return req, nil
}

// NormalizeTTSRequest validates and applies defaults to a TTS synthesis request.
func NormalizeTTSRequest(req TTSRequest) (TTSRequest, error) {
	req.ModelRef = strings.TrimSpace(req.ModelRef)
	req.Text = strings.TrimSpace(req.Text)
	req.Voice = strings.TrimSpace(req.Voice)
	req.Format = strings.ToLower(strings.TrimSpace(req.Format))
	req.Language = strings.TrimSpace(req.Language)
	req.OutputDir = strings.TrimSpace(req.OutputDir)

	if req.Text == "" {
		return req, validationErr("errors.mediagen.text_required", "Text to speak is required", nil)
	}
	if req.Format == "" {
		req.Format = DefaultTTSFormat
	} else if !slices.Contains(TTSFormats(), req.Format) {
		return req, validationErr("errors.mediagen.invalid_audio_format", "Unsupported audio format: "+req.Format, map[string]any{"value": req.Format})
	}
	if req.Speed == 0 {
		req.Speed = DefaultTTSSpeed
	} else if req.Speed < minTTSSpeed || req.Speed > maxTTSSpeed {
		return req, validationErr("errors.mediagen.invalid_speed", fmt.Sprintf("Speed must be between %.2f and %.2f", minTTSSpeed, maxTTSSpeed), nil)
	}
	return req, nil
}

func trimNonEmpty(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, s := range items {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ParseWxH parses pixel dimension strings such as "1024x1024" or "1280*720".
func ParseWxH(s string) (w, h int, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	sep := "x"
	if strings.Contains(s, "*") {
		sep = "*"
	}
	parts := strings.Split(s, sep)
	if len(parts) != 2 {
		return 0, 0, false
	}
	wi, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	hi, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || wi < 64 || hi < 64 || wi > 16384 || hi > 16384 {
		return 0, 0, false
	}
	return wi, hi, true
}

// ImagePixelSize maps aspect ratio and tier ("1K", "2K", "4K", or explicit "WxH") to pixel dimensions
// aligned cleanly to multiples of 64.
func ImagePixelSize(aspectRatio, resolution string) (w, h int) {
	if ew, eh, ok := ParseWxH(resolution); ok {
		return ew, eh
	}
	if ew, eh, ok := ParseWxH(aspectRatio); ok {
		return ew, eh
	}

	var longEdge int
	switch strings.ToUpper(strings.TrimSpace(resolution)) {
	case "4K":
		longEdge = 4096
	case "2K":
		longEdge = 2048
	default:
		longEdge = 1024
	}

	switch aspectRatio {
	case "16:9":
		return snap64(longEdge * 4 / 3), snap64(longEdge * 3 / 4) // 1344x768 @1K, 2688x1536 @2K
	case "9:16":
		return snap64(longEdge * 3 / 4), snap64(longEdge * 4 / 3)
	case "4:3":
		return snap64(longEdge * 8 / 7), snap64(longEdge * 6 / 7)
	case "3:4":
		return snap64(longEdge * 6 / 7), snap64(longEdge * 8 / 7)
	case "3:2":
		return snap64(longEdge * 6 / 5), snap64(longEdge * 4 / 5)
	case "2:3":
		return snap64(longEdge * 4 / 5), snap64(longEdge * 6 / 5)
	case "21:9":
		return snap64(longEdge * 3 / 2), snap64(longEdge * 2 / 3)
	default: // "1:1"
		return longEdge, longEdge
	}
}

// OpenAIImageSize maps aspectRatio/resolution into GPT-Image / DALL-E compatible presets.
func OpenAIImageSize(aspectRatio, resolution string) string {
	if w, h, ok := ParseWxH(resolution); ok {
		return fmt.Sprintf("%dx%d", w, h)
	}
	if w, h, ok := ParseWxH(aspectRatio); ok {
		return fmt.Sprintf("%dx%d", w, h)
	}
	switch aspectRatio {
	case "16:9", "4:3", "3:2", "21:9":
		return "1536x1024"
	case "9:16", "3:4", "2:3":
		return "1024x1536"
	default:
		return "1024x1024"
	}
}

// OpenAIVideoSize maps aspectRatio and video resolution into OpenAI Sora WxH dimensions.
func OpenAIVideoSize(aspectRatio, resolution string) string {
	hiDef := strings.ToLower(strings.TrimSpace(resolution)) == "1080p"
	switch aspectRatio {
	case "9:16", "3:4":
		if hiDef {
			return "1024x1792"
		}
		return "720x1280"
	case "1:1":
		return "1024x1024"
	default:
		if hiDef {
			return "1792x1024"
		}
		return "1280x720"
	}
}

func snap64(v int) int {
	r := ((v + 32) / 64) * 64
	if r < 64 {
		return 64
	}
	return r
}

// ParsePCMMime parses linear PCM MIME headers such as "audio/L16;codec=pcm;rate=24000;channels=1".
func ParsePCMMime(mimeType string) (rate, channels, bits int, isPCM bool) {
	lower := strings.ToLower(strings.TrimSpace(mimeType))
	if !strings.HasPrefix(lower, "audio/l16") && !strings.HasPrefix(lower, "audio/pcm") && !strings.Contains(lower, "codec=pcm") {
		return 0, 0, 0, false
	}
	rate = 24000
	channels = 1
	bits = 16
	if strings.HasPrefix(lower, "audio/l24") {
		bits = 24
	} else if strings.HasPrefix(lower, "audio/l8") {
		bits = 8
	}
	for _, part := range strings.Split(lower, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "rate":
			if v, err := strconv.Atoi(kv[1]); err == nil && v >= 8000 && v <= 192000 {
				rate = v
			}
		case "channels":
			if v, err := strconv.Atoi(kv[1]); err == nil && v >= 1 && v <= 8 {
				channels = v
			}
		}
	}
	return rate, channels, bits, true
}

// WrapPCMToWAV prepends a standard RIFF/WAVE header around raw little-endian PCM bytes.
// If pcm already starts with a RIFF header, it is returned unchanged.
func WrapPCMToWAV(pcm []byte, mimeType string) []byte {
	if len(pcm) >= 4 && string(pcm[:4]) == "RIFF" {
		return pcm
	}
	rate, channels, bits, ok := ParsePCMMime(mimeType)
	if !ok {
		rate, channels, bits = 24000, 1, 16
	}
	byteRate := rate * channels * (bits / 8)
	blockAlign := channels * (bits / 8)
	dataLen := len(pcm)

	out := make([]byte, 44+dataLen)
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(36+dataLen))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(out[20:22], 1)  // AudioFormat = 1 (PCM)
	binary.LittleEndian.PutUint16(out[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(out[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(out[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(out[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(out[34:36], uint16(bits))
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(dataLen))
	copy(out[44:], pcm)
	return out
}

// ExtForMime maps a media MIME type (stripping parameters) to a leading-dot file extension.
func ExtForMime(mimeType, fallbackExt string) string {
	base := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch base {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "video/x-matroska":
		return ".mkv"
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/l16", "audio/pcm":
		return ".wav"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac":
		return ".flac"
	case "audio/aac":
		return ".aac"
	case "audio/mp4":
		return ".m4a"
	case "audio/opus":
		return ".opus"
	default:
		if fallbackExt != "" && !strings.HasPrefix(fallbackExt, ".") {
			return "." + fallbackExt
		}
		return fallbackExt
	}
}

// MimeForExt maps a file extension or format name to a canonical MIME type.
func MimeForExt(ext string) string {
	e := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	switch e {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "mp4", "m4v":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "mov":
		return "video/quicktime"
	case "mkv":
		return "video/x-matroska"
	case "wav", "pcm":
		return "audio/wav"
	case "mp3":
		return "audio/mpeg"
	case "ogg":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	case "aac":
		return "audio/aac"
	case "m4a":
		return "audio/mp4"
	case "opus":
		return "audio/opus"
	default:
		return "application/octet-stream"
	}
}

// SanitizeRemoteURL removes query strings and fragments before logging or diagnostic output
// so signed asset tokens never leak into logs.
func SanitizeRemoteURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
