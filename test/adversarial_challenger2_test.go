package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/runner"
	"app/internal/security"
	binariessvc "app/internal/service/binaries"
	"app/internal/tools/watermark"
)

// ============================================================================
// Adversarial Challenge 1: Stress-Test Binary Manifest & Regexes
// ============================================================================

func TestAdversarial_Manifest_VersionExtraction_EdgeCases(t *testing.T) {
	ffmpegEntry, ok := binaries.GetManifest(string(binaries.IDFFmpeg))
	if !ok {
		t.Fatal("ffmpeg manifest entry missing")
	}
	ffprobeEntry, ok := binaries.GetManifest(string(binaries.IDFFprobe))
	if !ok {
		t.Fatal("ffprobe manifest entry missing")
	}
	ytDlpEntry, ok := binaries.GetManifest(string(binaries.IDYtDlp))
	if !ok {
		t.Fatal("yt-dlp manifest entry missing")
	}

	testCases := []struct {
		name        string
		entry       binaries.ManifestEntry
		output      string
		expectedVer string
	}{
		// Git hash versions
		{
			name:        "FFmpeg git hash release",
			entry:       ffmpegEntry,
			output:      "ffmpeg version N-112345-gabcdef1234 Copyright (c) 2000-2024",
			expectedVer: "N-112345-gabcdef1234",
		},
		{
			name:        "FFmpeg git date version",
			entry:       ffmpegEntry,
			output:      "ffmpeg version git-2024-05-01-abcdef Copyright (c) 2000-2024",
			expectedVer: "git-2024-05-01-abcdef",
		},
		{
			name:        "FFprobe git hash release",
			entry:       ffprobeEntry,
			output:      "ffprobe version N-112345-gabcdef1234 Copyright (c) 2007-2024",
			expectedVer: "N-112345-gabcdef1234",
		},
		{
			name:        "yt-dlp standard release with newline and carriage return",
			entry:       ytDlpEntry,
			output:      "\r\n2024.10.22\r\n",
			expectedVer: "2024.10.22",
		},
		{
			name:        "yt-dlp nightly with revision suffix",
			entry:       ytDlpEntry,
			output:      "2024.10.22.235959",
			expectedVer: "2024.10.22.235959",
		},
		// Noisy stdout/stderr with interleaved banners
		{
			name:        "FFmpeg with extensive debug banner before version",
			entry:       ffmpegEntry,
			output:      "[debug] initializing environment...\nconfiguration: --enable-gpl\nffmpeg version 7.1-essentials Copyright (c)\nbuilt with gcc 13.2.0",
			expectedVer: "7.1-essentials",
		},
		{
			name:        "yt-dlp with python warning banners before version",
			entry:       ytDlpEntry,
			output:      "UserWarning: Certificate verification notice\n[debug] system initialized\n2024.11.04\n[debug] exiting",
			expectedVer: "2024.11.04",
		},
		// Truncated lines
		{
			name:        "FFmpeg truncated version token (empty string)",
			entry:       ffmpegEntry,
			output:      "ffmpeg version",
			expectedVer: "",
		},
		{
			name:        "FFmpeg trailing space with no version",
			entry:       ffmpegEntry,
			output:      "ffmpeg version ",
			expectedVer: "",
		},
		{
			name:        "yt-dlp incomplete year only",
			entry:       ytDlpEntry,
			output:      "2024",
			expectedVer: "",
		},
		{
			name:        "yt-dlp year and month only",
			entry:       ytDlpEntry,
			output:      "2024.10",
			expectedVer: "",
		},
		// Binary garbage and ANSI escape sequences
		{
			name:        "FFmpeg with ANSI color sequences",
			entry:       ffmpegEntry,
			output:      "\x1b[32mffmpeg version 7.1.0\x1b[0m Copyright (c)",
			expectedVer: "7.1.0\x1b[0m", // Note: regex captures until whitespace
		},
		{
			name:        "Completely random binary garbage",
			entry:       ffmpegEntry,
			output:      string([]byte{0x00, 0xFF, 0xFE, 0x12, 0x34, 0x7F, 0x80}),
			expectedVer: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.entry.ExtractVersion(tc.output)
			if got != tc.expectedVer {
				t.Errorf("ExtractVersion mismatch: expected %q, got %q", tc.expectedVer, got)
			}
		})
	}
}

