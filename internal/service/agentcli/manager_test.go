package agentcli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	domainagent "app/internal/domain/agent"
)

// channel is a fake vendor release server whose newest version can be changed between calls.
type channel struct {
	mu      sync.Mutex
	version string
	corrupt bool // serve bytes that do not match the published checksum
}

func (c *channel) set(v string) { c.mu.Lock(); c.version = v; c.mu.Unlock() }
func (c *channel) get() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version, c.corrupt
}

func exeBytes(version string) []byte { return []byte("MZ fake program " + version) }

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names) // the same archive bytes every time, or its published checksum would not match
	for _, name := range names {
		data := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func sum256(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func sum512(b []byte) string { s := sha512.Sum512(b); return hex.EncodeToString(s[:]) }

func newServer(t *testing.T, ch *channel) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	body := func(w http.ResponseWriter, data []byte, corrupt bool) {
		if corrupt {
			data = append([]byte("TAMPERED"), data...)
		}
		_, _ = w.Write(data)
	}
	// version_file scheme (claude)
	mux.HandleFunc("/claude/latest", func(w http.ResponseWriter, r *http.Request) { v, _ := ch.get(); fmt.Fprintln(w, v) })
	mux.HandleFunc("/claude/", func(w http.ResponseWriter, r *http.Request) {
		v, corrupt := ch.get()
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifest.json"):
			fmt.Fprintf(w, `{"platforms":{"win32-x64":{"checksum":%q}}}`, sum256(exeBytes(v)))
		case strings.HasSuffix(r.URL.Path, "/win32-x64/claude.exe"):
			body(w, exeBytes(v), corrupt)
		default:
			http.NotFound(w, r)
		}
	})
	// manifest scheme (agy)
	mux.HandleFunc("/agy/manifests/windows_amd64.json", func(w http.ResponseWriter, r *http.Request) {
		v, _ := ch.get()
		fmt.Fprintf(w, `{"version":%q,"url":"http://%s/agy/bin/agy.exe","sha512":%q}`, v, r.Host, sum512(exeBytes(v)))
	})
	mux.HandleFunc("/agy/bin/agy.exe", func(w http.ResponseWriter, r *http.Request) { v, c := ch.get(); body(w, exeBytes(v), c) })
	// release_package scheme (codex)
	pkg := func(v string) []byte {
		return tarGz(t, map[string][]byte{"bin/codex.exe": exeBytes(v), "codex-package.json": []byte("{}")})
	}
	mux.HandleFunc("/codex/latest", func(w http.ResponseWriter, r *http.Request) {
		v, _ := ch.get()
		fmt.Fprintf(w, `{"tag_name":"rust-v%s"}`, v)
	})
	mux.HandleFunc("/codex/dl/", func(w http.ResponseWriter, r *http.Request) {
		v, corrupt := ch.get()
		switch {
		case strings.HasSuffix(r.URL.Path, "codex-package_SHA256SUMS"):
			fmt.Fprintf(w, "%s  codex-package-x86_64-pc-windows-msvc.tar.gz\n", sum256(pkg(v)))
		case strings.HasSuffix(r.URL.Path, "codex-package-x86_64-pc-windows-msvc.tar.gz"):
			body(w, pkg(v), corrupt)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newManager(t *testing.T, srv *httptest.Server) *Manager {
	t.Helper()
	return New(Options{
		Dir: t.TempDir(), Client: srv.Client(), UserAgent: "test", GOOS: "windows", GOARCH: "amd64",
		Specs: map[domainagent.ProviderID]domainagent.InstallSpec{
			domainagent.ProviderClaude: {
				Scheme: domainagent.SchemeVersionFile, BaseURL: srv.URL + "/claude", LatestPath: "latest",
				Platforms: map[string]string{"windows_amd64": "win32-x64"}, Executable: "claude.exe",
			},
			domainagent.ProviderAntigravity: {
				Scheme: domainagent.SchemeManifest, BaseURL: srv.URL + "/agy",
				Platforms: map[string]string{"windows_amd64": "windows_amd64"}, Executable: "agy.exe",
			},
			domainagent.ProviderCodex: {
				Scheme: domainagent.SchemeReleasePackage, BaseURL: srv.URL + "/codex/dl", LatestAPI: srv.URL + "/codex/latest",
				TagPrefix: "rust-v", Platforms: map[string]string{"windows_amd64": "x86_64-pc-windows-msvc"},
				Executable: "bin/codex.exe", PackageAsset: "codex-package-{platform}.tar.gz", ChecksumAsset: "codex-package_SHA256SUMS",
			},
		},
	})
}

var allIDs = []domainagent.ProviderID{domainagent.ProviderClaude, domainagent.ProviderAntigravity, domainagent.ProviderCodex}

func TestInstallEachSchemeVerifiesAndRuns(t *testing.T) {
	ch := &channel{version: "1.2.3"}
	m := newManager(t, newServer(t, ch))
	for _, id := range allIDs {
		t.Run(string(id), func(t *testing.T) {
			if _, ok := m.Current(id); ok {
				t.Fatal("nothing is installed yet")
			}
			cur, changed, err := m.Install(context.Background(), id)
			if err != nil || !changed || cur.Version != "1.2.3" {
				t.Fatalf("install: %+v changed=%v err=%v", cur, changed, err)
			}
			got, err := os.ReadFile(cur.Path)
			if err != nil || !bytes.Equal(got, exeBytes("1.2.3")) {
				t.Fatalf("program content wrong: %q %v", got, err)
			}
			if rel, err := filepath.Rel(m.dir, cur.Path); err != nil || strings.HasPrefix(rel, "..") {
				t.Fatalf("installed outside the app folder: %s", cur.Path)
			}
		})
	}
}

func TestInstallIsIdempotentAndUpdatesInPlace(t *testing.T) {
	ch := &channel{version: "1.0.0"}
	m := newManager(t, newServer(t, ch))
	ctx := context.Background()
	first, _, err := m.Install(ctx, domainagent.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := m.Install(ctx, domainagent.ProviderCodex); err != nil || changed {
		t.Fatalf("same version again must change nothing: changed=%v err=%v", changed, err)
	}
	ch.set("1.1.0")
	next, changed, err := m.Install(ctx, domainagent.ProviderCodex)
	if err != nil || !changed || next.Version != "1.1.0" {
		t.Fatalf("update: %+v changed=%v err=%v", next, changed, err)
	}
	if cur, _ := m.Current(domainagent.ProviderCodex); cur.Version != "1.1.0" {
		t.Fatalf("current must point at the new version, got %s", cur.Version)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("the previous version must stay for rollback: %v", err)
	}
}

func TestTamperedDownloadNeverBecomesCurrent(t *testing.T) {
	ch := &channel{version: "1.0.0"}
	m := newManager(t, newServer(t, ch))
	ctx := context.Background()
	for _, id := range allIDs {
		if _, _, err := m.Install(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	ch.mu.Lock()
	ch.version, ch.corrupt = "2.0.0", true
	ch.mu.Unlock()
	for _, id := range allIDs {
		t.Run(string(id), func(t *testing.T) {
			if _, _, err := m.Install(ctx, id); err == nil {
				t.Fatal("a download that does not match its checksum must fail")
			}
			cur, ok := m.Current(id)
			if !ok || cur.Version != "1.0.0" {
				t.Fatalf("the working version must stay current, got %+v ok=%v", cur, ok)
			}
			entries, _ := os.ReadDir(m.versionsDir(id))
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".staging") || e.Name() == "2.0.0" {
					t.Fatalf("leftover from the failed install: %s", e.Name())
				}
			}
		})
	}
}

func TestOldVersionsArePruned(t *testing.T) {
	ch := &channel{version: "1.0.0"}
	m := newManager(t, newServer(t, ch))
	ctx := context.Background()
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0"} {
		ch.set(v)
		if _, _, err := m.Install(ctx, domainagent.ProviderAntigravity); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(m.versionsDir(domainagent.ProviderAntigravity))
	if len(entries) != m.keep {
		t.Fatalf("kept %d versions, want %d", len(entries), m.keep)
	}
}

func TestUnsupportedPlatformHasNoManagedCopy(t *testing.T) {
	ch := &channel{version: "1.0.0"}
	srv := newServer(t, ch)
	m := newManager(t, srv)
	m.goarch = "riscv64"
	if m.Supported(domainagent.ProviderClaude) {
		t.Fatal("a platform the channel does not publish must not be managed")
	}
	if _, _, err := m.Install(context.Background(), domainagent.ProviderClaude); err == nil {
		t.Fatal("install must refuse an unsupported platform")
	}
}

func TestVersionFromTheServerCannotEscapeTheFolder(t *testing.T) {
	ch := &channel{version: "../../evil"}
	m := newManager(t, newServer(t, ch))
	for _, id := range allIDs {
		if _, _, err := m.Install(context.Background(), id); err == nil {
			t.Fatalf("%s: a path-like version must be rejected", id)
		}
	}
}

func TestArchiveEntryOutsideTheFolderIsRejected(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archive, tarGz(t, map[string][]byte{"../escape.txt": []byte("x")}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := untarGz(archive, filepath.Join(dir, "out")); err == nil {
		t.Fatal("entry escaping the folder must be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("file was written outside the folder")
	}
}

func TestChecksumLineParsing(t *testing.T) {
	sums := []byte("aaa  other.tar.gz\nbbb *pkg.tar.gz\n")
	if got := checksumFor(sums, "pkg.tar.gz"); got != "bbb" {
		t.Fatalf("got %q", got)
	}
	if checksumFor(sums, "missing") != "" {
		t.Fatal("unknown asset must have no checksum")
	}
}
