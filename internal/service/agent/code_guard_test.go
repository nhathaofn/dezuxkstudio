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

// codeSamples must always be detected as source code / commands.
var codeSamples = map[string]string{
	"fenced python":        "Đây là code:\n```python\nprint('hi')\n```\n",
	"fenced js one line":   "```js\nconsole.log(1)\n```",
	"unfenced python":      "def read_csv(path):\n    with open(path) as f:\n        return f.read()\n",
	"unfenced js":          "const fs = require('fs');\nconst data = fs.readFileSync('a.txt');\nconsole.log(data);\n",
	"unfenced go":          "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"x\")\n}\n",
	"c include":            "#include <stdio.h>\nint main() {\n  printf(\"hi\");\n  return 0;\n}\n",
	"bash script":          "#!/bin/bash\nfor f in *.mp4; do\n  mv \"$f\" \"new_$f\"\ndone\n",
	"shell commands":       "npm install express\nnode server.js\ncurl -X POST http://localhost:3000\n",
	"powershell":           "Get-ChildItem *.mp4 | ForEach-Object {\n  Rename-Item $_ -NewName (\"x_\" + $_.Name)\n}\nWrite-Host \"done\"\n",
	"sql":                  "SELECT name FROM users WHERE id = 1;\nINSERT INTO logs VALUES (1);\nDELETE FROM users WHERE id = 2;\n",
	"html":                 "<html>\n<body>\n<script>alert(1)</script>\n</body>\n</html>\n",
	"unlabeled fence code": "```\nimport os\nos.system('dir')\nx = os.getcwd()\n```",
	"json embedding code":  `{"script": "import os\nos.system('calc')\nprint(os.getcwd())\n"}`,
	"java":                 "public class Main {\n  public static void main(String[] args) {\n    System.out.println(\"hi\");\n  }\n}\n",
	"code in dialogue":     "Cảnh 1:\nNhân vật A đọc:\nconst x = fetch('/api');\nlet y = await x.json();\nconsole.log(y);\n",
	"unterminated fence":   "```python\nimport os\nos.remove('a')\n",
}

// proseSamples are legitimate DezuxkStudio content and must never be blocked.
var proseSamples = map[string]string{
	"vietnamese script":    "Cảnh 1: Bình minh trên phố cổ Hà Nội.\nNarrator: Mỗi buổi sáng, thành phố thức dậy trong tiếng rao.\nCảnh 2: Cận cảnh ly cà phê trứng.\nNarrator: Hương vị này đã có từ năm 1946.\n",
	"srt":                  "1\n00:00:01,000 --> 00:00:03,500\nXin chào các bạn!\n\n2\n00:00:03,600 --> 00:00:06,000\nHôm nay chúng ta học nấu phở.\n",
	"workflow json":        `{"nodes":[{"id":"n1","type":"tool.watermark_remover","params":{"inplace":false,"input":"D:/media/a.mp4"}}],"edges":[]}`,
	"image prompt":         "A cinematic shot of a lone samurai in the rain, neon reflections, 35mm, shallow depth of field --ar 16:9 --v 6 --style raw\nNegative prompt: blurry, lowres, watermark\n",
	"markdown steps":       "## Cách xóa watermark\n1. Mở tab **Watermark Remover**.\n2. Kéo thả video (mp4) vào khung.\n3. Chọn vùng watermark rồi nhấn Start.\n> Lưu ý: bật inplace sẽ ghi đè file gốc.\n",
	"settings explanation": "Thông số gợi ý:\nfps = 30\nresolution = 1920x1080\nbitrate = 8 Mbps\n",
	"storyboard arrows":    "Luồng cảnh:\nCảnh 1 => Cảnh 2\nCảnh 2 => Cảnh 3\nCảnh 3 => Kết thúc\n",
	"english script":       "INT. KITCHEN - NIGHT\nMAYA (whispering): Did you hear that?\nJAKE: It's just the wind.\nShe walks to the window and opens it slowly.\n",
	"one tool mention":     "Bạn cần cài ffmpeg trước. Mình có thể tải nó qua mục Binaries, bạn chỉ cần nhấn Approve.\n",
	"translation":          "Original: The quick brown fox jumps over the lazy dog.\nBản dịch: Con cáo nâu nhanh nhẹn nhảy qua con chó lười.\n",
	"fenced srt":           "```srt\n1\n00:00:01,000 --> 00:00:02,000\nXin chào\n```",
	"fenced json data":     "```json\n{\"title\": \"Video 1\", \"tags\": [\"travel\", \"food\"]}\n```",
	"csv":                  "scene,duration,description\n1,5s,Mở đầu với flycam\n2,10s,Phỏng vấn nhân vật\n",
}