func TestAdversarial_Manifest_ProgressRegexes_EdgeCases(t *testing.T) {
	t.Run("FFmpegProgressParser_ZeroDuration", func(t *testing.T) {
		parser := &runner.FFmpegProgressParser{TotalDuration: 0}
		pct, msg, ok := parser.ParseLine("frame=   10 fps= 10 q=20.0 size=    100kB time=00:00:10.00 bitrate= 100kbits/s speed=1.0x")
		if !ok {
			t.Fatal("expected ok=true")
		}
		if pct != 0.0 {
			t.Errorf("expected pct=0.0 when TotalDuration=0, got %f", pct)
		}
		if !strings.Contains(msg, "speed: 1.0x") {
			t.Errorf("expected speed in msg, got %q", msg)
		}
	})

	t.Run("FFmpegProgressParser_ProgressExceedingTotalDuration_CappedAt100", func(t *testing.T) {
		parser := &runner.FFmpegProgressParser{TotalDuration: 10 * time.Second}
		// 15 seconds elapsed, which exceeds 10s
		pct, msg, ok := parser.ParseLine("frame=  100 fps= 24 q=20.0 size=   1000kB time=00:00:15.00 bitrate= 100kbits/s speed=2.0x")
		if !ok {
			t.Fatal("expected ok=true")
		}
		if pct != 100.0 {
			t.Errorf("expected pct=100.0 capped, got %f", pct)
		}
		if !strings.Contains(msg, "00:00:15") {
			t.Errorf("expected formatted time, got %q", msg)
		}
	})

	t.Run("FFmpegProgressParser_MissingSpeedParameter", func(t *testing.T) {
		parser := &runner.FFmpegProgressParser{TotalDuration: 60 * time.Second}
		pct, msg, ok := parser.ParseLine("frame=   50 fps= 20 q=25.0 size=    500kB time=00:00:30.00 bitrate=  80kbits/s")
		if !ok {
			t.Fatal("expected ok=true even without speed parameter")
		}
		if pct < 49.9 || pct > 50.1 {
			t.Errorf("expected ~50%%, got %f", pct)
		}
		if strings.Contains(msg, "speed:") {
			t.Errorf("expected no speed in msg, got %q", msg)
		}
	})

	t.Run("FFmpegProgressParser_NonMatchingLines", func(t *testing.T) {
		parser := &runner.FFmpegProgressParser{TotalDuration: 60 * time.Second}
		lines := []string{
			"Input #0, matroska,webm, from 'input.mkv':",
			"Stream #0:0(eng): Video: h264 (High)",
			"Press [q] to stop, [?] for help",
			"[h264 @ 000001] Missing reference picture",
			"",
		}
		for _, line := range lines {
			_, _, ok := parser.ParseLine(line)
			if ok {
				t.Errorf("expected ok=false for non-progress line %q", line)
			}
		}
	})

	t.Run("YtDlpProgressParser_StandardAndEdgeCases", func(t *testing.T) {
		parser := &runner.YtDlpProgressParser{}

		// Standard line
		pct, msg, ok := parser.ParseLine("[download]  45.5% of ~100.00MiB at  5.20MiB/s ETA 00:10")
		if !ok {
			t.Fatal("expected ok=true for standard yt-dlp line")
		}
		if pct != 45.5 {
			t.Errorf("expected pct 45.5, got %f", pct)
		}
		if !strings.Contains(msg, "45.5% / ~100.00MiB at 5.20MiB/s") {
			t.Errorf("unexpected msg: %q", msg)
		}

		// 100% completion
		pct, _, ok = parser.ParseLine("[download] 100.0% of 150.00MiB at 10.00MiB/s")
		if !ok || pct != 100.0 {
			t.Errorf("expected 100.0%%, got %f (ok=%v)", pct, ok)
		}

		// Non-matching yt-dlp lines
		nonMatching := []string{
			"[youtube] Extracting URL: https://www.youtube.com/watch?v=xyz",
			"[download] Destination: video.mp4",
			"[download] 100% of 15.00MiB in 00:02", // Missing "at <speed>"
			"[Merger] Merging formats into 'video.mp4'",
			"ERROR: Video unavailable",
		}
		for _, line := range nonMatching {
			_, _, ok := parser.ParseLine(line)
			if ok {
				t.Errorf("expected ok=false for yt-dlp line: %q", line)
			}
		}
	})
}

