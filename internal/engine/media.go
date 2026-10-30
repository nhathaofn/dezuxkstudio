package engine

import (
	"encoding/json"
	"mime"
	"path"
	"strings"
)

// MediaKind tells an image from a video.
type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaVideo MediaKind = "video"
)

// MediaRef is the value an image, video or media port carries: a file path or local URL.
type MediaRef struct {
	LocalURL string    `json:"localUrl"`
	Kind     MediaKind `json:"kind"`
	MimeType string    `json:"mimeType,omitempty"`
}

var videoTypes = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/x-m4v", ".mov": "video/quicktime", ".webm": "video/webm",
}

// RefFor describes a stored file from its path/name.
func RefFor(localURL string, kind MediaKind) MediaRef {
	ext := strings.ToLower(path.Ext(localURL))
	mimeType := videoTypes[ext]
	if mimeType == "" {
		mimeType = strings.TrimSpace(strings.Split(mime.TypeByExtension(ext), ";")[0])
	}
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		kind = MediaImage
	case strings.HasPrefix(mimeType, "video/"):
		kind = MediaVideo
	}
	return MediaRef{LocalURL: localURL, Kind: kind, MimeType: mimeType}
}

// Accepts reports whether a port of type t can take this file.
func (m MediaRef) Accepts(t PortType) bool {
	switch t {
	case PortMedia:
		return m.Kind == MediaImage || m.Kind == MediaVideo
	case PortImage:
		return m.Kind == MediaImage
	case PortVideo:
		return m.Kind == MediaVideo
	}
	return false
}

// Compatible reports whether an output of type from may feed an input of type to.
func Compatible(from, to PortType) bool {
	if from == to {
		return true
	}
	return to == PortMedia && (from == PortImage || from == PortVideo)
}

// Texts is a text port that takes one value or many.
type Texts []string

// UnmarshalJSON accepts a string or a list of strings.
func (t *Texts) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*t = Texts{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*t = many
	return nil
}

// NonEmpty drops blank entries and surrounding space.
func (t Texts) NonEmpty() []string {
	out := make([]string, 0, len(t))
	for _, s := range t {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
