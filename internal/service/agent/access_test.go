package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// t.TempDir() itself lives under %LOCALAPPDATA% on Windows (which the real policy protects).
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

func TestAccessPolicyOpenByDefaultBlocksSensitive(t *testing.T) {
	f := newAccessFixture(t)
	p := f.svc.policy

	allowedRead := []string{
		filepath.Join(f.user, "story.md"), filepath.Join(f.user, "sub.srt"), filepath.Join(f.user, "a.png"),
		filepath.Join(f.user, "clip.mp4"), filepath.Join(f.ws, "notes.txt"), filepath.Join(f.media, "x.webp"),
	}
	for _, path := range allowedRead {
		if _, err := p.ValidateRead(path); err != nil {
			t.Errorf("expected read allowed for %s: %v", path, err)
		}
	}
	deniedRead := []string{
		filepath.Join(f.user, "main.py"), filepath.Join(f.user, "app.ts"), filepath.Join(f.user, "run.ps1"),
		filepath.Join(f.user, "tool.exe"), filepath.Join(f.user, "page.html"), filepath.Join(f.user, ".env"),
		filepath.Join(f.user, "id_rsa"), filepath.Join(f.user, "cert.pem"), filepath.Join(f.user, "package.json"),
		filepath.Join(f.user, ".ssh", "config.txt"), filepath.Join(f.user, "repo", ".git", "notes.txt"),
		filepath.Join(f.user, "node_modules", "readme.md"), filepath.Join(f.system, "notes.txt"),
		filepath.Join(f.profiles, "codex", "notes.txt"), filepath.Join(f.base, "data", "app.db"),
		filepath.Join(f.user, "report.pdf"), // documents can be organized but not opened
	}
	for _, path := range deniedRead {
		if _, err := p.ValidateRead(path); err == nil {
			t.Errorf("expected read denied for %s", path)
		}
	}

	if _, err := p.ValidateWrite(filepath.Join(f.user, "kich-ban.md")); err != nil {
		t.Errorf("expected writing a script anywhere on the machine to be allowed: %v", err)
	}
	for _, bad := range []string{"index.html", "a.bat", "settings.yaml", "tsconfig.json", "requirements.txt"} {
		if _, err := p.ValidateWrite(filepath.Join(f.user, bad)); err == nil {
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
		_, _, err := p.ValidateMove(filepath.Join(f.user, m.src), filepath.Join(f.user, m.dst))
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
	if _, err := p.ValidateAllowedDirCandidate(f.user); err != nil {
		t.Errorf("expected a normal user folder to be grantable: %v", err)
	}
}

func TestWorkflowRunNodeExpandsFoldersAndAppliesUIDefaults(t *testing.T) {
	f := newAccessFixture(t)
	for _, name := range []string{"b.jpg", "a.png", "notes.txt", "clip.mp4", "sub/c.webp", ".git/x.png"} {
		mustWrite(t, filepath.Join(f.user, name), "x")
	}

	payload, isErr := f.call(t, "workflow_run_node", map[string]any{
		"type":  watermark.NodeType,
		"input": map[string]any{"images": f.user},
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
		"type": watermark.NodeType, "input": map[string]any{"images": []any{f.user}}, "recursive": true,
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
	mustWrite(t, filepath.Join(f.user, "a.png"), "x")
	mustWrite(t, filepath.Join(f.user, "empty", "readme.txt"), "x")
	img := filepath.Join(f.user, "a.png")

	cases := map[string]map[string]any{
		"unknown node":    {"type": "tool.magic_eraser", "input": map[string]any{"images": img}},
		"invented param":  {"type": watermark.NodeType, "input": map[string]any{"images": img, "strength": 5}},
		"hidden param":    {"type": watermark.NodeType, "input": map[string]any{"images": img, "min_score": 0.1}},
		"invalid option":  {"type": watermark.NodeType, "input": map[string]any{"images": img, "corner": "center"}},
		"out of range":    {"type": watermark.NodeType, "input": map[string]any{"images": img, "workers": 500}},
		"missing file":    {"type": watermark.NodeType, "input": map[string]any{"images": filepath.Join(f.user, "nope.png")}},
		"wrong file type": {"type": watermark.NodeType, "input": map[string]any{"images": filepath.Join(f.user, "empty", "readme.txt")}},
		"empty folder":    {"type": watermark.NodeType, "input": map[string]any{"images": filepath.Join(f.user, "empty")}},
		"no input":        {"type": watermark.NodeType, "input": map[string]any{}},
		"folder in single port": {"type": watermark.NodeType, "input": map[string]any{
			"image": []any{img, img + "x"}}},
		"protected path": {"type": watermark.NodeType, "input": map[string]any{"images": f.system}},
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

func TestDestructiveAndOutsideActionsNeedApproval(t *testing.T) {
	f := newAccessFixture(t)
	mustWrite(t, filepath.Join(f.user, "a.png"), "x")

	payload, isErr := f.call(t, "workflow_run_node", map[string]any{
		"type": watermark.NodeType, "input": map[string]any{"images": f.user, "inplace": true},
	})
	if isErr || payload["status"] != "needs_approval" || f.wf.lastInput != nil {
		t.Fatalf("expected in-place run to wait for approval, got %v (ran=%v)", payload, f.wf.lastInput != nil)
	}
	if !strings.Contains(payload["summary"].(string), "1 file") {
		t.Fatalf("expected approval summary to describe the files, got %v", payload["summary"])
	}

	userFile := filepath.Join(f.user, "old.txt")
	mustWrite(t, userFile, "keep")
	payload, _ = f.call(t, "workspace_delete_file", map[string]any{"path": userFile})
	if payload["status"] != "needs_approval" {
		t.Fatalf("expected delete outside trusted roots to need approval, got %v", payload)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Fatal("file must not be deleted before approval")
	}
	payload, _ = f.call(t, "workspace_write_file", map[string]any{"path": userFile, "content": "new"})
	if payload["status"] != "needs_approval" {
		t.Fatalf("expected overwrite outside trusted roots to need approval, got %v", payload)
	}

	wsFile := filepath.Join(f.ws, "tmp.txt")
	mustWrite(t, wsFile, "x")
	if payload, isErr := f.call(t, "workspace_delete_file", map[string]any{"path": wsFile}); isErr || payload["ok"] != true {
		t.Fatalf("expected direct delete inside workspace, got %v", payload)
	}
	newFile := filepath.Join(f.user, "Scripts", "kich-ban.md")
	if payload, isErr := f.call(t, "workspace_write_file", map[string]any{"path": newFile, "content": "# Cảnh 1"}); isErr || payload["ok"] != true {
		t.Fatalf("expected creating a new script in a user folder to work, got %v", payload)
	}
}

func TestListFilesAnywhereHidesSensitive(t *testing.T) {
	f := newAccessFixture(t)
	for _, name := range []string{"a.png", "story.md", "main.py", "id_rsa", ".env", "clip.mp4", ".git/x", "Album/b.jpg"} {
		mustWrite(t, filepath.Join(f.user, name), "x")
	}
	payload, isErr := f.call(t, "workspace_list_files", map[string]any{"dir": f.user})
	if isErr {
		t.Fatalf("list failed: %v", payload)
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

	// Search locates line inside nested script while ignoring forbidden code in user folder.
	mustWrite(t, filepath.Join(f.ws, "hidden.go"), "package main\n// Nhân vật Minh\n")
	payload, isErr = f.call(t, "workspace_search_text", map[string]any{"dir": f.ws, "query": "Nhân vật Minh"})
	if isErr {
		t.Fatalf("search failed: %v", payload)
	}
	matches := payload["matches"].([]any)
	if len(matches) != 1 || matches[0].(map[string]any)["rel_path"] != "ep1/scene1.txt" {
		t.Fatalf("unexpected search matches: %v", matches)
	}

	// Surgical edit in trusted workspace works directly; outside trusted workspace needs approval; code edit rejected.
	payload, isErr = f.call(t, "workspace_edit_file", map[string]any{
		"path": scriptPath, "mode": "replace", "old_text": "Cảnh 2500", "content": "Cảnh Cuối",
	})
	if isErr || payload["ok"] != true {
		t.Fatalf("replace edit failed: %v", payload)
	}
	payload, isErr = f.call(t, "workspace_edit_file", map[string]any{
		"path": scriptPath, "mode": "append", "content": "Hết tập 1.\n",
	})
	if isErr || payload["ok"] != true {
		t.Fatalf("append edit failed: %v", payload)
	}
	editedBytes, _ := os.ReadFile(scriptPath)
	if !strings.Contains(string(editedBytes), "Cảnh Cuối") || !strings.HasSuffix(string(editedBytes), "Hết tập 1.\n") {
		t.Fatalf("edits did not land cleanly in file")
	}

	if _, isErr := f.call(t, "workspace_edit_file", map[string]any{
		"path": scriptPath, "mode": "append", "content": "package main\nfunc main() {\n\tprintln(\"hi\")\n}\n",
	}); !isErr {
		t.Fatal("assistant mode must block code injection via workspace_edit_file")
	}

	outsideFile := filepath.Join(f.user, "note.txt")
	mustWrite(t, outsideFile, "Dòng 1\nDòng 2\n")
	payload, _ = f.call(t, "workspace_edit_file", map[string]any{
		"path": outsideFile, "mode": "append", "content": "Dòng 3\n",
	})
	if payload["status"] != "needs_approval" {
		t.Fatalf("expected outside trusted folder edit to require approval, got %v", payload)
	}

	// 2. Document reading works via workspace_read_document with pagination and rejects code-laden docs.
	docPath := filepath.Join(f.user, "brief.docx")
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:r><w:t>Kịch bản gốc rất hay.</w:t></w:r></w:p>` +
		`</w:body></w:document>`
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
	if isErr2 || payload2["has_more"] != false || !strings.Contains(payload2["content"].(string), "rất hay.") {
		t.Fatalf("unexpected second page document response: isErr=%v payload=%v", isErr2, payload2)
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