func TestAdversarial_Manifest_GetManifest_InvalidLookups(t *testing.T) {
	invalidIDs := []string{
		"",
		" ",
		"FFMPEG",
		"ffmpeg.exe",
		"yt-dlp", // Actual ID is yt_dlp
		"nonexistent_tool_123",
		"../../bin/sh",
		"null",
	}

	for _, id := range invalidIDs {
		entry, ok := binaries.GetManifest(id)
		if ok {
			t.Errorf("GetManifest(%q) returned true, expected false", id)
		}
		if entry.ID != "" {
			t.Errorf("GetManifest(%q) returned non-empty entry", id)
		}
	}
}

// ============================================================================
// Adversarial Challenge 2: Stress-Test Backend Error Handling & Contracts
// ============================================================================

func TestAdversarial_Engine_CycleDetection_ComplexTopologies(t *testing.T) {
	reg := engine.NewRegistry()
	_ = reg.Register(watermark.NewNode(watermark.Options{}))
	ctx := context.Background()

	// 1. Self-loop: Node A connects to itself
	t.Run("SelfLoop", func(t *testing.T) {
		g := engine.Graph{
			Nodes: []engine.GraphNode{
				{ID: "node_self", Type: watermark.NodeType},
			},
			Edges: []engine.Edge{
				{
					From: engine.Endpoint{Node: "node_self", Port: "image"},
					To:   engine.Endpoint{Node: "node_self", Port: "image"},
				},
			},
		}
		_, err := reg.RunGraph(ctx, g)
		if err == nil {
			t.Fatal("expected cycle error on self-loop, got nil")
		}
		assertNoVietnameseDiacritics(t, err.Error(), "self-loop error")
		if !strings.Contains(strings.ToLower(err.Error()), "cycle") {
			t.Errorf("expected 'cycle' in error, got: %q", err.Error())
		}
	})

	// 2. Multi-node cycle: A -> B -> C -> D -> A
	t.Run("FourNodeCycle", func(t *testing.T) {
		g := engine.Graph{
			Nodes: []engine.GraphNode{
				{ID: "node_1", Type: watermark.NodeType},
				{ID: "node_2", Type: watermark.NodeType},
				{ID: "node_3", Type: watermark.NodeType},
				{ID: "node_4", Type: watermark.NodeType},
			},
			Edges: []engine.Edge{
				{From: engine.Endpoint{Node: "node_1", Port: "image"}, To: engine.Endpoint{Node: "node_2", Port: "image"}},
				{From: engine.Endpoint{Node: "node_2", Port: "image"}, To: engine.Endpoint{Node: "node_3", Port: "image"}},
				{From: engine.Endpoint{Node: "node_3", Port: "image"}, To: engine.Endpoint{Node: "node_4", Port: "image"}},
				{From: engine.Endpoint{Node: "node_4", Port: "image"}, To: engine.Endpoint{Node: "node_1", Port: "image"}},
			},
		}
		_, err := reg.RunGraph(ctx, g)
		if err == nil {
			t.Fatal("expected cycle error on 4-node cycle, got nil")
		}
		assertNoVietnameseDiacritics(t, err.Error(), "4-node cycle error")
		if !strings.Contains(strings.ToLower(err.Error()), "cycle") {
			t.Errorf("expected 'cycle' in error, got: %q", err.Error())
		}
	})

	// 3. Disconnected components where one component has a cycle:
	// Component 1: valid_1 -> valid_2
	// Component 2: cycle_1 -> cycle_2 -> cycle_1
	t.Run("CycleInDisconnectedSubGraph", func(t *testing.T) {
		g := engine.Graph{
			Nodes: []engine.GraphNode{
				{ID: "valid_1", Type: watermark.NodeType},
				{ID: "valid_2", Type: watermark.NodeType},
				{ID: "cycle_1", Type: watermark.NodeType},
				{ID: "cycle_2", Type: watermark.NodeType},
			},
			Edges: []engine.Edge{
				{From: engine.Endpoint{Node: "valid_1", Port: "image"}, To: engine.Endpoint{Node: "valid_2", Port: "image"}},
				{From: engine.Endpoint{Node: "cycle_1", Port: "image"}, To: engine.Endpoint{Node: "cycle_2", Port: "image"}},
				{From: engine.Endpoint{Node: "cycle_2", Port: "image"}, To: engine.Endpoint{Node: "cycle_1", Port: "image"}},
			},
		}
		_, err := reg.RunGraph(ctx, g)
		if err == nil {
			t.Fatal("expected cycle error in disconnected subgraph, got nil")
		}
		assertNoVietnameseDiacritics(t, err.Error(), "disconnected subgraph cycle error")
		if !strings.Contains(strings.ToLower(err.Error()), "cycle") {
			t.Errorf("expected 'cycle' in error, got: %q", err.Error())
		}
	})

	// 4. Diamond DAG with Back-Edge: A -> B, A -> C -> D, D -> B, B -> D (back-edge)
	t.Run("DiamondWithBackEdge", func(t *testing.T) {
		g := engine.Graph{
			Nodes: []engine.GraphNode{
				{ID: "dia_a", Type: watermark.NodeType},
				{ID: "dia_b", Type: watermark.NodeType},
				{ID: "dia_c", Type: watermark.NodeType},
				{ID: "dia_d", Type: watermark.NodeType},
			},
			Edges: []engine.Edge{
				{From: engine.Endpoint{Node: "dia_a", Port: "images"}, To: engine.Endpoint{Node: "dia_b", Port: "images"}},
				{From: engine.Endpoint{Node: "dia_a", Port: "image"}, To: engine.Endpoint{Node: "dia_c", Port: "image"}},
				{From: engine.Endpoint{Node: "dia_c", Port: "image"}, To: engine.Endpoint{Node: "dia_d", Port: "image"}},
				{From: engine.Endpoint{Node: "dia_d", Port: "image"}, To: engine.Endpoint{Node: "dia_b", Port: "image"}},
				{From: engine.Endpoint{Node: "dia_b", Port: "images"}, To: engine.Endpoint{Node: "dia_d", Port: "images"}}, // Back-edge B -> D (images port accepts multiple)
			},
		}
		_, err := reg.RunGraph(ctx, g)
		if err == nil {
			t.Fatal("expected cycle error on diamond with back-edge, got nil")
		}
		assertNoVietnameseDiacritics(t, err.Error(), "diamond with back-edge error")
		if !strings.Contains(strings.ToLower(err.Error()), "cycle") {
			t.Errorf("expected 'cycle' in error, got: %q", err.Error())
		}
	})
}

