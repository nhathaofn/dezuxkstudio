package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/engine"
	"app/internal/tools/watermark"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type accessFixture struct {
	base, ws, media, profiles, user, system string
	svc                                     *Service
	wf                                      *fakeWorkflow
	cs                                      *mcp.ClientSession
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newAccessFixture builds a Service whose OS-protected dirs are replaced by a fake "system" folder, because
// creating test files under C:\Windows is not possible.
func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	base := t.TempDir()
	f := &accessFixture{
		base:     base,
		ws:       filepath.Join(base, "data", "agent-workspace"),
		media:    filepath.Join(base, "data", "media"),
		profiles: filepath.Join(base, "data", "agent-profiles"),
		user:     filepath.Join(base, "Users", "me", "Pictures"),
		system:   filepath.Join(base, "Windows"),
	}
	for _, d := range []string{f.ws, f.media, f.profiles, f.user, f.system} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.wf = &fakeWorkflow{specs: []engine.Spec{watermark.NewNode(watermark.Options{}).Spec()}}
	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      filepath.Join(base, "data"),
		WorkspaceDir: f.ws,
		MediaDir:     f.media,
		ProfilesDir:  f.profiles,
		Logger:       slog.New(slog.DiscardHandler),
		Workflow:     f.wf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	svc.policy.protected = []string{f.profiles, filepath.Join(base, "data", "logs"), f.system}
	f.svc = svc

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	f.cs = cs
	return f
}

func (f *accessFixture) call(t *testing.T, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := f.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var payload map[string]any
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			_ = json.Unmarshal([]byte(tc.Text), &payload)
			break
		}
	}
	return payload, res.IsError
}

func TestAccessPolicyScopedToRootsAndBlocksSensitive(t *testing.T) {
	f := newAccessFixture(t)
	p := f.svc.policy

	// Outside workspace/media/trusted folders, access is denied by default.
	for _, outside := range []string{
		filepath.Join(f.user, "story.md"),
		filepath.Join(f.user, "a.png"),
	} {
		if _, err := p.ValidateRead(outside); err == nil {
			t.Errorf("expected read denied outside project roots for %s", outside)
		}
		if _, err := p.ValidateWrite(filepath.Join(f.user, "kich-ban.md")); err == nil {
			t.Errorf("expected write denied outside project roots")
		}
	}
	if _, err := p.ValidateDir(f.user); err == nil {
		t.Errorf("expected dir listing denied outside project roots")
	}

	// Inside workspace and media roots, allowed content & media files work while code/secrets stay denied.
	allowedRead := []string{
		filepath.Join(f.ws, "story.md"), filepath.Join(f.ws, "sub.srt"), filepath.Join(f.ws, "notes.txt"),
		filepath.Join(f.media, "a.png"), filepath.Join(f.media, "clip.mp4"), filepath.Join(f.media, "x.webp"),
	}
	for _, path := range allowedRead {
		if _, err := p.ValidateRead(path); err != nil {
			t.Errorf("expected read allowed inside project roots for %s: %v", path, err)
		}
	}
	deniedRead := []string{
		filepath.Join(f.ws, "main.py"), filepath.Join(f.ws, "app.ts"), filepath.Join(f.ws, "run.ps1"),
		filepath.Join(f.ws, "tool.exe"), filepath.Join(f.ws, "page.html"), filepath.Join(f.ws, ".env"),
		filepath.Join(f.ws, "id_rsa"), filepath.Join(f.ws, "cert.pem"), filepath.Join(f.ws, "package.json"),
		filepath.Join(f.ws, ".ssh", "config.txt"), filepath.Join(f.ws, "repo", ".git", "notes.txt"),
		filepath.Join(f.ws, "node_modules", "readme.md"), filepath.Join(f.system, "notes.txt"),
		filepath.Join(f.profiles, "codex", "notes.txt"), filepath.Join(f.base, "data", "app.db"),
		filepath.Join(f.ws, "report.pdf"), // documents use workspace_read_document instead
	}
	for _, path := range deniedRead {
		if _, err := p.ValidateRead(path); err == nil {
			t.Errorf("expected read denied for %s", path)
		}
	}

	if _, err := p.ValidateWrite(filepath.Join(f.ws, "kich-ban.md")); err != nil {
		t.Errorf("expected writing a script in workspace to be allowed: %v", err)
	}
	for _, bad := range []string{"index.html", "a.bat", "settings.yaml", "tsconfig.json", "requirements.txt"} {
		if _, err := p.ValidateWrite(filepath.Join(f.ws, bad)); err == nil {
			t.Errorf("expected write of %s to be denied", bad)
		}
	}

	moves := []struct {
		src, dst string
		ok       bool
	}{
		{"a.jpg", "b.png", true},
		{"notes.txt", "notes.md", true},
		{"report.pdf", "archive/report.pdf", true},
		{"notes.txt", "notes.hta", false},
		{"notes.txt", "notes", false},
		{"a.jpg", "a.mp4", false},
		{"report.pdf", "report.docx", false},
		{"main.py", "main.txt", false},
	}
	for _, m := range moves {
		_, _, err := p.ValidateMove(filepath.Join(f.ws, m.src), filepath.Join(f.ws, m.dst))
		if (err == nil) != m.ok {
			t.Errorf("move %s → %s: ok=%v err=%v", m.src, m.dst, m.ok, err)
		}
	}

	if !p.IsTrusted(filepath.Join(f.ws, "a.txt")) || p.IsTrusted(filepath.Join(f.user, "a.txt")) {
		t.Error("trusted roots misclassified")
	}
	if _, err := p.ValidateAllowedDirCandidate(filepath.VolumeName(f.base) + string(filepath.Separator)); err == nil {
		t.Error("expected a whole drive to be rejected as trusted folder")
	}
	if _, err := p.ValidateAllowedDirCandidate(f.system); err == nil {
		t.Error("expected a protected system folder to be rejected as trusted folder")
	}

	// Adding f.user as a Trusted Folder unlocks content/media inside it while still blocking secrets & code.
	if _, err := p.ValidateAllowedDirCandidate(f.user); err != nil {
		t.Errorf("expected user folder to be valid as a trusted folder: %v", err)
	}
	p.SetAllowedDirs([]string{f.user})
	if _, err := p.ValidateRead(filepath.Join(f.user, "story.md")); err != nil {
		t.Errorf("expected read allowed inside added trusted folder: %v", err)
	}
	if _, err := p.ValidateWrite(filepath.Join(f.user, "kich-ban.md")); err != nil {
		t.Errorf("expected write allowed inside added trusted folder: %v", err)
	}
	if _, err := p.ValidateRead(filepath.Join(f.user, ".env")); err == nil {
		t.Error("expected .env to remain blocked inside trusted folder")
	}
	if _, err := p.ValidateRead(filepath.Join(f.user, "main.py")); err == nil {
		t.Error("expected .py to remain blocked inside trusted folder")
	}
}

