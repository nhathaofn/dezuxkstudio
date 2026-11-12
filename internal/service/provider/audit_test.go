package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	domain "app/internal/domain/provider"
)

func TestRedactMasksCredentialsAndTruncatesRunes(t *testing.T) {
	key := "sk-live+abc/12345678"
	in := "echo " + key + " enc=" + strings.ReplaceAll(strings.ReplaceAll(key, "+", "%2B"), "/", "%2F") +
		" Authorization: Bearer other-token-123456 api_key=zzzzzzzzzz"
	out := redact(in, key)
	for _, leak := range []string{key, "%2Fabc", "other-token-123456", "zzzzzzzzzz"} {
		if strings.Contains(out, leak) {
			t.Fatalf("leak %q in %q", leak, out)
		}
	}
	long := strings.Repeat("é", 300)
	if r := []rune(redact(long, "")); len(r) != 203 {
		t.Fatalf("rune truncation: %d", len(r))
	}
}

func TestValidateBaseURLHardening(t *testing.T) {
	for _, bad := range []string{"https://u:p@x.com/v1", "https://x.com/v1?k=1", "https://x.com/v1#f",
		"http://169.254.169.254/", "http://metadata.google.internal/", "ftp://x"} {
		if domain.ValidateBaseURL(bad) == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
	for _, ok := range []string{"http://localhost:11434/v1", "http://192.168.1.5:1234/v1", "https://api.openai.com/v1"} {
		if err := domain.ValidateBaseURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}

func TestInferGeminiEndpoint(t *testing.T) {
	cases := map[string]domain.Endpoint{
		"veo-3.0-generate-preview":     domain.EndpointGeminiVideo,
		"gemini-2.5-flash-preview-tts": domain.EndpointGeminiTTS,
		"imagen-4.0-generate":          domain.EndpointGeminiImage,
		"gemini-2.5-flash-image":       domain.EndpointGeminiImage,
		"gemini-2.5-pro":               domain.EndpointGeminiGenerate,
	}
	for id, want := range cases {
		if got := InferGeminiEndpoint(id); got != want {
			t.Errorf("%s: %s want %s", id, got, want)
		}
	}
}

func TestProbeViduRejects2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	e := newEnv(t, 2*time.Second)
	res := e.testCred(t, domain.IDVidu, srv.URL, map[string]string{"api_key": probeKey})
	if res.OK() || res.Outcome.Dead() {
		t.Fatalf("2xx must be undecidable, not ok and not dead: %+v", res)
	}
}

func TestEnsureOpenAIBaseMatchesArcReel(t *testing.T) {
	cases := map[string]string{
		"https://x.com":        "https://x.com/v1",
		"https://x.com/v1/":    "https://x.com/v1",
		"https://x.com/v4":     "https://x.com/v4",
		"https://x.com/v1beta": "https://x.com/v1beta/v1",
		"https://x.com/api/v3": "https://x.com/api/v3",
	}
	for in, want := range cases {
		if got := ensureOpenAIBase(in); got != want {
			t.Errorf("%s → %s want %s", in, got, want)
		}
	}
}
