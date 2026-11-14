package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// codeSamples contain explicit programming-language Markdown fences that must always be blocked in chat.
var codeSamples = map[string]string{
	"fenced python":       "Đây là code:\n```python\nprint('hi')\n```\n",
	"fenced js one line":  "```js\nconsole.log(1)\n```",
	"fenced go":           "```go\npackage main\nfunc main() { fmt.Println(\"x\") }\n```\n",
	"fenced c":            "```c\n#include <stdio.h>\nint main() { printf(\"hi\"); return 0; }\n```\n",
	"fenced bash":         "```bash\n#!/bin/bash\nmv \"$f\" \"new_$f\"\n```\n",
	"fenced shell":        "```sh\nnpm install express\nnode server.js\n```\n",
	"fenced powershell":   "```powershell\nGet-ChildItem *.mp4 | Rename-Item -NewName { \"x_\" + $_.Name }\n```\n",
	"fenced sql":          "```sql\nDELETE FROM users WHERE id = 2;\n```\n",
	"fenced html":         "```html\n<html><body><script>alert(1)</script></body></html>\n```\n",
	"fenced java":         "```java\npublic class Main { public static void main(String[] args) { System.out.println(\"hi\"); } }\n```\n",
	"fenced yaml":         "```yaml\nservices:\n  api:\n    image: alpine\n```\n",
	"unterminated fence":  "```python\nimport os\nos.remove('a')\n",
	"indented tilde bash": "  ~~~bash\nrm -rf /tmp/demo\n~~~\n",
}

func longVietnameseMovieScript() string {
	var sb strings.Builder
	sb.WriteString("# KỊCH BẢN PHIM NGẮN: ÁNH ĐÈN PHỐ CỔ\n\n")
	sb.WriteString("| Shot | Thời lượng | Góc máy | Thông số ánh sáng |\n")
	sb.WriteString("| --- | --- | --- | --- |\n")
	for i := 1; i <= 120; i++ {
		sb.WriteString("| S01 | 00:05 | Medium | key_light=5600K, iso=800, lens=35mm |\n")
		sb.WriteString("[CẢNH NGOẠI — ĐÊM — HÀ NỘI]\n")
		sb.WriteString("LAN (thì thầm, nhìn ra cửa sổ):\n")
		sb.WriteString("  (ngừng một nhịp)\n")
		sb.WriteString("  Nếu ngày mai trời còn mưa: chúng ta dời lịch quay sang thứ Sáu (18:30).\n")
		sb.WriteString("Prompt: cinematic portrait = soft rain, ratio=16:9, steps=28, cfg=6.5.\n\n")
	}
	return sb.String()
}

// proseSamples are legitimate DezuxkStudio content and must never be corrupted or blocked.
var proseSamples = map[string]string{
	"vietnamese script":     "Cảnh 1: Bình minh trên phố cổ Hà Nội.\nNarrator: Mỗi buổi sáng, thành phố thức dậy trong tiếng rao.\nCảnh 2: Cận cảnh ly cà phê trứng.\nNarrator: Hương vị này đã có từ năm 1946.\n",
	"long vietnamese story": longVietnameseMovieScript(),
	"english screenplay":    "INT. COFFEE SHOP - NIGHT\n\nRAIN streaks the window. MAYA sits across from DEV.\n\nMAYA\n(V.O., wry)\nYou always order tea when ratio=16:9 feels too tight.\n\nDEV\n(CONT'D)\nLook at the shot list: ext=night, iso=1600, shutter=1/50.\n",
	"srt":                   "1\n00:00:01,000 --> 00:00:03,500\nXin chào các bạn!\n\n2\n00:00:03,600 --> 00:00:06,000\nHôm nay chúng ta học nấu phở.\n",
	"fenced srt":            "```srt\n1\n00:00:01,000 --> 00:00:02,500\nCảnh 1: Mở đầu.\n```\n",
	"workflow json":         `{"nodes":[{"id":"n1","type":"tool.watermark_remover","params":{"inplace":false,"input":"D:/media/a.mp4"}}],"edges":[]}`,
	"fenced json shots":     "```json\n{\n  \"episode\": 1,\n  \"dialogue\": \"Lan nói: \u201cXin chào\u201d\",\n  \"shots\": [\n    {\"id\": \"s1\", \"params\": \"width=1920, height=1080, fps=24\"}\n  ]\n}\n```\n",
	"bare fence notes":      "```\nShot 1: camera=35mm\nShot 2: mood=noir\nAction: Lan (bước vào): Chào buổi tối!\n```\n",
	"fenced markdown":       "```markdown\n| Cảnh | Ghi chú |\n| --- | --- |\n| 1 | fade=in |\n```\n",
	"image prompt":          "A cinematic shot of a lone samurai in the rain, neon reflections, 35mm, shallow depth of field --ar 16:9 --v 6 --style raw\nwidth=1920, height=1080, cfg=7.0, sampler=euler\nNegative prompt: blurry, lowres, watermark\n",
	"markdown steps":        "## Cách xóa watermark\n1. Mở tab **Watermark Remover** trong DezuxkStudio.\n2. Chọn video cần xử lý.\n3. Nhấn **Chạy** để xuất kết quả.\n",
	"inline code span":      "```var_a``` là tên trường dữ liệu trong JSON shot list.\n",
	"json narrative":        `{"notes": "for beat in act1:\n  intensity=high\n  dialogue: Chào bạn!\n"}`,
}

