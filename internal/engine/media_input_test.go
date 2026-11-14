package engine

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestImageInputRef_UnmarshalAndMarshal(t *testing.T) {
	t.Run("single string and whitespace trim", func(t *testing.T) {
		var r ImageInputRef
		if err := json.Unmarshal([]byte(`"  /media/photo.jpg  "`), &r); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.LocalURL != "/media/photo.jpg" || r.Kind != MediaImage || r.MimeType != "image/jpeg" {
			t.Fatalf("got %+v", r.MediaRef)
		}
	})

	t.Run("empty and whitespace string yields zero MediaRef", func(t *testing.T) {
		for _, raw := range []string{`""`, `"   "`} {
			r := ImageInputRef{MediaRef: MediaRef{LocalURL: "prev.png", Kind: MediaImage}}
			if err := json.Unmarshal([]byte(raw), &r); err != nil {
				t.Fatalf("%s: unexpected error: %v", raw, err)
			}
			if r.MediaRef != (MediaRef{}) {
				t.Fatalf("%s: expected zero MediaRef, got %+v", raw, r.MediaRef)
			}
		}
	})

	t.Run("null leaves zero value without error", func(t *testing.T) {
		for _, raw := range []string{`null`, `  null  `} {
			var r ImageInputRef
			if err := json.Unmarshal([]byte(raw), &r); err != nil {
				t.Fatalf("%s: unexpected error: %v", raw, err)
			}
			if r.MediaRef != (MediaRef{}) {
				t.Fatalf("%s: expected zero MediaRef, got %+v", raw, r.MediaRef)
			}
		}
	})

	t.Run("MediaRef object normalizes empty kind and trims localUrl", func(t *testing.T) {
		var r ImageInputRef
		if err := json.Unmarshal([]byte(`{"localUrl": "  /media/frame.png  ", "kind": "  ", "mimeType": "image/png"}`), &r); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := MediaRef{LocalURL: "/media/frame.png", Kind: MediaImage, MimeType: "image/png"}
		if r.MediaRef != want {
			t.Fatalf("got %+v want %+v", r.MediaRef, want)
		}
	})

	t.Run("MediaRef object preserves explicit kind", func(t *testing.T) {
		var r ImageInputRef
		if err := json.Unmarshal([]byte(`{"localUrl": "clip.mp4", "kind": "video"}`), &r); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Kind != MediaVideo || r.LocalURL != "clip.mp4" {
			t.Fatalf("got %+v", r.MediaRef)
		}
	})

	t.Run("MediaRef object with blank localUrl stays empty", func(t *testing.T) {
		var r ImageInputRef
		if err := json.Unmarshal([]byte(`{"localUrl": "   "}`), &r); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.LocalURL != "" || r.Kind != "" {
			t.Fatalf("expected empty LocalURL and Kind, got %+v", r.MediaRef)
		}
	})

	t.Run("invalid JSON types return error", func(t *testing.T) {
		for _, raw := range []string{`123`, `true`, `["a.png"]`} {
			var r ImageInputRef
			if err := json.Unmarshal([]byte(raw), &r); err == nil {
				t.Fatalf("%s: expected error, got %+v", raw, r)
			}
		}
	})

	t.Run("marshal and unmarshal roundtrip in struct", func(t *testing.T) {
		type payload struct {
			Image  ImageInputRef  `json:"image,omitempty"`
			Images ImageInputRefs `json:"images,omitempty"`
		}
		in := payload{
			Image:  ImageInputRef{MediaRef: MediaRef{LocalURL: "one.png", Kind: MediaImage, MimeType: "image/png"}},
			Images: ImageInputRefs{{LocalURL: "two.jpg", Kind: MediaImage}},
		}
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var out payload
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("roundtrip mismatch: got %+v want %+v", out, in)
		}
	})
}