func TestWorkflowRunNodeExpandsFoldersAndAppliesUIDefaults(t *testing.T) {
	f := newAccessFixture(t)
	for _, name := range []string{"b.jpg", "a.png", "notes.txt", "clip.mp4", "sub/c.webp", ".git/x.png"} {
		mustWrite(t, filepath.Join(f.media, name), "x")
	}

	payload, isErr := f.call(t, "workflow_run_node", map[string]any{
		"type":  watermark.NodeType,
		"input": map[string]any{"images": f.media},
	})
	if isErr {
		t.Fatalf("expected run to succeed, got %v", payload)
	}
	var got map[string]any
	if err := json.Unmarshal(f.wf.lastInput, &got); err != nil {
		t.Fatal(err)
	}
	images, _ := got["images"].([]any)
	if len(images) != 2 || !strings.HasSuffix(images[0].(string), "a.png") || !strings.HasSuffix(images[1].(string), "b.jpg") {
		t.Fatalf("expected folder expanded to [a.png b.jpg], got %v", got["images"])
	}
	if got["corner"] != "bottom-right" || got["workers"] != float64(0) || got["inplace"] != false {
		t.Fatalf("expected UI defaults applied, got %v", got)
	}

	if _, isErr := f.call(t, "workflow_run_node", map[string]any{
		"type": watermark.NodeType, "input": map[string]any{"images": []any{f.media}}, "recursive": true,
	}); isErr {
		t.Fatal("recursive run failed")
	}
	_ = json.Unmarshal(f.wf.lastInput, &got)
	if imgs, _ := got["images"].([]any); len(imgs) != 3 {
		t.Fatalf("expected recursive expansion to include sub/c.webp (and skip .git), got %v", got["images"])
	}
}

func TestWorkflowRunNodeRejectsInventedInput(t *testing.T) {
	f := newAccessFixture(t)
	mustWrite(t, filepath.Join(f.media, "a.png"), "x")
	mustWrite(t, filepath.Join(f.media, "empty", "readme.txt"), "x")
	mustWrite(t, filepath.Join(f.user, "outside.png"), "x")
	img := filepath.Join(f.media, "a.png")

	cases := map[string]map[string]any{
		"unknown node":     {"type": "tool.magic_eraser", "input": map[string]any{"images": img}},
		"invented param":   {"type": watermark.NodeType, "input": map[string]any{"images": img, "strength": 5}},
		"hidden param":     {"type": watermark.NodeType, "input": map[string]any{"images": img, "min_score": 0.1}},
		"invalid option":   {"type": watermark.NodeType, "input": map[string]any{"images": img, "corner": "center"}},
		"out of range":     {"type": watermark.NodeType, "input": map[string]any{"images": img, "workers": 500}},
		"missing file":     {"type": watermark.NodeType, "input": map[string]any{"images": filepath.Join(f.media, "nope.png")}},
		"wrong file type":  {"type": watermark.NodeType, "input": map[string]any{"images": filepath.Join(f.media, "empty", "readme.txt")}},
		"empty folder":     {"type": watermark.NodeType, "input": map[string]any{"images": filepath.Join(f.media, "empty")}},
		"no input":         {"type": watermark.NodeType, "input": map[string]any{}},
		"folder in single": {"type": watermark.NodeType, "input": map[string]any{"image": []any{img, img + "x"}}},
		"protected path":   {"type": watermark.NodeType, "input": map[string]any{"images": f.system}},
		"untrusted path":   {"type": watermark.NodeType, "input": map[string]any{"images": filepath.Join(f.user, "outside.png")}},
	}
	for name, args := range cases {
		f.wf.lastInput = nil
		payload, isErr := f.call(t, "workflow_run_node", args)
		if !isErr {
			t.Errorf("%s: expected an error, got %v", name, payload)
		}
		if f.wf.lastInput != nil {
			t.Errorf("%s: node must not run on invalid input", name)
		}
	}
}

