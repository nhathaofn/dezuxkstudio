package provider

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every errors.provider.* key passed to WithMsg in the provider feature must exist in both frontend
// dictionaries, otherwise the UI silently falls back to the English message.
func TestProviderMsgKeysExistInFrontend(t *testing.T) {
	keyRe := regexp.MustCompile(`WithMsg\(\s*"(errors\.provider\.[a-z0-9_]+)"`)
	// Keys passed through a helper parameter (bad(key, ...)) appear as plain literals.
	litRe := regexp.MustCompile(`"(errors\.provider\.[a-z0-9_]+)"`)

	var files []string
	for _, pat := range []string{
		"../../domain/provider/*.go",
		"../../service/provider/*.go",
		"../../tools/provider/*.go",
		"../../contracts/providers.go",
	} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	keys := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{keyRe, litRe} {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				keys[m[1]] = true
			}
		}
	}
	if len(keys) == 0 {
		t.Fatal("no errors.provider.* keys found; the scan is broken")
	}
	for _, dict := range []string{"en.ts", "vi.ts"} {
		b, err := os.ReadFile(filepath.Join("../../../frontend/src/lib/i18n", dict))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for k := range keys {
			if !strings.Contains(src, `"`+k+`":`) {
				t.Errorf("%s is missing translation key %s", dict, k)
			}
		}
	}
}