func TestAdversarial_Engine_AllErrorBranchesEnglishCleanliness(t *testing.T) {
	reg := engine.NewRegistry()
	_ = reg.Register(watermark.NewNode(watermark.Options{}))
	ctx := context.Background()

	cases := []struct {
		name        string
		graph       engine.Graph
		expectedSub string
	}{
		{
			name:        "Empty nodes",
			graph:       engine.Graph{Nodes: []engine.GraphNode{}},
			expectedSub: "does not contain any nodes",
		},
		{
			name: "Missing node ID",
			graph: engine.Graph{
				Nodes: []engine.GraphNode{{ID: "", Type: watermark.NodeType}},
			},
			expectedSub: "missing an identifier",
		},
		{
			name: "Duplicate node ID",
			graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "dup", Type: watermark.NodeType},
					{ID: "dup", Type: watermark.NodeType},
				},
			},
			expectedSub: "duplicate node id",
		},
		{
			name: "Unknown node type",
			graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "node1", Type: "unknown.engine.type"},
				},
			},
			expectedSub: "unknown node type",
		},
		{
			name: "Invalid params JSON",
			graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "node1", Type: watermark.NodeType, Params: []byte("{invalid-json}")},
				},
			},
			expectedSub: "invalid parameters for node",
		},
		{
			name: "Edge to non-existent node",
			graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "node1", Type: watermark.NodeType},
				},
				Edges: []engine.Edge{
					{From: engine.Endpoint{Node: "node1", Port: "image"}, To: engine.Endpoint{Node: "ghost", Port: "image"}},
				},
			},
			expectedSub: "references a non-existent node",
		},
		{
			name: "Node does not have output port",
			graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "node1", Type: watermark.NodeType},
					{ID: "node2", Type: watermark.NodeType},
				},
				Edges: []engine.Edge{
					{From: engine.Endpoint{Node: "node1", Port: "imaginary_output"}, To: engine.Endpoint{Node: "node2", Port: "image"}},
				},
			},
			expectedSub: "does not have output port",
		},
		{
			name: "Node does not have input port",
			graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "node1", Type: watermark.NodeType},
					{ID: "node2", Type: watermark.NodeType},
				},
				Edges: []engine.Edge{
					{From: engine.Endpoint{Node: "node1", Port: "image"}, To: engine.Endpoint{Node: "node2", Port: "imaginary_input"}},
				},
			},
			expectedSub: "does not have input port",
		},
		{
			name: "Port only accepts single incoming connection",
			graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "node1", Type: watermark.NodeType},
					{ID: "node2", Type: watermark.NodeType},
					{ID: "node3", Type: watermark.NodeType},
				},
				Edges: []engine.Edge{
					{From: engine.Endpoint{Node: "node1", Port: "image"}, To: engine.Endpoint{Node: "node3", Port: "image"}},
					{From: engine.Endpoint{Node: "node2", Port: "image"}, To: engine.Endpoint{Node: "node3", Port: "image"}},
				},
			},
			expectedSub: "only accepts a single incoming connection",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reg.RunGraph(ctx, tc.graph)
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
			msg := err.Error()
			assertNoVietnameseDiacritics(t, msg, "engine validation: "+tc.name)
			if !strings.Contains(strings.ToLower(msg), strings.ToLower(tc.expectedSub)) {
				t.Errorf("expected error containing %q, got: %q", tc.expectedSub, msg)
			}
		})
	}
}