func TestDestructiveAndOutsideActionsRequireTrustOrApproval(t *testing.T) {
	f := newAccessFixture(t)
	mustWrite(t, filepath.Join(f.media, "a.png"), "x")

	payload, isErr := f.call(t, "workflow_run_node", map[string]any{
		"type": watermark.NodeType, "input": map[string]any{"images": f.media, "inplace": true},
	})
	if isErr || payload["status"] != "needs_approval" || f.wf.lastInput != nil {
		t.Fatalf("expected in-place run to wait for approval, got %v (ran=%v)", payload, f.wf.lastInput != nil)
	}
	if !strings.Contains(payload["summary"].(string), "1 file") {
		t.Fatalf("expected approval summary to describe the files, got %v", payload["summary"])
	}

	// Paths outside workspace/media/trusted folders are denied outright rather than prompted.
	userFile := filepath.Join(f.user, "old.txt")
	mustWrite(t, userFile, "keep")
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"workspace_delete_file", map[string]any{"path": userFile}},
		{"workspace_write_file", map[string]any{"path": userFile, "content": "new"}},
		{"workspace_write_file", map[string]any{"path": filepath.Join(f.user, "Scripts", "kich-ban.md"), "content": "# Cảnh 1"}},
		{"workspace_edit_file", map[string]any{"path": userFile, "mode": "append", "content": "more"}},
		{"workspace_read_file", map[string]any{"path": userFile}},
		{"workspace_list_files", map[string]any{"dir": f.user}},
	} {
		if _, isErr := f.call(t, tc.tool, tc.args); !isErr {
			t.Fatalf("%s on untrusted path must fail", tc.tool)
		}
	}
	if raw, err := os.ReadFile(userFile); err != nil || string(raw) != "keep" {
		t.Fatalf("untrusted file must remain untouched, got %q err=%v", string(raw), err)
	}

	// Inside workspace/media/trusted folders, file operations execute directly; move requires overwrite=true
	// when the destination already exists, without prompting for file approval.
	wsFile := filepath.Join(f.ws, "tmp.txt")
	mustWrite(t, wsFile, "x")
	if payload, isErr := f.call(t, "workspace_delete_file", map[string]any{"path": wsFile}); isErr || payload["ok"] != true {
		t.Fatalf("expected direct delete inside workspace, got %v", payload)
	}
	moveSrc := filepath.Join(f.ws, "src.txt")
	moveDst := filepath.Join(f.ws, "dst.txt")
	mustWrite(t, moveSrc, "new")
	mustWrite(t, moveDst, "existing")
	if _, isErr := f.call(t, "workspace_move_file", map[string]any{
		"source": moveSrc, "destination": moveDst, "overwrite": false,
	}); !isErr {
		t.Fatalf("expected move without overwrite=true to fail when destination exists")
	}
	payload, isErr = f.call(t, "workspace_move_file", map[string]any{
		"source": moveSrc, "destination": moveDst, "overwrite": true,
	})
	if isErr || payload["ok"] != true {
		t.Fatalf("expected overwriting move inside workspace to succeed directly, got isErr=%v payload=%v", isErr, payload)
	}
	if raw, err := os.ReadFile(moveDst); err != nil || string(raw) != "new" {
		t.Fatalf("expected destination to be replaced with %q, got %q err=%v", "new", string(raw), err)
	}
}

func TestListFilesTrustedHidesSensitive(t *testing.T) {
	f := newAccessFixture(t)
	for _, name := range []string{"a.png", "story.md", "main.py", "id_rsa", ".env", "clip.mp4", ".git/x", "Album/b.jpg"} {
		mustWrite(t, filepath.Join(f.user, name), "x")
	}
	if _, isErr := f.call(t, "workspace_list_files", map[string]any{"dir": f.user}); !isErr {
		t.Error("expected untrusted folder listing to be denied")
	}
	f.svc.policy.SetAllowedDirs([]string{f.user})
	payload, isErr := f.call(t, "workspace_list_files", map[string]any{"dir": f.user})
	if isErr {
		t.Fatalf("list failed on trusted folder: %v", payload)
	}
	var names []string
	for _, e := range payload["entries"].([]any) {
		names = append(names, e.(map[string]any)["name"].(string))
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"a.png", "story.md", "clip.mp4", "Album"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %s in listing %v", want, names)
		}
	}
	for _, hidden := range []string{"main.py", "id_rsa", ".env", ".git"} {
		for _, n := range names {
			if n == hidden {
				t.Errorf("expected %s hidden, got %v", hidden, names)
			}
		}
	}
	if _, isErr := f.call(t, "workspace_list_files", map[string]any{"dir": f.system}); !isErr {
		t.Error("expected system folder listing to be denied")
	}
}