func TestImageInputRefs_UnmarshalJSON(t *testing.T) {
	t.Run("null and blank strings", func(t *testing.T) {
		for _, raw := range []string{`null`, `  null  `, `""`, `"   "`} {
			refs := ImageInputRefs{{LocalURL: "stale.png", Kind: MediaImage}}
			if err := json.Unmarshal([]byte(raw), &refs); err != nil {
				t.Fatalf("%s: unexpected error: %v", raw, err)
			}
			if len(refs) != 0 {
				t.Fatalf("%s: expected empty refs, got %+v", raw, refs)
			}
		}
	})

	t.Run("single string", func(t *testing.T) {
		var refs ImageInputRefs
		if err := json.Unmarshal([]byte(`"  /media/pic.png  "`), &refs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := ImageInputRefs{RefFor("/media/pic.png", MediaImage)}
		if !reflect.DeepEqual(refs, want) {
			t.Fatalf("got %+v want %+v", refs, want)
		}
	})

	t.Run("single MediaRef normalizes empty kind and trims localUrl", func(t *testing.T) {
		var refs ImageInputRefs
		if err := json.Unmarshal([]byte(`{"localUrl": "  a.webp  "}`), &refs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := ImageInputRefs{{LocalURL: "a.webp", Kind: MediaImage}}
		if !reflect.DeepEqual(refs, want) {
			t.Fatalf("got %+v want %+v", refs, want)
		}
	})

	t.Run("empty array", func(t *testing.T) {
		var refs ImageInputRefs
		if err := json.Unmarshal([]byte(`[]`), &refs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if refs == nil || len(refs) != 0 {
			t.Fatalf("expected non-nil empty slice, got %#v", refs)
		}
	})

	t.Run("array of strings skips blanks", func(t *testing.T) {
		var refs ImageInputRefs
		if err := json.Unmarshal([]byte(`["a.png", "  ", "  b.jpg  ", ""]`), &refs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := ImageInputRefs{
			RefFor("a.png", MediaImage),
			RefFor("b.jpg", MediaImage),
		}
		if !reflect.DeepEqual(refs, want) {
			t.Fatalf("got %+v want %+v", refs, want)
		}
	})

	t.Run("array of MediaRefs normalizes empty kind and skips blank localUrl", func(t *testing.T) {
		var refs ImageInputRefs
		if err := json.Unmarshal([]byte(`[{"localUrl": "a.png"}, {"localUrl": "   "}, {"localUrl": "b.png", "kind": "image", "mimeType": "image/png"}]`), &refs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := ImageInputRefs{
			{LocalURL: "a.png", Kind: MediaImage},
			{LocalURL: "b.png", Kind: MediaImage, MimeType: "image/png"},
		}
		if !reflect.DeepEqual(refs, want) {
			t.Fatalf("got %+v want %+v", refs, want)
		}
	})

	t.Run("mixed array of strings, MediaRefs, nulls and blanks", func(t *testing.T) {
		var refs ImageInputRefs
		raw := `["first.png", null, {"localUrl": "  second.jpg  "}, "", {"localUrl": "  "}, {"localUrl": "third.webp", "kind": "image"}]`
		if err := json.Unmarshal([]byte(raw), &refs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := ImageInputRefs{
			RefFor("first.png", MediaImage),
			{LocalURL: "second.jpg", Kind: MediaImage},
			{LocalURL: "third.webp", Kind: MediaImage},
		}
		if !reflect.DeepEqual(refs, want) {
			t.Fatalf("got %+v want %+v", refs, want)
		}
	})

	t.Run("invalid inputs return error", func(t *testing.T) {
		for _, raw := range []string{`123`, `{}`, `{"localUrl": ""}`, `{"other": "x"}`, `["ok.png", 42]`, `[{"localUrl": 123}]`} {
			var refs ImageInputRefs
			if err := json.Unmarshal([]byte(raw), &refs); err == nil {
				t.Fatalf("%s: expected error, got %+v", raw, refs)
			}
		}
	})
}