func TestContainsSourceCodeDetectsCode(t *testing.T) {
	for name, sample := range codeSamples {
		if !ContainsSourceCode(sample) {
			t.Errorf("%s: expected source code to be detected:\n%s", name, sample)
		}
	}
}

func TestContainsSourceCodeAllowsContent(t *testing.T) {
	for name, sample := range proseSamples {
		if ContainsSourceCode(sample) {
			t.Errorf("%s: legitimate content was flagged as code:\n%s", name, sample)
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
		for _, chunk := range []int{1, 3, 17, 10000} {
			got, g := streamThrough(sample, chunk)
			if got != sample {
				t.Errorf("%s (chunk=%d): content altered\nwant %q\ngot  %q", name, chunk, sample, got)
			}
			if g.blocked() {
				t.Errorf("%s (chunk=%d): guard blocked legitimate content", name, chunk)
			}
		}
	}
}

func TestStreamFilterNeverReleasesCode(t *testing.T) {
	codeMarkers := []string{"print(", "console.log", "fmt.Println", "printf(", "mv \"$f\"", "node server.js",
		"Rename-Item", "DELETE FROM", "<script>", "os.system", "System.out", "fetch('/api')", "os.remove"}
	for name, sample := range codeSamples {
		if name == "json embedding code" {
			continue // a single-line JSON blob is judged by ContainsSourceCode at the file-write layer
		}
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
	out.WriteString(g.filterThought("def a():\n    import os\n    os.system('x')\n    return 1\n"))
	_, tail := g.finalize()
	out.WriteString(tail)
	if strings.Contains(out.String(), "os.system") {
		t.Fatalf("code leaked through reasoning stream: %q", out.String())
	}
}

// TestServiceGuardsProviderOutput drives a full turn through Service with a fake Codex runner that emits code,
// and verifies neither the UI events nor the persisted history contain it.
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
			thought, _ := json.Marshal("User wants code.\nimport os\nos.system('calc')\nprint('x')\n")
			acc.handleLine(`{"type":"item.completed","item":{"type":"reasoning","text":` + string(thought) + `}}`)
			acc.handleLine(`{"type":"item.completed","item":{"type":"agent_message","text":` + string(msg) + `}}`)
			svc.persistAssistantMessage(domainagent.ProviderCodex, messageID, acc.assistantOutput(), acc.thoughtOutput())
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
	history, _ := repo.ListChatMessages(ctx, 10)
	for _, m := range history {
		if strings.Contains(m.Text, "os.system") || strings.Contains(m.Thought, "os.system") {
			t.Fatalf("code persisted to chat history: %+v", m)
		}
	}
}

func TestWorkspaceWriteToolRejectsCode(t *testing.T) {
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

	write := func(path, content string) *mcp.CallToolResult {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name:      "workspace_write_file",
			Arguments: map[string]any{"path": path, "content": content},
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	for name, sample := range codeSamples {
		target := filepath.Join(ws, "note.txt")
		if res := write(target, sample); !res.IsError {
			t.Errorf("%s: expected workspace_write_file to refuse code", name)
		}
		if _, err := os.Stat(target); err == nil {
			t.Fatalf("%s: code was written to disk", name)
		}
	}
	if res := write(filepath.Join(ws, "script.md"), proseSamples["vietnamese script"]); res.IsError {
		t.Fatalf("expected legitimate script to be written, got %+v", res.Content)
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
	text := "Đây là đoạn mã bạn cần nhé:\nimport os\nos.system('calc')\nprint(os.getcwd())\n"
	got, g := streamThrough(text, 3)
	if !g.blocked() || strings.Contains(got, "os.system") {
		t.Fatalf("expected code blocked even with early prose release, got %q", got)
	}
}