func TestCheckTrustedFile(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	trusted := filepath.Join(root, "granted")
	outside := filepath.Join(root, "elsewhere")
	for _, d := range []string{media, trusted, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := NewAccessPolicy(filepath.Join(root, "data"), filepath.Join(root, "ws"), media, "", []string{trusted})

	for _, ok := range []string{filepath.Join(media, "a.png"), filepath.Join(trusted, "sub", "b.png")} {
		if err := p.CheckTrustedFile(ok, true); err != nil {
			t.Errorf("%s should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{filepath.Join(outside, "c.png"), filepath.Join(media, "..", "elsewhere", "d.png")} {
		if err := p.CheckTrustedFile(bad, false); err == nil {
			t.Errorf("%s should be denied", bad)
		}
	}
}

func TestLargeScriptToolsAndDocumentRead(t *testing.T) {
	f := newAccessFixture(t)

	// 1. Large script (> 64 KiB) read in line chunks + search + surgical edit + append.
	var big strings.Builder
	for i := 1; i <= 2500; i++ {
		fmt.Fprintf(&big, "Cảnh %04d: Nội dung lời thoại dài để kiểm tra phân trang kịch bản %s.\n", i, strings.Repeat("x", 24))
	}
	scriptPath := filepath.Join(f.ws, "big_script.md")
	mustWrite(t, scriptPath, big.String())

	// Whole-file read fails safely with chunk hint; start_line/end_line succeeds.
	if _, isErr := f.call(t, "workspace_read_file", map[string]any{"path": scriptPath}); !isErr {
		t.Fatal("expected unpaged >64KiB read to fail")
	}
	payload, isErr := f.call(t, "workspace_read_file", map[string]any{
		"path": scriptPath, "start_line": 2495, "end_line": 2500,
	})
	if isErr {
		t.Fatalf("paged read failed: %v", payload)
	}
	if !strings.Contains(payload["content"].(string), "Cảnh 2500") || payload["start_line"].(float64) != 2495 {
		t.Fatalf("unexpected paged read payload: %v", payload)
	}

	// Recursive listing picks up subfolder files cleanly.
	mustWrite(t, filepath.Join(f.ws, "ep1", "scene1.txt"), "Nhân vật Minh xuất hiện.")
	payload, isErr = f.call(t, "workspace_list_files", map[string]any{"dir": f.ws, "recursive": true})
	if isErr || !strings.Contains(fmt.Sprint(payload["entries"]), "ep1/scene1.txt") {
		t.Fatalf("recursive list missed nested file: isErr=%v payload=%v", isErr, payload)
	}

	// Search locates line inside nested script while ignoring forbidden code extensions.
	mustWrite(t, filepath.Join(f.ws, "hidden.go"), "package main\n// Nhân vật Minh\n")
	payload, isErr = f.call(t, "workspace_search_text", map[string]any{"dir": f.ws, "query": "Nhân vật Minh"})
	if isErr {
		t.Fatalf("search failed: %v", payload)
	}
	matches := payload["matches"].([]any)
	if len(matches) != 1 || matches[0].(map[string]any)["rel_path"] != "ep1/scene1.txt" {
		t.Fatalf("unexpected search matches: %v", matches)
	}

	// Surgical edit in trusted workspace works directly, including prompt parameters with '=', ':', and brackets.
	payload, isErr = f.call(t, "workspace_edit_file", map[string]any{
		"path": scriptPath, "mode": "replace", "old_text": "Cảnh 2500", "content": "Cảnh Cuối",
	})
	if isErr || payload["ok"] != true {
		t.Fatalf("replace edit failed: %v", payload)
	}
	payload, isErr = f.call(t, "workspace_edit_file", map[string]any{
		"path": scriptPath, "mode": "append", "content": "Shot params: width=1920, ratio=16:9 (end).\n",
	})
	if isErr || payload["ok"] != true {
		t.Fatalf("append edit failed: %v", payload)
	}
	editedBytes, _ := os.ReadFile(scriptPath)
	if !strings.Contains(string(editedBytes), "Cảnh Cuối") || !strings.HasSuffix(string(editedBytes), "Shot params: width=1920, ratio=16:9 (end).\n") {
		t.Fatalf("edits did not land cleanly in file")
	}

	// Code files and untrusted outside paths remain blocked.
	if _, isErr := f.call(t, "workspace_edit_file", map[string]any{
		"path": filepath.Join(f.ws, "bad.py"), "mode": "append", "content": "print(1)\n",
	}); !isErr {
		t.Fatal("editing .py must be denied by extension policy")
	}
	outsideFile := filepath.Join(f.user, "note.txt")
	mustWrite(t, outsideFile, "Dòng 1\nDòng 2\n")
	if _, isErr := f.call(t, "workspace_edit_file", map[string]any{
		"path": outsideFile, "mode": "append", "content": "Dòng 3\n",
	}); !isErr {
		t.Fatal("expected edit outside trusted folders to be denied")
	}

	// 2. Document reading works inside trusted roots with pagination and rejects untrusted documents.
	untrustedDoc := filepath.Join(f.user, "outside.docx")
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:r><w:t>Kịch bản gốc rất hay.</w:t></w:r></w:p>` +
		`</w:body></w:document>`
	writeZipFixture(t, untrustedDoc, map[string]string{"word/document.xml": docXML})
	if _, isErr := f.call(t, "workspace_read_document", map[string]any{"path": untrustedDoc}); !isErr {
		t.Fatal("expected workspace_read_document to reject untrusted outside document")
	}

	docPath := filepath.Join(f.ws, "brief.docx")
	writeZipFixture(t, docPath, map[string]string{"word/document.xml": docXML})
	payload, isErr = f.call(t, "workspace_read_document", map[string]any{
		"path": docPath, "offset_chars": 0, "limit_chars": 8,
	})
	if isErr || payload["has_more"] != true || payload["content"] != "Kịch bản" {
		t.Fatalf("unexpected paged document response: isErr=%v payload=%v", isErr, payload)
	}
	payload2, isErr2 := f.call(t, "workspace_read_document", map[string]any{
		"path": docPath, "offset_chars": int(payload["next_offset"].(float64)), "limit_chars": 50,
	})
	if isErr2 || payload2["has_more"] != false || payload2["source_truncated"] != false || !strings.Contains(payload2["content"].(string), "rất hay.") {
		t.Fatalf("unexpected second page document response: isErr=%v payload=%v", isErr2, payload2)
	}
	payload3, isErr3 := f.call(t, "workspace_read_document", map[string]any{
		"path": docPath, "limit_chars": 60000,
	})
	if isErr3 || payload3["limit_chars"].(float64) != 60000 || payload3["source_truncated"] != false {
		t.Fatalf("expected 60000 limit_chars without 32k clamp: isErr=%v payload=%v", isErr3, payload3)
	}
}

func TestDeveloperModePolicyAllowsCodeOutsideSecrets(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "repo")
	profiles := filepath.Join(root, "profiles")
	for _, d := range []string{ws, profiles} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := NewAccessPolicy(root, ws, filepath.Join(root, "media"), profiles, nil)
	p.SetDeveloperMode(true)

	for _, allowed := range []string{
		filepath.Join(ws, "main.go"),
		filepath.Join(ws, "package.json"),
		filepath.Join(ws, "Makefile"),
		filepath.Join(ws, ".git", "HEAD"),
	} {
		if _, err := p.ValidateRead(allowed); err != nil {
			t.Errorf("developer mode should allow reading %s: %v", allowed, err)
		}
	}
	for _, denied := range []string{
		filepath.Join(ws, ".env"),
		filepath.Join(ws, "id_rsa"),
		filepath.Join(ws, "app.exe"),
		filepath.Join(profiles, "claude", "secret.json"),
	} {
		if _, err := p.ValidateRead(denied); err == nil {
			t.Errorf("developer mode must still deny secret/binary/profile path %s", denied)
		}
	}
}

func writeZipFixture(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWorkspaceRootCandidate(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	wsDir := filepath.Join(base, "ws")
	mediaDir := filepath.Join(base, "media")
	profilesDir := filepath.Join(dataDir, "agent-profiles")
	systemDir := filepath.Join(base, "Windows")
	appDataDir := filepath.Join(base, "AppData")

	for _, d := range []string{dataDir, wsDir, mediaDir, profilesDir, systemDir, appDataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	p := NewAccessPolicy(dataDir, wsDir, mediaDir, profilesDir, nil)
	p.protected = []string{systemDir, appDataDir, profilesDir}

	// 1. Accept valid existing directory
	validDir := filepath.Join(base, "CreativeWorkspace")
	if err := os.MkdirAll(validDir, 0o755); err != nil {
		t.Fatal(err)
	}
	norm, err := p.ValidateWorkspaceRootCandidate(validDir)
	if err != nil {
		t.Fatalf("expected valid existing dir to be accepted, got error: %v", err)
	}
	if norm != filepath.Clean(validDir) {
		t.Fatalf("expected normalized %q, got %q", filepath.Clean(validDir), norm)
	}

	// 2. Accept non-existent directory under existing valid parent (e.g. Documents\DezuxkStudio)
	nonExistentChild := filepath.Join(validDir, "DezuxkStudio")
	norm, err = p.ValidateWorkspaceRootCandidate(nonExistentChild)
	if err != nil {
		t.Fatalf("expected non-existent dir under valid parent to be accepted, got error: %v", err)
	}
	if norm != filepath.Clean(nonExistentChild) {
		t.Fatalf("expected normalized %q, got %q", filepath.Clean(nonExistentChild), norm)
	}

	// 3. Reject empty string or whitespace
	for _, empty := range []string{"", "   ", "\t\n"} {
		if _, err := p.ValidateWorkspaceRootCandidate(empty); err == nil {
			t.Fatalf("expected empty candidate %q to be rejected", empty)
		}
	}

	// 4. Reject NUL bytes
	if _, err := p.ValidateWorkspaceRootCandidate(validDir + "\x00bad"); err == nil {
		t.Fatal("expected NUL byte candidate to be rejected")
	}

	// 5. Reject relative path
	if _, err := p.ValidateWorkspaceRootCandidate("some/relative/path"); err == nil {
		t.Fatal("expected relative path to be rejected")
	}

	// 6. Reject volume roots
	volRoot := filepath.VolumeName(base) + string(filepath.Separator)
	if _, err := p.ValidateWorkspaceRootCandidate(volRoot); err == nil {
		t.Fatalf("expected volume root %q to be rejected", volRoot)
	}
	if _, err := p.ValidateWorkspaceRootCandidate("/"); err == nil {
		t.Fatal("expected root '/' to be rejected")
	}
	if _, err := p.ValidateWorkspaceRootCandidate(`\`); err == nil {
		t.Fatal(`expected root '\' to be rejected`)
	}

	// 7. Reject device paths and network paths
	devicePaths := []string{
		`\\?\` + validDir,
		`\\.\PhysicalDrive0`,
		`\??\` + validDir,
		`\\server\share`,
	}
	for _, dp := range devicePaths {
		if _, err := p.ValidateWorkspaceRootCandidate(dp); err == nil {
			t.Fatalf("expected device/network path %q to be rejected", dp)
		}
	}

	// 8. Reject Alternate Data Streams (ADS)
	adsPath := filepath.Join(validDir, "folder:stream")
	if _, err := p.ValidateWorkspaceRootCandidate(adsPath); err == nil {
		t.Fatalf("expected ADS path %q to be rejected", adsPath)
	}

	// 9. Reject existing regular file
	filePath := filepath.Join(validDir, "file.txt")
	mustWrite(t, filePath, "content")
	if _, err := p.ValidateWorkspaceRootCandidate(filePath); err == nil {
		t.Fatalf("expected regular file %q to be rejected as workspace root", filePath)
	}

	// 10. Reject inside dataDir
	insideData := filepath.Join(dataDir, "custom")
	if _, err := p.ValidateWorkspaceRootCandidate(insideData); err == nil {
		t.Fatalf("expected path inside dataDir %q to be rejected", insideData)
	}

	// 11. Reject inside protected system folder (systemDir, appDataDir)
	insideSystem := filepath.Join(systemDir, "Sub")
	if _, err := p.ValidateWorkspaceRootCandidate(insideSystem); err == nil {
		t.Fatalf("expected path inside systemDir %q to be rejected", insideSystem)
	}
	insideAppData := filepath.Join(appDataDir, "Sub")
	if _, err := p.ValidateWorkspaceRootCandidate(insideAppData); err == nil {
		t.Fatalf("expected path inside appDataDir %q to be rejected", insideAppData)
	}

	// 12. Reject forbidden directory segments (.git, .ssh, .aws)
	for _, seg := range []string{".git", ".ssh", ".aws", "node_modules"} {
		badSegPath := filepath.Join(validDir, seg, "workspace")
		if _, err := p.ValidateWorkspaceRootCandidate(badSegPath); err == nil {
			t.Fatalf("expected path with forbidden segment %q to be rejected", badSegPath)
		}
	}

	// 13. Reject path traversal resolving into protected folder
	traversal := filepath.Join(systemDir, "..", "Windows", "Sub")
	if _, err := p.ValidateWorkspaceRootCandidate(traversal); err == nil {
		t.Fatalf("expected traversal path %q to be rejected", traversal)
	}

	// 14. Reject Windows DOS reserved device names (CON, PRN, AUX, NUL, COM1-9, LPT1-9)
	dosCandidates := []string{
		`C:\CON`,
		`D:\projects\PRN`,
		`D:\projects\AUX`,
		`D:\projects\workspace\CON\sub`,
		`D:\projects\workspace\NUL\sub`,
		`C:\COM1`,
		`C:\LPT1`,
		`C:\path\com1`,
		`D:\projects\lpt3.txt`,
		`C:\projects\con.txt`,
		`D:\workspace\COM9\test`,
		`C:\workspace\lpt1`,
		`C:\workspace\lpt9\sub`,
		`C:\workspace\aux.tar.gz`,
		`C:\workspace\nul`,
		`C:\workspace\conin$`,
		`C:\workspace\conout$`,
		filepath.Join(validDir, "CON"),
		filepath.Join(validDir, "PRN", "sub"),
		filepath.Join(validDir, "nul.txt"),
		filepath.Join(validDir, "sub", "com5"),
	}
	for _, cand := range dosCandidates {
		if _, err := p.ValidateWorkspaceRootCandidate(cand); err == nil {
			t.Fatalf("expected DOS device candidate %q to be rejected", cand)
		} else if !strings.Contains(err.Error(), "contains a Windows reserved device name") {
			t.Fatalf("expected error containing %q for %q, got: %v", "contains a Windows reserved device name", cand, err)
		}
	}

	// Normal valid paths continue to be accepted (even containing substrings like "contact", "console", "constant")
	validNames := []string{
		filepath.Join(validDir, "contact"),
		filepath.Join(validDir, "console"),
		filepath.Join(validDir, "constant"),
		filepath.Join(validDir, "com10"),
		filepath.Join(validDir, "lpt0"),
		filepath.Join(validDir, "auxiliary"),
		filepath.Join(validDir, "null_folder"),
		filepath.Join(validDir, "printer"),
		filepath.Join(validDir, "confectionery"),
	}
	for _, v := range validNames {
		norm, err := p.ValidateWorkspaceRootCandidate(v)
		if err != nil {
			t.Fatalf("expected valid non-device path %q to be accepted, got error: %v", v, err)
		}
		if norm != filepath.Clean(v) {
			t.Fatalf("expected normalized %q, got %q", filepath.Clean(v), norm)
		}
	}

	// Also verify that Resolve on DOS device names within workspace is rejected
	for _, devFile := range []string{
		filepath.Join(wsDir, "CON"),
		filepath.Join(wsDir, "con.txt"),
		filepath.Join(wsDir, "PRN"),
		filepath.Join(wsDir, "aux.log"),
		filepath.Join(wsDir, "nul"),
		filepath.Join(wsDir, "com1"),
		filepath.Join(wsDir, "lpt2.dat"),
	} {
		if _, err := p.Resolve(devFile); err == nil {
			t.Fatalf("expected Resolve to reject DOS device file %q", devFile)
		} else if !strings.Contains(err.Error(), "contains a Windows reserved device name") {
			t.Fatalf("expected error containing %q for %q, got: %v", "contains a Windows reserved device name", devFile, err)
		}
	}
}

func TestSetWorkspaceDirs_HotReload(t *testing.T) {
	f := newAccessFixture(t)
	svc := f.svc

	newWS := filepath.Join(f.base, "new_workspace")
	newMedia := filepath.Join(f.base, "new_media")
	for _, d := range []string{newWS, newMedia} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Update workspace directories
	svc.SetWorkspaceDirs(newWS, newMedia)

	if svc.getWorkspaceDir() != newWS {
		t.Fatalf("expected workspaceDir %q, got %q", newWS, svc.getWorkspaceDir())
	}
	if svc.getMediaDir() != newMedia {
		t.Fatalf("expected mediaDir %q, got %q", newMedia, svc.getMediaDir())
	}

	// AccessPolicy roots must contain the new directories
	roots := svc.policy.Roots()
	hasWS, hasMedia := false, false
	for _, r := range roots {
		if pathKey(r) == pathKey(newWS) {
			hasWS = true
		}
		if pathKey(r) == pathKey(newMedia) {
			hasMedia = true
		}
	}
	if !hasWS || !hasMedia {
		t.Fatalf("expected policy.Roots() to contain newWS and newMedia: %v", roots)
	}

	// Capabilities snapshot must reflect the new directories
	payload, isErr := f.call(t, "studio_list_capabilities", nil)
	if isErr {
		t.Fatalf("studio_list_capabilities failed: %v", payload)
	}
	if payload["workspace_dir"] != newWS {
		t.Fatalf("expected capabilities workspace_dir = %q, got %v", newWS, payload["workspace_dir"])
	}
	trustedFolders, _ := payload["trusted_folders"].([]any)
	foundNewWS := false
	for _, tf := range trustedFolders {
		if tfStr, ok := tf.(string); ok && pathKey(tfStr) == pathKey(newWS) {
			foundNewWS = true
			break
		}
	}
	if !foundNewWS {
		t.Fatalf("expected trusted_folders to include newWS: %v", trustedFolders)
	}
}

func TestValidateDeleteRootGuards(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	wsDir := filepath.Join(base, "studio", "workspace")
	mediaDir := filepath.Join(base, "studio", "media")
	stagingDir := filepath.Join(dataDir, "staging")

	for _, d := range []string{dataDir, wsDir, mediaDir, stagingDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	p := NewAccessPolicy(dataDir, wsDir, mediaDir, "", nil)
	p.SetWorkspaceDirs(wsDir, mediaDir, stagingDir)

	// Direct roots cannot be deleted
	if _, err := p.ValidateDelete(wsDir); err == nil {
		t.Fatal("expected delete of wsDir to be denied")
	}
	if _, err := p.ValidateDelete(mediaDir); err == nil {
		t.Fatal("expected delete of mediaDir to be denied")
	}
	if _, err := p.ValidateDelete(stagingDir); err == nil {
		t.Fatal("expected delete of stagingDir to be denied")
	}
	// Workspace parent (studio root) cannot be deleted
	if _, err := p.ValidateDelete(filepath.Dir(wsDir)); err == nil {
		t.Fatal("expected delete of workspace root parent to be denied")
	}

	// Files inside workspace can be deleted
	testFile := filepath.Join(wsDir, "sample.txt")
	mustWrite(t, testFile, "hello")
	if _, err := p.ValidateDelete(testFile); err != nil {
		t.Fatalf("expected delete of text file inside wsDir to be allowed: %v", err)
	}
}

func TestAddProtectedDirsBlocksCandidateAndResolve(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	wsDir := filepath.Join(base, "studio", "workspace")
	mediaDir := filepath.Join(base, "studio", "media")
	extLogs := filepath.Join(base, "external_logs")
	extConfig := filepath.Join(base, "external_config")
	for _, d := range []string{dataDir, wsDir, mediaDir, extLogs, extConfig} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	p := NewAccessPolicy(dataDir, wsDir, mediaDir, "", nil)
	p.AddProtectedDirs(extLogs, extConfig)

	if _, err := p.ValidateWorkspaceRootCandidate(filepath.Join(extLogs, "nested")); err == nil {
		t.Fatal("expected ValidateWorkspaceRootCandidate to reject path inside external logs dir")
	}
	if _, err := p.ValidateWorkspaceRootCandidate(filepath.Join(extConfig, "nested")); err == nil {
		t.Fatal("expected ValidateWorkspaceRootCandidate to reject path inside external config dir")
	}
}

func TestAccessPolicySecretsNLEFormatsProjectsDirAndSafeGuardErrors(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	secretsDir := filepath.Join(dataDir, "secrets")
	legacyStudioWS := filepath.Join(dataDir, "studio", "workspace")
	legacyProjects := filepath.Join(legacyStudioWS, "projects")
	wsDir := filepath.Join(base, "custom", "workspace")
	projectsDir := filepath.Join(wsDir, "projects")
	mediaDir := filepath.Join(base, "custom", "media")

	for _, d := range []string{secretsDir, legacyProjects, projectsDir, mediaDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(secretsDir, "secrets.enc"), "cipher")
	mustWrite(t, filepath.Join(secretsDir, "master.key"), "key")
	mustWrite(t, filepath.Join(wsDir, "secrets.enc"), "cipher")
	mustWrite(t, filepath.Join(wsDir, ".env"), "API_KEY=secret")
	mustWrite(t, filepath.Join(wsDir, "timeline.otio"), `{"OTIO_SCHEMA":"Timeline.1"}`)
	mustWrite(t, filepath.Join(wsDir, "timeline.fcpxml"), `<fcpxml version="1.10"/>`)
	mustWrite(t, filepath.Join(legacyProjects, "first-film", "project.json"), `{"title":"First Film"}`)

	p := NewAccessPolicy(dataDir, wsDir, mediaDir, "", nil)
	p.SetProjectsDir(projectsDir)

	// 1. NLE text formats (.otio, .fcpxml) are first-class text files.
	for _, nle := range []string{filepath.Join(wsDir, "timeline.otio"), filepath.Join(wsDir, "timeline.fcpxml")} {
		if _, err := p.ValidateRead(nle); err != nil {
			t.Fatalf("expected NLE format %s readable: %v", nle, err)
		}
		if _, err := p.ValidateWrite(nle); err != nil {
			t.Fatalf("expected NLE format %s writable: %v", nle, err)
		}
		if err := p.CheckTrustedFile(nle, true); err != nil {
			t.Fatalf("expected CheckTrustedFile allowed on %s: %v", nle, err)
		}
	}

	// 2. Legacy <Data>/studio/workspace/projects seeded automatically on startup remains trusted.
	if err := p.CheckTrustedFile(filepath.Join(legacyProjects, "first-film", "project.json"), true); err != nil {
		t.Fatalf("expected legacy studio project file trusted: %v", err)
	}

	// 3. ValidateDelete guards projectsDir directly.
	if _, err := p.ValidateDelete(projectsDir); err == nil {
		t.Fatal("expected ValidateDelete to block deleting projectsDir")
	}

	// 4. Secrets folder, .enc, master.key, and .env are blocked in BOTH assistant and developer modes,
	// and CheckTrustedFile returns CodeUnauthorized without leaking server host paths.
	for _, dev := range []bool{false, true} {
		p.SetDeveloperMode(dev)
		for _, bad := range []string{
			filepath.Join(secretsDir, "secrets.enc"),
			filepath.Join(secretsDir, "master.key"),
			filepath.Join(wsDir, "secrets.enc"),
			filepath.Join(wsDir, ".env"),
		} {
			err := p.CheckTrustedFile(bad, false)
			if !apperr.IsCode(err, apperr.CodeUnauthorized) {
				t.Fatalf("dev=%v: expected CodeUnauthorized for %s, got %v", dev, bad, err)
			}
			if strings.Contains(err.Error(), base) {
				t.Fatalf("CheckTrustedFile leaked host directory %q in error: %v", base, err)
			}
		}
	}

	// 5. LimitsFrom wires MaxApprovalContinuations from Config.
	cfg := config.Default(config.ModeDesktop)
	cfg.Agent.MaxApprovalContinuations = 9
	lim := LimitsFrom(cfg)
	if lim.MaxApprovalContinuations != 9 {
		t.Fatalf("LimitsFrom MaxApprovalContinuations = %d, want 9", lim.MaxApprovalContinuations)
	}
}
