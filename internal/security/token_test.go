package security

import (
	"net/http/httptest"
	"testing"
)

func TestNewTokenIsRandomHex(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if len(a) != TokenBytes*2 || a == b {
		t.Fatalf("tokens %q %q", a, b)
	}
}

func TestTokenEqual(t *testing.T) {
	if !TokenEqual("abc", "abc") || TokenEqual("abc", "abd") || TokenEqual("", "") || TokenEqual("abc", "") {
		t.Fatal("unexpected comparison result")
	}
}

func TestExtractors(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "bearer  tok ")
	r.Header.Add("Sec-WebSocket-Protocol", "dezuxk.v1, auth.sub")
	if got := BearerToken(r); got != "tok" {
		t.Errorf("bearer = %q", got)
	}
	if got := SubprotocolToken(r, "auth."); got != "sub" {
		t.Errorf("subprotocol = %q", got)
	}
}

func TestOriginAllowed(t *testing.T) {
	patterns := []string{"wails://wails", "http://localhost:*", "*.example.com"}
	cases := map[string]bool{
		"wails://wails":           true,
		"http://localhost:5173":   true,
		"https://localhost:5173":  false,
		"https://app.example.com": true,
		"https://evil.com":        false,
		"not a url":               false,
	}
	for origin, want := range cases {
		if got := OriginAllowed(origin, patterns); got != want {
			t.Errorf("%s: got %v", origin, got)
		}
	}
}