func TestAdversarial_DPAPI_MultiByteAndEmojiPreservation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI is only supported on Windows")
	}

	multiBytePayloads := [][]byte{
		[]byte("Tiếng Việt có đầy đủ dấu: Ơ, Ư, Đ, ă, ắ, ằ, ẳ, ẵ, ặ, ô, ố, ồ, ổ, ỗ, ộ"),
		[]byte("Unicode CJK: 漢字, ひらがな, カタカナ, 한국어"),
		[]byte("Emoji & Symbols: 🔥🚀💡🎉🛡️✨💻📦"),
		[]byte("Arabic & RTL: مرحبا بالعالم - اختبار التشفير"),
		[]byte("Complex JSON: " + `{"user": "Nguyễn Văn A", "note": "Ghi chú bảo mật", "emoji": "🔐"}`),
	}

	for _, payload := range multiBytePayloads {
		encrypted, err := security.EncryptDPAPI(payload)
		if err != nil {
			t.Fatalf("EncryptDPAPI failed: %v", err)
		}
		if bytes.Equal(encrypted, payload) {
			t.Fatal("plaintext equals ciphertext")
		}

		decrypted, err := security.DecryptDPAPI(encrypted)
		if err != nil {
			t.Fatalf("DecryptDPAPI failed: %v", err)
		}
		if !bytes.Equal(decrypted, payload) {
			t.Fatalf("UTF-8 corruption detected!\nExpected: %s\nGot:      %s", string(payload), string(decrypted))
		}
	}
}

// ============================================================================
// Adversarial Challenge 3: Stress-Test Mock Download Resilience
// ============================================================================

