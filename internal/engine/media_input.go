package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ImageInputRef accepts either a JSON string path or an engine.MediaRef object.
type ImageInputRef struct {
	MediaRef
}

// InputRef builds an ImageInputRef from a local file path or URL, inferring MediaKind when recognized.
func InputRef(localURL string, fallback MediaKind) ImageInputRef {
	s := strings.TrimSpace(localURL)
	if s == "" {
		return ImageInputRef{}
	}
	return ImageInputRef{MediaRef: RefFor(s, fallback)}
}

// InputRefs builds an ImageInputRefs slice from local file paths or URLs, omitting empty entries.
func InputRefs(localURLs []string, fallback MediaKind) ImageInputRefs {
	if len(localURLs) == 0 {
		return nil
	}
	out := make([]MediaRef, 0, len(localURLs))
	for _, raw := range localURLs {
		if s := strings.TrimSpace(raw); s != "" {
			out = append(out, RefFor(s, fallback))
		}
	}
	return out
}

// UnmarshalJSON decodes a string path (trimmed, inferred via RefFor) or a MediaRef object,
// normalizing an empty Kind to MediaImage when LocalURL is non-empty.
func (r *ImageInputRef) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}

	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		if s = strings.TrimSpace(s); s != "" {
			r.MediaRef = RefFor(s, MediaImage)
		} else {
			r.MediaRef = MediaRef{}
		}
		return nil
	}

	var ref MediaRef
	if err := json.Unmarshal(trimmed, &ref); err != nil {
		return err
	}
	ref.LocalURL = strings.TrimSpace(ref.LocalURL)
	ref.Kind = MediaKind(strings.TrimSpace(string(ref.Kind)))
	if ref.LocalURL != "" && ref.Kind == "" {
		ref.Kind = MediaImage
	}
	r.MediaRef = ref
	return nil
}

// ImageInputRefs accepts a single string or MediaRef, or a JSON array of strings and/or MediaRefs.
type ImageInputRefs []MediaRef

// UnmarshalJSON decodes null, a single string/MediaRef, or a mixed array of strings and MediaRefs.
func (r *ImageInputRefs) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*r = nil
		return nil
	}

	var singleStr string
	if err := json.Unmarshal(trimmed, &singleStr); err == nil {
		if s := strings.TrimSpace(singleStr); s != "" {
			*r = []MediaRef{RefFor(s, MediaImage)}
		} else {
			*r = nil
		}
		return nil
	}

	var singleRef ImageInputRef
	if err := json.Unmarshal(trimmed, &singleRef); err == nil && singleRef.LocalURL != "" {
		*r = []MediaRef{singleRef.MediaRef}
		return nil
	}

	var rawList []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawList); err != nil {
		return fmt.Errorf("expected image list or path: %w", err)
	}

	out := make([]MediaRef, 0, len(rawList))
	for _, item := range rawList {
		var img ImageInputRef
		if err := json.Unmarshal(item, &img); err != nil {
			return err
		}
		if img.LocalURL != "" {
			out = append(out, img.MediaRef)
		}
	}
	*r = out
	return nil
}
