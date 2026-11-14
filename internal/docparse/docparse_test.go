package docparse

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestExtractDocumentFormats(t *testing.T) {
	dir := t.TempDir()

	t.Run("docx", func(t *testing.T) {
		p := filepath.Join(dir, "brief.docx")
		docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>Cảnh 1: Mở đầu</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Nhân vật chính bước vào phòng.</w:t></w:r></w:p>` +
			`</w:body></w:document>`
		writeZipFixture(t, p, map[string]string{"word/document.xml": docXML})

		format, got, err := ExtractText(p)
		if err != nil {
			t.Fatal(err)
		}
		if format != "docx" || !strings.Contains(got, "Cảnh 1: Mở đầu") || !strings.Contains(got, "Nhân vật chính bước vào phòng.") {
			t.Fatalf("unexpected docx output: format=%q got=%q", format, got)
		}
	})

	t.Run("odt", func(t *testing.T) {
		p := filepath.Join(dir, "outline.odt")
		odtXML := `<?xml version="1.0" encoding="UTF-8"?>` +
			`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" ` +
			`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">` +
			`<office:automatic-styles><style>ignored</style></office:automatic-styles>` +
			`<office:body><office:text>` +
			`<text:h>Tiêu đề</text:h>` +
			`<text:p>Nội dung<text:s text:c="2"/>kịch bản</text:p>` +
			`</office:text></office:body></office:document-content>`
		writeZipFixture(t, p, map[string]string{"content.xml": odtXML})

		format, got, err := ExtractText(p)
		if err != nil {
			t.Fatal(err)
		}
		if format != "odt" || strings.Contains(got, "ignored") || !strings.Contains(got, "Nội dung  kịch bản") {
			t.Fatalf("unexpected odt output: format=%q got=%q", format, got)
		}
	})

	t.Run("rtf", func(t *testing.T) {
		p := filepath.Join(dir, "notes.rtf")
		rtf := `{\rtf1\ansi{\fonttbl{\f0 Arial;}}C\u7843?nh\tab 2\par Tho\u7841?i nh\'e2n v\u7853?t}`
		if err := os.WriteFile(p, []byte(rtf), 0o644); err != nil {
			t.Fatal(err)
		}
		format, got, err := extractRTFFileForTest(p)
		if err != nil {
			t.Fatal(err)
		}
		if format != "rtf" || !strings.Contains(got, "Cảnh\t2") || !strings.Contains(got, "Thoại nhân vật") {
			t.Fatalf("unexpected rtf output: format=%q got=%q", format, got)
		}
	})

	t.Run("pdf_compressed_and_cmap", func(t *testing.T) {
		p := filepath.Join(dir, "script.pdf")
		// Flate-compressed content stream + ToUnicode CMap mapping glyph 0001 -> 'ệ' (U+1EC7).
		cmap := "/CIDInit /ProcSet findresource begin\nbeginbfchar\n<0001> <1EC7>\nendbfchar\nend"
		var cmapZ bytes.Buffer
		zw := zlib.NewWriter(&cmapZ)
		_, _ = zw.Write([]byte(cmap))
		_ = zw.Close()

		content := "BT /F1 12 Tf (Tieng Vi) Tj <0001> Tj [(t)] TJ T* (Dong hai) Tj ET"
		var contentZ bytes.Buffer
		cw := zlib.NewWriter(&contentZ)
		_, _ = cw.Write([]byte(content))
		_ = cw.Close()

		pdf := fmt.Sprintf("%%PDF-1.4\n"+
			"1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n"+
			"2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n"+
			"3 0 obj << /Type /Page /Parent 2 0 R /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >> endobj\n"+
			"4 0 obj << /Type /Font /Subtype /Type0 /ToUnicode 6 0 R >> endobj\n"+
			"5 0 obj << /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream\nendobj\n"+
			"6 0 obj << /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream\nendobj\n%%%%EOF\n",
			contentZ.Len(), contentZ.String(), cmapZ.Len(), cmapZ.String())
		if err := os.WriteFile(p, []byte(pdf), 0o644); err != nil {
			t.Fatal(err)
		}

		format, got, err := ExtractText(p)
		if err != nil {
			t.Fatal(err)
		}
		if format != "pdf" || !strings.Contains(got, "Tieng Việt") || !strings.Contains(got, "Dong hai") {
			t.Fatalf("unexpected pdf output: format=%q got=%q", format, got)
		}
	})

	t.Run("pdf_empty_returns_error", func(t *testing.T) {
		p := filepath.Join(dir, "scan.pdf")
		pdf := "%PDF-1.4\n1 0 obj << /Type /Page /Contents 2 0 R >> endobj\n2 0 obj << /Length 2 >>\nstream\n()\nendstream\nendobj\n%%EOF\n"
		if err := os.WriteFile(p, []byte(pdf), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ExtractText(p); err == nil {
			t.Fatal("expected descriptive error for scanned/empty PDF")
		}
	})
}

func extractRTFFileForTest(path string) (string, string, error) {
	return ExtractText(path)
}

func TestExtractDocumentSupportsLargeDocsAndReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("a", 300_000)
	docPath := filepath.Join(dir, "large.docx")
	writeZipFixture(t, docPath, map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>` + body + `</w:t></w:r></w:p>` +
			`</w:body></w:document>`,
	})

	format, text, err := ExtractText(docPath)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if format != "docx" {
		t.Fatalf("unexpected format %q", format)
	}
	if got := len([]rune(text)); got != 300_000 {
		t.Fatalf("expected 300000 runes without 250k truncation, got %d", got)
	}

	_, bounded, truncated, err := ExtractTextBounded(docPath, 50)
	if err != nil {
		t.Fatalf("ExtractTextBounded: %v", err)
	}
	if !truncated || len([]rune(bounded)) != 50 {
		t.Fatalf("expected truncated=true and 50 runes, got truncated=%v len=%d", truncated, len([]rune(bounded)))
	}

	_, unbounded, truncatedZero, err := ExtractTextBounded(docPath, 0)
	if err != nil {
		t.Fatalf("ExtractTextBounded(0): %v", err)
	}
	if truncatedZero || len([]rune(unbounded)) != 300_000 {
		t.Fatalf("expected unbounded 300000 runes when maxRunes=0, got truncated=%v len=%d", truncatedZero, len([]rune(unbounded)))
	}
}

func TestExtractDocumentErrorsAndLimits(t *testing.T) {
	if MaxDocumentRunes != 1_000_000 {
		t.Fatalf("expected MaxDocumentRunes=1_000_000, got %d", MaxDocumentRunes)
	}

	dir := t.TempDir()
	if _, _, err := ExtractText(dir); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("expected folder error, got %v", err)
	}

	txtPath := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(txtPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExtractText(txtPath); err == nil || !strings.Contains(err.Error(), "unsupported document format") {
		t.Fatalf("expected unsupported format error, got %v", err)
	}

	badPDF := filepath.Join(dir, "bad.pdf")
	if err := os.WriteFile(badPDF, []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExtractText(badPDF); err == nil || !strings.Contains(err.Error(), "invalid PDF header") {
		t.Fatalf("expected invalid PDF header error, got %v", err)
	}

	badDOCX := filepath.Join(dir, "bad.docx")
	writeZipFixture(t, badDOCX, map[string]string{"other.xml": "<root/>"})
	if _, _, err := ExtractText(badDOCX); err == nil || !strings.Contains(err.Error(), "word/document.xml") {
		t.Fatalf("expected missing word/document.xml error, got %v", err)
	}

	badODT := filepath.Join(dir, "bad.odt")
	writeZipFixture(t, badODT, map[string]string{"meta.xml": "<root/>"})
	if _, _, err := ExtractText(badODT); err == nil || !strings.Contains(err.Error(), "content.xml") {
		t.Fatalf("expected missing content.xml error, got %v", err)
	}
}