func TestAdversarial_Download_CrashSafetyAndTempFileCleanup(t *testing.T) {
	ctx := context.Background()

	t.Run("MidDownloadServerFailure_CleansUpTempFiles", func(t *testing.T) {
		toolsDir := t.TempDir()
		repo := newMockRepo()
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		svc := binariessvc.New(repo, toolsDir, logger)

		// Server that sends 100 bytes and abruptly terminates connection
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "10000")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("X", 100)))
			// Close connection abruptly
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
		}))
		defer server.Close()

		origSpec := binaries.Registry[binaries.IDFFmpeg]
		platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

		testSpec := origSpec
		testDownloads := make(map[string]binaries.DownloadSpec)
		for k, v := range origSpec.Downloads {
			testDownloads[k] = v
		}
		testDownloads[platformKey] = binaries.DownloadSpec{
			URL:           server.URL + "/ffmpeg.zip",
			ArchiveFormat: binaries.ArchiveFormatZip,
			ExtractFile:   "bin/ffmpeg.exe",
		}
		testSpec.Downloads = testDownloads
		binaries.Registry[binaries.IDFFmpeg] = testSpec
		defer func() { binaries.Registry[binaries.IDFFmpeg] = origSpec }()

		_, err := svc.Download(ctx, binaries.IDFFmpeg, nil)
		if err == nil {
			t.Fatal("expected download failure on aborted connection, got nil")
		}

		// Verify target file was NOT created
		targetExec := filepath.Join(toolsDir, testSpec.ExecutableName())
		if _, statErr := os.Stat(targetExec); !os.IsNotExist(statErr) {
			t.Errorf("target executable %s should not exist after aborted download", targetExec)
		}

		// Verify no leftover .tmp-* or .*-download.zip files in toolsDir
		entries, _ := os.ReadDir(toolsDir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				t.Errorf("found leftover temporary file in toolsDir: %s", e.Name())
			}
		}
	})

	t.Run("ExistingTargetFile_AtomicOverwriteIntegrity", func(t *testing.T) {
		toolsDir := t.TempDir()
		repo := newMockRepo()
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		svc := binariessvc.New(repo, toolsDir, logger)

		origSpec := binaries.Registry[binaries.IDYtDlp]
		platformKey := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
		targetExec := filepath.Join(toolsDir, origSpec.ExecutableName())

		// Create pre-existing valid binary file
		preExistingData := []byte("PRE_EXISTING_BINARY_DATA_V1")
		if err := os.WriteFile(targetExec, preExistingData, 0755); err != nil {
			t.Fatalf("failed to write pre-existing binary: %v", err)
		}

		// Now simulate a failed download that fails midway
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		testSpec := origSpec
		testDownloads := make(map[string]binaries.DownloadSpec)
		for k, v := range origSpec.Downloads {
			testDownloads[k] = v
		}
		testDownloads[platformKey] = binaries.DownloadSpec{
			URL:           server.URL + "/yt-dlp.exe",
			ArchiveFormat: binaries.ArchiveFormatRaw,
			ExtractFile:   "yt-dlp.exe",
		}
		testSpec.Downloads = testDownloads
		binaries.Registry[binaries.IDYtDlp] = testSpec
		defer func() { binaries.Registry[binaries.IDYtDlp] = origSpec }()

		_, err := svc.Download(ctx, binaries.IDYtDlp, nil)
		if err == nil {
			t.Fatal("expected download failure on HTTP 500")
		}

		// The pre-existing file MUST remain completely intact (atomic safety guarantee!)
		remainingData, err := os.ReadFile(targetExec)
		if err != nil {
			t.Fatalf("pre-existing binary was deleted or inaccessible: %v", err)
		}
		if !bytes.Equal(remainingData, preExistingData) {
			t.Fatalf("pre-existing binary was corrupted by failed download! Expected %s, got %s", preExistingData, remainingData)
		}
	})

	t.Run("RawBinaryAtomic_PermissionCheck", func(t *testing.T) {
		tmpDir := t.TempDir()
		dest := filepath.Join(tmpDir, "test_binary.exe")

		err := fsutil.WriteAtomic(dest, 0755, func(w io.Writer) error {
			_, err := w.Write([]byte("EXECUTABLE_PAYLOAD"))
			return err
		})
		if err != nil {
			t.Fatalf("WriteAtomic failed: %v", err)
		}

		info, err := os.Stat(dest)
		if err != nil {
			t.Fatalf("stat failed: %v", err)
		}
		if info.IsDir() {
			t.Fatal("expected file, got directory")
		}
	})
}

// Memory repo helper for tests
type mockRepo struct {
	paths map[binaries.BinaryID]string
}

func newMockRepo() *mockRepo {
	return &mockRepo{paths: make(map[binaries.BinaryID]string)}
}

func (m *mockRepo) GetCustomPath(ctx context.Context, id binaries.BinaryID) (string, error) {
	return m.paths[id], nil
}

func (m *mockRepo) GetAllCustomPaths(ctx context.Context) (map[binaries.BinaryID]string, error) {
	out := make(map[binaries.BinaryID]string)
	for k, v := range m.paths {
		out[k] = v
	}
	return out, nil
}

func (m *mockRepo) SetCustomPath(ctx context.Context, id binaries.BinaryID, customPath string) error {
	m.paths[id] = customPath
	return nil
}