func TestContainsSourceCodeDetectsCode(t *testing.T) {
	for name, sample := range codeSamples {
		if !ContainsSourceCode(sample) {
			t.Errorf("%s: expected ContainsSourceCode to detect fenced code", name)
		}
	}
}

func TestContainsSourceCodeAllowsContent(t *testing.T) {
	for name, sample := range proseSamples {
		if ContainsSourceCode(sample) {
			t.Errorf("%s: false positive on legitimate content", name)
		}
	}
}

// streamThrough feeds text to a fresh guard in chunks of the given size and returns everything released.
func streamThrough(text string, chunk int) (string, *turnOutputGuard) {
	g := newTurnOutputGuard("m1")
	var out strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); i += chunk {
		end := min(i+chunk, len(runes))
		out.WriteString(g.filterText(string(runes[i:end])))
	}
	tail, _ := g.finalize()
	out.WriteString(tail)
	return out.String(), g
}

func TestStreamFilterPassesContentUnchanged(t *testing.T) {
	for name, sample := range proseSamples {
		chunkSizes := []int{1, 3, 17, 10000}
		if len(sample) > 5000 {
			// Avoid O(N^2) allocations with 1-rune chunks on a 30k-char fixture while testing streaming chunks.
			chunkSizes = []int{17, 256, 100000}
		}
		for _, chunk := range chunkSizes {
			got, g := streamThrough(sample, chunk)
			if got != sample {
				t.Errorf("%s (chunk=%d): content altered", name, chunk)
			}
			if g.blocked() {
				t.Errorf("%s (chunk=%d): guard blocked legitimate content", name, chunk)
			}
		}
	}
}

func TestStreamFilterNeverReleasesCode(t *testing.T) {
	codeMarkers := []string{"print(", "console.log", "fmt.Println", "printf(", "mv \"$f\"", "node server.js",
		"Rename-Item", "DELETE FROM", "<script>", "os.remove", "System.out", "image: alpine", "rm -rf"}
	for name, sample := range codeSamples {
		for _, chunk := range []int{1, 4, 10000} {
			got, g := streamThrough("Đây là câu trả lời.\n"+sample+"\nHết.", chunk)
			if !g.blocked() {
				t.Errorf("%s (chunk=%d): expected guard to block", name, chunk)
				continue
			}
			for _, m := range codeMarkers {
				if strings.Contains(got, m) {
					t.Errorf("%s (chunk=%d): code fragment %q leaked to the UI:\n%s", name, chunk, m, got)
				}
			}
			if !strings.Contains(got, noticeMarker(noticeCodeBlocked)) {
				t.Errorf("%s (chunk=%d): expected the block notice marker, got %q", name, chunk, got)
			}
			if strings.Contains(got, "Hết.") {
				t.Errorf("%s (chunk=%d): text after blocked code must be dropped", name, chunk)
			}
			if g.sanitizedText() != got {
				t.Errorf("%s (chunk=%d): persisted text differs from what the UI received", name, chunk)
			}
		}
	}
}

func TestStreamFilterKeepsTextBeforeCode(t *testing.T) {
	got, g := streamThrough("Mở đầu hợp lệ.\n```python\nimport os\n```\n", 2)
	if !g.blocked() || !strings.HasPrefix(got, "Mở đầu hợp lệ.\n") {
		t.Fatalf("expected leading prose to survive and code to be blocked, got %q", got)
	}
}

func TestBlockNoticeIsLanguageNeutral(t *testing.T) {
	// The UI translates the marker; the backend must not pick a language.
	if got := noticeMarker(noticeCodeBlocked); got != "[[notice:code_blocked]]" {
		t.Fatalf("unexpected marker %q", got)
	}
	if got := stripNoticeMarkers("ok\n[[notice:code_blocked]]\n"); strings.Contains(got, "notice") {
		t.Fatalf("markers must not be replayed to the model: %q", got)
	}
}

