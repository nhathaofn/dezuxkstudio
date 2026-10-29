package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSeparatesDirectoriesAndHonoursOverrides(t *testing.T) {
	p, err := Resolve("TestApp", Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.Config, p.Data, p.Logs} {
		if !filepath.IsAbs(d) || filepath.Base(filepath.Dir(d)) == "" {
			t.Fatalf("not absolute: %+v", p)
		}
	}
	if p.Data == p.Logs {
		t.Fatal("data and logs must differ")
	}

	tmp := t.TempDir()
	o, err := Resolve("TestApp", Overrides{DataDir: filepath.Join(tmp, "d"), LogDir: filepath.Join(tmp, "l")})
	if err != nil {
		t.Fatal(err)
	}
	if o.Data != filepath.Join(tmp, "d") || o.Logs != filepath.Join(tmp, "l") || o.Config != p.Config {
		t.Fatalf("overrides not applied: %+v", o)
	}
	if err := o.Ensure(); err == nil {
		for _, d := range []string{o.Data, o.Logs, o.Backups(), o.Diagnostics()} {
			if info, err := os.Stat(d); err != nil || !info.IsDir() {
				t.Errorf("missing dir %s", d)
			}
		}
	}

	if _, err := Resolve("", Overrides{}); err == nil {
		t.Fatal("empty app id accepted")
	}
}