func TestThoughtFilterDropsCode(t *testing.T) {
	g := newTurnOutputGuard("m1")
	var out strings.Builder
	out.WriteString(g.filterThought("Người dùng muốn code.\n"))
	out.WriteString(g.filterThought("```python\nimport os\nos.system('x')\n```\n"))
	_, tail := g.finalize()
	out.WriteString(tail)
	if strings.Contains(out.String(), "os.system") {
		t.Fatalf("code leaked through reasoning stream: %q", out.String())
	}
}

// TestServiceGuardsProviderOutput drives a full turn through Service with a fake Codex runner that emits code,
// verifying the UI delta stream, final done text, and stored DB history are all clean.
func TestServiceGuardsProviderOutput(t *testing.T) {
	dir := t.TempDir()
	dummyCodex := filepath.Join(dir, "codex.exe")
	if err := os.WriteFile(dummyCodex, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := newFakeRepo()
	var mu sync.Mutex
	var chats []domainagent.ChatEvent
	doneCh := make(chan domainagent.ChatEvent, 4)
	var svc *Service
	var err error
	svc, err = New(Options{
		Repo:         repo,
		DataDir:      dir,
		WorkspaceDir: filepath.Join(dir, "ws"),
		MediaDir:     filepath.Join(dir, "media"),
		ProfilesDir:  filepath.Join(dir, "agent-profiles"),
		Logger:       slog.New(slog.DiscardHandler),
		OnChatEvent: func(evt domainagent.ChatEvent) {
			mu.Lock()
			chats = append(chats, evt)
			mu.Unlock()
			if evt.Type == domainagent.ChatEventDone || evt.Type == domainagent.ChatEventError {
				doneCh <- evt
			}
		},
		ProbeCodexAuth: func(context.Context, string, string) (domainagent.AuthState, string) {
			return domainagent.AuthAuthenticated, "test"
		},
		RunCodexTurn: func(_ context.Context, _, _, _, _, _, messageID, _ string) error {
			acc := &codexStreamAccumulator{providerID: domainagent.ProviderCodex, messageID: messageID, emit: svc.emitChat}
			msg, _ := json.Marshal("Được thôi, đây là code:\n```python\nimport os\nos.system('calc')\n```\nChúc bạn thành công.")
			thought, _ := json.Marshal("User wants code.\n```python\nimport os\nos.system('calc')\n```\n")
			acc.handleLine(`{"type":"item.completed","item":{"type":"reasoning","text":` + string(thought) + `}}`)
			acc.handleLine(`{"type":"item.completed","item":{"type":"agent_message","text":` + string(msg) + `}}`)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx := context.Background()
	if _, err := svc.SetCustomPath(ctx, domainagent.ProviderCodex, dummyCodex); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Connect(ctx, domainagent.ProviderCodex); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, _, err := svc.SendPrompt(ctx, "Viết giúp tôi code Python mở calculator"); err != nil {
		t.Fatal(err)
	}
	select {
	case evt := <-doneCh:
		if evt.Type != domainagent.ChatEventDone {
			t.Fatalf("expected done, got %+v", evt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn timed out")
	}

	mu.Lock()
	var ui strings.Builder
	for _, e := range chats {
		ui.WriteString(e.Text)
	}
	mu.Unlock()
	if strings.Contains(ui.String(), "os.system") {
		t.Fatalf("code reached the UI: %q", ui.String())
	}
	if !strings.Contains(ui.String(), noticeMarker(noticeCodeBlocked)) {
		t.Fatalf("expected block notice in UI stream, got %q", ui.String())
	}
	history, _ := repo.ListConversationMessages(ctx, svc.CurrentConversationID(), 10)
	if len(history) == 0 {
		t.Fatal("expected persisted chat history")
	}
	for _, m := range history {
		if strings.Contains(m.Text, "os.system") || strings.Contains(m.Thought, "os.system") {
			t.Fatalf("code persisted to chat history: %+v", m)
		}
	}
}

func TestWorkspaceWriteToolExtensionBlockingAndJSONValidation(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      dir,
		WorkspaceDir: ws,
		MediaDir:     filepath.Join(dir, "media"),
		ProfilesDir:  filepath.Join(dir, "agent-profiles"),
		Logger:       slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(tool string, args map[string]any) *mcp.CallToolResult {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	resultText := func(res *mcp.CallToolResult) string {
		if len(res.Content) == 0 {
			return ""
		}
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			return tc.Text
		}
		return ""
	}

	// Extension blocking prevents writing programming source files.
	for name, tc := range map[string]struct{ ext, body string }{
		"python":     {".py", "print('hi')\n"},
		"shell":      {".sh", "echo hi\n"},
		"go":         {".go", "package main\nfunc main() {}\n"},
		"powershell": {".ps1", "Write-Host hi\n"},
	} {
		target := filepath.Join(ws, "bad_"+name+tc.ext)
		if res := call("workspace_write_file", map[string]any{"path": target, "content": tc.body}); !res.IsError {
			t.Errorf("%s: expected workspace_write_file to reject code extension %s", name, tc.ext)
		}
		if _, err := os.Stat(target); err == nil {
			t.Fatalf("%s: forbidden extension %s was written to disk", name, tc.ext)
		}
	}

	// Long creative scripts / markdown tables / parameter syntax write and read cleanly without false positives.
	longScriptPath := filepath.Join(ws, "script.md")
	longScript := longVietnameseMovieScript()
	if res := call("workspace_write_file", map[string]any{"path": longScriptPath, "content": longScript}); res.IsError {
		t.Fatalf("expected long script write to succeed, got %s", resultText(res))
	}
	if res := call("workspace_read_file", map[string]any{"path": longScriptPath}); res.IsError {
		t.Fatalf("expected long script read to succeed, got %s", resultText(res))
	}

	// Pre-write JSON validation rejects broken syntax and hints on typographic curly quotes,
	// while allowing valid UTF-8 typographic curly quotes inside valid JSON string values.
	badJSONPath := filepath.Join(ws, "bad_curly.json")
	resBad := call("workspace_write_file", map[string]any{
		"path":    badJSONPath,
		"content": "{“shot”: 1}",
	})
	if !resBad.IsError || !strings.Contains(resultText(resBad), "typographic curly quotes") {
		t.Fatalf("expected curly-quote JSON validation hint, got isError=%v text=%q", resBad.IsError, resultText(resBad))
	}
	if _, err := os.Stat(badJSONPath); err == nil {
		t.Fatal("invalid JSON must not be written to disk")
	}

	shotListPath := filepath.Join(ws, "shot_list.json")
	validJSON := "{\"dialogue\": \"\u201cXin ch\u00e0o\u201d\", \"fps\": 24}"
	if res := call("workspace_write_file", map[string]any{"path": shotListPath, "content": validJSON}); res.IsError {
		t.Fatalf("valid JSON with UTF-8 curly quotes inside string must succeed, got %s", resultText(res))
	}

	// workspace_edit_file must also validate resulting JSON before modifying disk.
	if res := call("workspace_edit_file", map[string]any{
		"path":    shotListPath,
		"mode":    "append",
		"content": "not valid trailing json",
	}); !res.IsError {
		t.Fatal("expected workspace_edit_file to reject edit that produces invalid JSON")
	}
	raw, _ := os.ReadFile(shotListPath)
	if string(raw) != validJSON {
		t.Fatalf("disk file mutated on failed JSON validation: got %q", string(raw))
	}
}

func TestStreamFilterReleasesProseBeforeNewline(t *testing.T) {
	g := newTurnOutputGuard("m1")
	var got strings.Builder
	for _, chunk := range []string{"Chào ", "bạn! Mình là ", "trợ lý của DezuxkStudio", " và có thể giúp bạn."} {
		out := g.filterText(chunk)
		if out == "" {
			t.Fatalf("expected prose chunk %q to stream immediately", chunk)
		}
		got.WriteString(out)
	}
	tail, _ := g.finalize()
	got.WriteString(tail)
	if got.String() != "Chào bạn! Mình là trợ lý của DezuxkStudio và có thể giúp bạn." {
		t.Fatalf("streamed text altered: %q", got.String())
	}
}

func TestStreamFilterEarlyReleaseStillBlocksCode(t *testing.T) {
	text := "Đây là đoạn mã bạn cần nhé:\n```python\nimport os\nos.system('calc')\n```\n"
	got, g := streamThrough(text, 3)
	if !g.blocked() || strings.Contains(got, "os.system") {
		t.Fatalf("expected fenced code blocked even with early prose release, got %q", got)
	}
}

func TestStreamFilterAllowsScreenplayParentheticals(t *testing.T) {
	g := newTurnOutputGuard("m1")
	var got strings.Builder
	for _, chunk := range []string{"[Cảnh 1] ", "Minh (thì thầm): ", "Chúng ta đi thôi."} {
		out := g.filterText(chunk)
		if out == "" {
			t.Fatalf("expected screenplay chunk %q to stream without paragraph hold", chunk)
		}
		got.WriteString(out)
	}
	tail, _ := g.finalize()
	got.WriteString(tail)
	if g.blocked() || got.String() != "[Cảnh 1] Minh (thì thầm): Chúng ta đi thôi." {
		t.Fatalf("unexpected screenplay stream: blocked=%v got=%q", g.blocked(), got.String())
	}

	gDev := newTurnOutputGuard("m2")
	gDev.disable()
	code := "```go\npackage main\nfunc main() { println(1) }\n```"
	if out := gDev.filterText(code); out != code || gDev.blocked() {
		t.Fatalf("disabled developer guard must stream code untouched, got %q", out)
	}
}
