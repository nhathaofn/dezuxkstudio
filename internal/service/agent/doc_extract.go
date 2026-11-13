package agent

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	maxDocumentFileBytes         = 32 << 20 // 32 MiB max document file size
	maxDocumentUncompressedBytes = 16 << 20 // 16 MiB bound on decompressed XML or stream payloads
	maxExtractedDocumentRunes    = 250_000  // safety cap so gigantic docs stay bounded in memory
)

// extractDocumentText extracts clean plain text from .docx, .odt, .rtf, and .pdf files
// using only the Go standard library and bounded memory/decompression limits.
func extractDocumentText(path string) (string, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("target path is a folder; use workspace_list_files")
	}
	if info.Size() > maxDocumentFileBytes {
		return "", "", fmt.Errorf("document exceeds maximum readable size (%d bytes)", maxDocumentFileBytes)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".docx":
		text, err := extractDOCXText(path)
		return "docx", text, err
	case ".odt":
		text, err := extractODTText(path)
		return "odt", text, err
	case ".rtf":
		data, err := readBoundedFile(path, maxDocumentFileBytes)
		if err != nil {
			return "", "", err
		}
		return "rtf", extractRTFText(data), nil
	case ".pdf":
		data, err := readBoundedFile(path, maxDocumentFileBytes)
		if err != nil {
			return "", "", err
		}
		text, err := extractPDFText(data)
		return "pdf", text, err
	default:
		return "", "", fmt.Errorf("unsupported document format %q", ext)
	}
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readBoundedStream(f, limit)
}

func readBoundedStream(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("document content exceeds safety limit (%d bytes)", limit)
	}
	return data, nil
}

func normalizeExtractedText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, line := range lines {
		trimmedRight := strings.TrimRight(line, " \t")
		if trimmedRight == "" {
			blanks++
			if blanks <= 1 && len(out) > 0 {
				out = append(out, "")
			}
			continue
		}
		blanks = 0
		out = append(out, trimmedRight)
	}
	res := strings.TrimSpace(strings.Join(out, "\n"))
	if r := []rune(res); len(r) > maxExtractedDocumentRunes {
		res = string(r[:maxExtractedDocumentRunes])
	}
	return res
}

// extractDOCXText reads body paragraphs and tables from word/document.xml inside a .docx archive.
func extractDOCXText(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx archive: %w", err)
	}
	defer zr.Close()

	var docFile *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "word/document.xml") {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("invalid docx: missing word/document.xml")
	}
	rc, err := docFile.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	xmlBytes, err := readBoundedStream(rc, maxDocumentUncompressedBytes)
	if err != nil {
		return "", err
	}
	dec := xml.NewDecoder(bytes.NewReader(xmlBytes))
	var sb strings.Builder
	var inTextRun bool
	var paragraphHasText bool

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse docx xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inTextRun = true
			case "tab":
				sb.WriteByte('\t')
				paragraphHasText = true
			case "br", "cr":
				sb.WriteByte('\n')
				paragraphHasText = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inTextRun = false
			case "tc":
				if paragraphHasText {
					sb.WriteByte('\t')
				}
			case "tr":
				sb.WriteByte('\n')
				paragraphHasText = false
			case "p":
				if paragraphHasText {
					sb.WriteByte('\n')
					paragraphHasText = false
				}
			}
		case xml.CharData:
			if inTextRun && len(t) > 0 {
				sb.Write(t)
				paragraphHasText = true
			}
		}
	}
	return normalizeExtractedText(sb.String()), nil
}

// extractODTText reads paragraphs and headings from content.xml inside an .odt archive.
func extractODTText(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open odt archive: %w", err)
	}
	defer zr.Close()

	var contentFile *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "content.xml") {
			contentFile = f
			break
		}
	}
	if contentFile == nil {
		return "", fmt.Errorf("invalid odt: missing content.xml")
	}
	rc, err := contentFile.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	xmlBytes, err := readBoundedStream(rc, maxDocumentUncompressedBytes)
	if err != nil {
		return "", err
	}
	dec := xml.NewDecoder(bytes.NewReader(xmlBytes))
	var sb strings.Builder
	var inBody bool
	var skipDepth int

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse odt xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "body" {
				inBody = true
				continue
			}
			if !inBody {
				continue
			}
			if skipDepth > 0 || t.Name.Local == "note-citation" || t.Name.Local == "annotation" {
				skipDepth++
				continue
			}
			switch t.Name.Local {
			case "tab":
				sb.WriteByte('\t')
			case "line-break":
				sb.WriteByte('\n')
			case "s":
				spaces := 1
				for _, attr := range t.Attr {
					if attr.Name.Local == "c" {
						if n, convErr := strconv.Atoi(attr.Value); convErr == nil && n > 0 {
							spaces = min(n, 64)
						}
					}
				}
				sb.WriteString(strings.Repeat(" ", spaces))
			}
		case xml.EndElement:
			if t.Name.Local == "body" {
				inBody = false
				continue
			}
			if skipDepth > 0 {
				skipDepth--
				continue
			}
			if inBody && (t.Name.Local == "p" || t.Name.Local == "h") {
				sb.WriteByte('\n')
			}
		case xml.CharData:
			if inBody && skipDepth == 0 {
				sb.Write(t)
			}
		}
	}
	return normalizeExtractedText(sb.String()), nil
}

type rtfState struct {
	uc   int
	skip bool
}

var rtfSkipDestinations = map[string]bool{
	"fonttbl": true, "colortbl": true, "stylesheet": true, "info": true,
	"pict": true, "object": true, "fldinst": true, "listtable": true,
	"listoverridetable": true, "revtbl": true, "rsidtbl": true,
	"mmathPr": true, "xmlnstbl": true, "themedata": true, "colorschememapping": true,
	"latentstyles": true, "datastore": true,
}

var win1252Table = [32]rune{
	0x20AC, 0xFFFD, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0xFFFD, 0x017D, 0xFFFD,
	0xFFFD, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0xFFFD, 0x017E, 0x0178,
}

func decodeWin1252Byte(b byte) rune {
	if b >= 0x80 && b <= 0x9F {
		return win1252Table[b-0x80]
	}
	return rune(b)
}

// extractRTFText converts an RTF document into plain UTF-8 text, skipping non-body destinations
// and decoding Unicode (\uN) and hex (\'hh) escapes.
func extractRTFText(data []byte) string {
	var sb strings.Builder
	stack := []rtfState{{uc: 1, skip: false}}
	skipChars := 0
	var highSurrogate uint16

	flushSurrogate := func() {
		if highSurrogate != 0 {
			sb.WriteRune(utf8.RuneError)
			highSurrogate = 0
		}
	}

	i := 0
	for i < len(data) {
		cur := &stack[len(stack)-1]
		b := data[i]
		switch b {
		case '{':
			flushSurrogate()
			skipChars = 0
			stack = append(stack, *cur)
			i++
		case '}':
			flushSurrogate()
			skipChars = 0
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			i++
		case '\\':
			i++
			if i >= len(data) {
				break
			}
			next := data[i]
			switch {
			case next == '{' || next == '}' || next == '\\':
				i++
				if !cur.skip {
					if skipChars > 0 {
						skipChars--
					} else {
						flushSurrogate()
						sb.WriteByte(next)
					}
				}
			case next == '\r' || next == '\n':
				i++
				if !cur.skip {
					flushSurrogate()
					sb.WriteByte('\n')
				}
			case next == '*':
				i++
				cur.skip = true
			case next == '~':
				i++
				if !cur.skip && skipChars == 0 {
					flushSurrogate()
					sb.WriteByte(' ')
				} else if skipChars > 0 {
					skipChars--
				}
			case next == '\'':
				if i+2 < len(data) {
					hexStr := string(data[i+1 : i+3])
					i += 3
					if !cur.skip {
						if skipChars > 0 {
							skipChars--
						} else if v, err := strconv.ParseUint(hexStr, 16, 8); err == nil {
							flushSurrogate()
							sb.WriteRune(decodeWin1252Byte(byte(v)))
						}
					}
				} else {
					i++
				}
			case (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z'):
				wordStart := i
				for i < len(data) && ((data[i] >= 'a' && data[i] <= 'z') || (data[i] >= 'A' && data[i] <= 'Z')) {
					i++
				}
				word := string(data[wordStart:i])
				hasParam := false
				param := 0
				if i < len(data) && (data[i] == '-' || (data[i] >= '0' && data[i] <= '9')) {
					hasParam = true
					neg := false
					if data[i] == '-' {
						neg = true
						i++
					}
					for i < len(data) && data[i] >= '0' && data[i] <= '9' {
						param = param*10 + int(data[i]-'0')
						i++
					}
					if neg {
						param = -param
					}
				}
				if i < len(data) && data[i] == ' ' {
					i++
				}

				if rtfSkipDestinations[word] {
					cur.skip = true
					continue
				}
				if cur.skip {
					continue
				}
				switch word {
				case "uc":
					if hasParam && param >= 0 {
						cur.uc = param
					}
				case "u":
					if hasParam {
						code := uint16(param)
						if code >= 0xD800 && code <= 0xDBFF {
							flushSurrogate()
							highSurrogate = code
						} else if code >= 0xDC00 && code <= 0xDFFF && highSurrogate != 0 {
							sb.WriteRune(utf16.DecodeRune(rune(highSurrogate), rune(code)))
							highSurrogate = 0
						} else {
							flushSurrogate()
							sb.WriteRune(rune(code))
						}
						skipChars = cur.uc
					}
				case "par", "line":
					flushSurrogate()
					skipChars = 0
					sb.WriteByte('\n')
				case "tab":
					flushSurrogate()
					skipChars = 0
					sb.WriteByte('\t')
				case "emdash":
					flushSurrogate()
					sb.WriteRune('—')
				case "endash":
					flushSurrogate()
					sb.WriteRune('–')
				case "lquote":
					flushSurrogate()
					sb.WriteRune('‘')
				case "rquote":
					flushSurrogate()
					sb.WriteRune('’')
				case "ldblquote":
					flushSurrogate()
					sb.WriteRune('“')
				case "rdblquote":
					flushSurrogate()
					sb.WriteRune('”')
				case "bullet":
					flushSurrogate()
					sb.WriteRune('•')
				}
			default:
				i++
			}
		case '\r', '\n':
			i++
		default:
			if !cur.skip {
				if skipChars > 0 {
					skipChars--
				} else {
					flushSurrogate()
					sb.WriteByte(b)
				}
			}
			i++
		}
	}
	return normalizeExtractedText(sb.String())
}

// --- Pure Go PDF text extractor (streams, FlateDecode, ObjStm, ToUnicode CMaps) ---

var (
	pdfObjHeaderRe    = regexp.MustCompile(`(?m)(\d+)\s+(\d+)\s+obj\b`)
	pdfRefRe          = regexp.MustCompile(`(\d+)\s+\d+\s+R\b`)
	pdfFontRefRe      = regexp.MustCompile(`/(F[A-Za-z0-9_.\-+]*|[A-Za-z0-9_.\-+]+)\s+(\d+)\s+\d+\s+R`)
	pdfTypePageRe     = regexp.MustCompile(`/Type\s*/Page(?:\b|[^A-Za-z0-9])`)
	pdfTypePagesRe    = regexp.MustCompile(`/Type\s*/Pages\b`)
	pdfTypeObjStmRe   = regexp.MustCompile(`/Type\s*/ObjStm\b`)
	pdfLengthRe       = regexp.MustCompile(`/Length\s+(\d+)`)
	pdfObjStmNRe      = regexp.MustCompile(`/N\s+(\d+)`)
	pdfObjStmFirstRe  = regexp.MustCompile(`/First\s+(\d+)`)
	pdfContentsArrRe  = regexp.MustCompile(`/Contents\s*\[([^\]]*)\]`)
	pdfContentsRefRe  = regexp.MustCompile(`/Contents\s+(\d+)\s+\d+\s+R`)
	pdfKidsArrRe      = regexp.MustCompile(`/Kids\s*\[([^\]]*)\]`)
	pdfToUnicodeRefRe = regexp.MustCompile(`/ToUnicode\s+(\d+)\s+\d+\s+R`)
	pdfFontDictRefRe  = regexp.MustCompile(`/Font\s+(\d+)\s+\d+\s+R`)
)

type pdfObject struct {
	id     int
	dict   string
	stream []byte
}

type pdfCMap struct {
	codeLens []int // sorted descending byte lengths seen in bfchar/bfrange (e.g. [2, 1])
	mapping  map[uint32]string
}

func extractPDFText(data []byte) (string, error) {
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	if !bytes.Contains(head, []byte("%PDF-")) {
		return "", fmt.Errorf("invalid PDF header")
	}

	objs := parsePDFObjects(data)
	// Expand PDF 1.5+ /ObjStm compressed objects so pages/fonts stored there are visible too.
	for _, obj := range objs {
		if !pdfTypeObjStmRe.MatchString(obj.dict) || len(obj.stream) == 0 {
			continue
		}
		expanded := expandPDFObjStm(obj)
		for id, sub := range expanded {
			if _, exists := objs[id]; !exists {
				objs[id] = sub
			}
		}
	}

	// Build font ToUnicode CMap cache keyed by font object ID.
	fontCMaps := make(map[int]*pdfCMap)
	for id, obj := range objs {
		if m := pdfToUnicodeRefRe.FindStringSubmatch(obj.dict); m != nil {
			cmapID, _ := strconv.Atoi(m[1])
			if cmapObj, ok := objs[cmapID]; ok && len(cmapObj.stream) > 0 {
				if cm := parsePDFCMap(cmapObj.stream); cm != nil {
					fontCMaps[id] = cm
				}
			}
		}
	}

	pageIDs := collectPDFPageOrder(objs)
	if len(pageIDs) == 0 {
		return "", fmt.Errorf("no selectable text found in PDF (it may be scanned images or encrypted)")
	}

	var pagesOut []string
	for _, pid := range pageIDs {
		pobj := objs[pid]
		fontMap := resolvePageFonts(pobj.dict, objs, fontCMaps)
		contentIDs := resolvePageContents(pobj.dict)
		var pageBuf strings.Builder
		for _, cid := range contentIDs {
			cobj, ok := objs[cid]
			if !ok || len(cobj.stream) == 0 {
				continue
			}
			extractPDFContentStream(cobj.stream, fontMap, &pageBuf)
		}
		if cleaned := strings.TrimSpace(pageBuf.String()); cleaned != "" {
			pagesOut = append(pagesOut, cleaned)
		}
	}

	joined := normalizeExtractedText(strings.Join(pagesOut, "\n\n"))
	if joined == "" {
		return "", fmt.Errorf("no selectable text found in PDF (it may be scanned images or encrypted)")
	}
	return joined, nil
}

func parsePDFObjects(data []byte) map[int]pdfObject {
	objs := make(map[int]pdfObject)
	n := len(data)
	pos := 0
	for pos < n {
		loc := pdfObjHeaderRe.FindSubmatchIndex(data[pos:])
		if loc == nil {
			break
		}
		idStart, idEnd := pos+loc[2], pos+loc[3]
		id, _ := strconv.Atoi(string(data[idStart:idEnd]))
		bodyStart := pos + loc[1]

		streamIdx := findPDFKeywordBeforeEndObj(data, bodyStart)
		if streamIdx >= 0 {
			dictText := string(data[bodyStart:streamIdx])
			rawStart := streamIdx + len("stream")
			if rawStart < n && data[rawStart] == '\r' {
				rawStart++
			}
			if rawStart < n && data[rawStart] == '\n' {
				rawStart++
			}
			rawEnd := findPDFStreamEnd(data, rawStart, dictText)
			raw := data[rawStart:rawEnd]
			decoded := decodePDFStream(dictText, raw)
			objs[id] = pdfObject{id: id, dict: dictText, stream: decoded}

			if nextEnd := bytes.Index(data[rawEnd:], []byte("endobj")); nextEnd >= 0 {
				pos = rawEnd + nextEnd + len("endobj")
			} else {
				pos = rawEnd + len("endstream")
			}
			continue
		}

		endIdx := bytes.Index(data[bodyStart:], []byte("endobj"))
		if endIdx < 0 {
			break
		}
		objs[id] = pdfObject{id: id, dict: string(data[bodyStart : bodyStart+endIdx])}
		pos = bodyStart + endIdx + len("endobj")
	}
	return objs
}

func findPDFKeywordBeforeEndObj(data []byte, start int) int {
	endObj := bytes.Index(data[start:], []byte("endobj"))
	window := data[start:]
	if endObj >= 0 {
		window = data[start : start+endObj]
	}
	streamIdx := bytes.Index(window, []byte("stream"))
	if streamIdx < 0 {
		return -1
	}
	// Verify "stream" is followed immediately by CR/LF (PDF spec requirement).
	after := start + streamIdx + len("stream")
	if after >= len(data) || (data[after] != '\r' && data[after] != '\n') {
		return -1
	}
	return start + streamIdx
}

func findPDFStreamEnd(data []byte, rawStart int, dictText string) int {
	if m := pdfLengthRe.FindStringSubmatch(dictText); m != nil {
		if l, err := strconv.Atoi(m[1]); err == nil && l >= 0 && rawStart+l <= len(data) {
			tail := bytes.TrimSpace(data[rawStart+l : min(len(data), rawStart+l+32)])
			if bytes.HasPrefix(tail, []byte("endstream")) {
				return rawStart + l
			}
		}
	}
	idx := bytes.Index(data[rawStart:], []byte("endstream"))
	if idx < 0 {
		return len(data)
	}
	end := rawStart + idx
	if end > rawStart && data[end-1] == '\n' {
		end--
	}
	if end > rawStart && data[end-1] == '\r' {
		end--
	}
	return end
}

func decodePDFStream(dict string, raw []byte) []byte {
	if strings.Contains(dict, "/DCTDecode") || strings.Contains(dict, "/JPXDecode") || strings.Contains(dict, "/CCITTFaxDecode") {
		return nil
	}
	if strings.Contains(dict, "/FlateDecode") {
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil
		}
		defer zr.Close()
		out, err := io.ReadAll(io.LimitReader(zr, maxDocumentUncompressedBytes+1))
		if err != nil || int64(len(out)) > maxDocumentUncompressedBytes {
			return nil
		}
		return out
	}
	if strings.Contains(dict, "/Filter") {
		return nil
	}
	if int64(len(raw)) > maxDocumentUncompressedBytes {
		return nil
	}
	return raw
}

func expandPDFObjStm(obj pdfObject) map[int]pdfObject {
	nMatch := pdfObjStmNRe.FindStringSubmatch(obj.dict)
	fMatch := pdfObjStmFirstRe.FindStringSubmatch(obj.dict)
	if nMatch == nil || fMatch == nil {
		return nil
	}
	count, err1 := strconv.Atoi(nMatch[1])
	first, err2 := strconv.Atoi(fMatch[1])
	if err1 != nil || err2 != nil || first <= 0 || first > len(obj.stream) || count <= 0 {
		return nil
	}
	fields := strings.Fields(string(obj.stream[:first]))
	if len(fields) < count*2 {
		return nil
	}
	type entry struct {
		id, off int
	}
	entries := make([]entry, 0, count)
	for i := 0; i < count; i++ {
		id, errID := strconv.Atoi(fields[i*2])
		off, errOff := strconv.Atoi(fields[i*2+1])
		if errID != nil || errOff != nil || first+off > len(obj.stream) {
			continue
		}
		entries = append(entries, entry{id: id, off: off})
	}
	out := make(map[int]pdfObject, len(entries))
	body := obj.stream[first:]
	for i, e := range entries {
		end := len(body)
		if i+1 < len(entries) && entries[i+1].off > e.off {
			end = entries[i+1].off
		}
		if e.off < end && end <= len(body) {
			out[e.id] = pdfObject{id: e.id, dict: string(body[e.off:end])}
		}
	}
	return out
}

func collectPDFPageOrder(objs map[int]pdfObject) []int {
	var rootPagesIDs []int
	for id, obj := range objs {
		if pdfTypePagesRe.MatchString(obj.dict) && !strings.Contains(obj.dict, "/Parent") {
			rootPagesIDs = append(rootPagesIDs, id)
		}
	}
	sort.Ints(rootPagesIDs)
	var ordered []int
	seen := make(map[int]bool)
	var walkPages func(id int)
	walkPages = func(id int) {
		if seen[id] {
			return
		}
		seen[id] = true
		obj, ok := objs[id]
		if !ok {
			return
		}
		if m := pdfKidsArrRe.FindStringSubmatch(obj.dict); m != nil {
			for _, ref := range pdfRefRe.FindAllStringSubmatch(m[1], -1) {
				childID, _ := strconv.Atoi(ref[1])
				walkPages(childID)
			}
			return
		}
		if pdfTypePageRe.MatchString(obj.dict) {
			ordered = append(ordered, id)
		}
	}
	for _, rootID := range rootPagesIDs {
		walkPages(rootID)
	}
	if len(ordered) > 0 {
		return ordered
	}
	for id, obj := range objs {
		if pdfTypePageRe.MatchString(obj.dict) {
			ordered = append(ordered, id)
		}
	}
	sort.Ints(ordered)
	return ordered
}

func resolvePageContents(dict string) []int {
	if m := pdfContentsArrRe.FindStringSubmatch(dict); m != nil {
		refs := pdfRefRe.FindAllStringSubmatch(m[1], -1)
		out := make([]int, 0, len(refs))
		for _, r := range refs {
			id, _ := strconv.Atoi(r[1])
			out = append(out, id)
		}
		return out
	}
	if m := pdfContentsRefRe.FindStringSubmatch(dict); m != nil {
		id, _ := strconv.Atoi(m[1])
		return []int{id}
	}
	return nil
}

func resolvePageFonts(pageDict string, objs map[int]pdfObject, fontCMaps map[int]*pdfCMap) map[string]*pdfCMap {
	fontSection := pageDict
	if m := pdfFontDictRefRe.FindStringSubmatch(pageDict); m != nil {
		id, _ := strconv.Atoi(m[1])
		if fdict, ok := objs[id]; ok {
			fontSection += "\n" + fdict.dict
		}
	}
	out := make(map[string]*pdfCMap)
	for _, m := range pdfFontRefRe.FindAllStringSubmatch(fontSection, -1) {
		name := m[1]
		id, _ := strconv.Atoi(m[2])
		if cm := fontCMaps[id]; cm != nil {
			out[name] = cm
		}
	}
	return out
}

func parsePDFCMap(stream []byte) *pdfCMap {
	s := string(stream)
	cm := &pdfCMap{mapping: make(map[uint32]string)}
	seenLens := make(map[int]bool)

	parseHexCode := func(tok string) (uint32, int, bool) {
		tok = strings.TrimPrefix(strings.TrimSuffix(tok, ">"), "<")
		if tok == "" || len(tok) > 8 {
			return 0, 0, false
		}
		v, err := strconv.ParseUint(tok, 16, 32)
		if err != nil {
			return 0, 0, false
		}
		return uint32(v), (len(tok) + 1) / 2, true
	}

	hexSeqToString := func(hexStr string) string {
		hexStr = strings.TrimPrefix(strings.TrimSuffix(hexStr, ">"), "<")
		if len(hexStr)%2 != 0 {
			hexStr += "0"
		}
		raw := make([]byte, 0, len(hexStr)/2)
		for i := 0; i < len(hexStr); i += 2 {
			b, err := strconv.ParseUint(hexStr[i:i+2], 16, 8)
			if err == nil {
				raw = append(raw, byte(b))
			}
		}
		return decodeUTF16BEBytes(raw)
	}

	hexTokRe := regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
	bfCharBlockRe := regexp.MustCompile(`(?s)beginbfchar\s*(.*?)\s*endbfchar`)
	for _, blk := range bfCharBlockRe.FindAllStringSubmatch(s, -1) {
		hexs := hexTokRe.FindAllStringSubmatch(blk[1], -1)
		for i := 0; i+1 < len(hexs); i += 2 {
			code, byteLen, ok := parseHexCode(hexs[i][1])
			if !ok {
				continue
			}
			cm.mapping[code] = hexSeqToString(hexs[i+1][1])
			seenLens[byteLen] = true
		}
	}

	bfRangeBlockRe := regexp.MustCompile(`(?s)beginbfrange\s*(.*?)\s*endbfrange`)
	for _, blk := range bfRangeBlockRe.FindAllStringSubmatch(s, -1) {
		lines := strings.Split(blk[1], "\n")
		for _, line := range lines {
			hexs := hexTokRe.FindAllStringSubmatch(line, -1)
			if len(hexs) < 3 {
				continue
			}
			startCode, byteLen, ok1 := parseHexCode(hexs[0][1])
			endCode, _, ok2 := parseHexCode(hexs[1][1])
			if !ok1 || !ok2 || endCode < startCode || endCode-startCode > 4096 {
				continue
			}
			seenLens[byteLen] = true
			if strings.Contains(line, "[") {
				for idx, dst := range hexs[2:] {
					c := startCode + uint32(idx)
					if c > endCode {
						break
					}
					cm.mapping[c] = hexSeqToString(dst[1])
				}
				continue
			}
			dstStart, _, ok3 := parseHexCode(hexs[2][1])
			if !ok3 {
				continue
			}
			for c := startCode; c <= endCode; c++ {
				cm.mapping[c] = string(rune(dstStart + (c - startCode)))
			}
		}
	}

	if len(cm.mapping) == 0 {
		return nil
	}
	for l := range seenLens {
		cm.codeLens = append(cm.codeLens, l)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(cm.codeLens)))
	return cm
}

func decodeUTF16BEBytes(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) == 1 {
		return string(decodeWin1252Byte(raw[0]))
	}
	u16 := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		u16 = append(u16, uint16(raw[i])<<8|uint16(raw[i+1]))
	}
	return string(utf16.Decode(u16))
}

func (cm *pdfCMap) decodeBytes(raw []byte) string {
	var sb strings.Builder
	i := 0
	for i < len(raw) {
		matched := false
		for _, cl := range cm.codeLens {
			if i+cl > len(raw) {
				continue
			}
			var code uint32
			for k := 0; k < cl; k++ {
				code = (code << 8) | uint32(raw[i+k])
			}
			if val, ok := cm.mapping[code]; ok {
				sb.WriteString(val)
				i += cl
				matched = true
				break
			}
		}
		if !matched {
			sb.WriteRune(decodeWin1252Byte(raw[i]))
			i++
		}
	}
	return sb.String()
}

func decodePDFStringBytes(raw []byte, cm *pdfCMap) string {
	if cm != nil {
		return cm.decodeBytes(raw)
	}
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		return decodeUTF16BEBytes(raw[2:])
	}
	var sb strings.Builder
	for _, b := range raw {
		sb.WriteRune(decodeWin1252Byte(b))
	}
	return sb.String()
}

func extractPDFContentStream(stream []byte, fonts map[string]*pdfCMap, out *strings.Builder) {
	inText := false
	var curFont string
	type pdfOperand struct {
		isStr bool
		isArr bool
		isNum bool
		num   float64
		bytes []byte
		name  string
		arr   []pdfOperand
	}

	decodeOpText := func(op pdfOperand) string {
		return decodePDFStringBytes(op.bytes, fonts[curFont])
	}

	var stack []pdfOperand
	push := func(op pdfOperand) {
		if len(stack) > 64 {
			stack = stack[1:]
		}
		stack = append(stack, op)
	}

	i := 0
	n := len(stream)
	for i < n {
		b := stream[i]
		if b == '%' {
			for i < n && stream[i] != '\n' && stream[i] != '\r' {
				i++
			}
			continue
		}
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == 0 {
			i++
			continue
		}
		if b == '(' {
			strBytes, next := readPDFLiteralString(stream, i)
			i = next
			push(pdfOperand{isStr: true, bytes: strBytes})
			continue
		}
		if b == '<' {
			if i+1 < n && stream[i+1] == '<' {
				i += 2
				continue
			}
			hexBytes, next := readPDFHexString(stream, i)
			i = next
			push(pdfOperand{isStr: true, bytes: hexBytes})
			continue
		}
		if b == '>' {
			if i+1 < n && stream[i+1] == '>' {
				i += 2
			} else {
				i++
			}
			continue
		}
		if b == '[' {
			arr, next := readPDFArray(stream, i)
			i = next
			elems := make([]pdfOperand, 0, len(arr))
			for _, e := range arr {
				elems = append(elems, pdfOperand{isStr: e.isStr, isNum: e.isNum, num: e.num, bytes: e.bytes})
			}
			push(pdfOperand{isArr: true, arr: elems})
			continue
		}
		if b == '/' {
			i++
			start := i
			for i < n && !isPDFDelimiter(stream[i]) {
				i++
			}
			push(pdfOperand{name: string(stream[start:i])})
			continue
		}
		if (b >= '0' && b <= '9') || b == '-' || b == '+' || b == '.' {
			start := i
			i++
			for i < n && ((stream[i] >= '0' && stream[i] <= '9') || stream[i] == '.') {
				i++
			}
			if v, err := strconv.ParseFloat(string(stream[start:i]), 64); err == nil {
				push(pdfOperand{isNum: true, num: v})
			}
			continue
		}

		// Operator keyword (`BT`, `ET`, `Tf`, `Tj`, `TJ`, `Td`, `TD`, `T*`, `Tm`, `'`, `"`)
		var opName string
		if b == '\'' || b == '"' {
			opName = string(b)
			i++
		} else {
			start := i
			for i < n && !isPDFDelimiter(stream[i]) {
				i++
			}
			opName = string(stream[start:i])
		}

		switch opName {
		case "BT":
			inText = true
		case "ET":
			if inText && out.Len() > 0 {
				out.WriteByte('\n')
			}
			inText = false
		case "Tf":
			if len(stack) >= 2 && stack[len(stack)-2].name != "" {
				curFont = stack[len(stack)-2].name
			}
		case "T*":
			if inText {
				out.WriteByte('\n')
			}
		case "Td", "TD":
			if inText && len(stack) >= 2 && stack[len(stack)-1].isNum && stack[len(stack)-1].num != 0 {
				out.WriteByte('\n')
			}
		case "Tm":
			if inText && len(stack) >= 6 && stack[len(stack)-1].isNum && out.Len() > 0 {
				out.WriteByte('\n')
			}
		case "Tj":
			if inText && len(stack) >= 1 && stack[len(stack)-1].isStr {
				out.WriteString(decodeOpText(stack[len(stack)-1]))
			}
		case "'":
			if inText && len(stack) >= 1 && stack[len(stack)-1].isStr {
				out.WriteByte('\n')
				out.WriteString(decodeOpText(stack[len(stack)-1]))
			}
		case "\"":
			if inText && len(stack) >= 1 && stack[len(stack)-1].isStr {
				out.WriteByte('\n')
				out.WriteString(decodeOpText(stack[len(stack)-1]))
			}
		case "TJ":
			if inText && len(stack) >= 1 && stack[len(stack)-1].isArr {
				for _, elem := range stack[len(stack)-1].arr {
					if elem.isStr {
						out.WriteString(decodePDFStringBytes(elem.bytes, fonts[curFont]))
					} else if elem.isNum && elem.num < -120 {
						out.WriteByte(' ')
					}
				}
			}
		}
		stack = stack[:0]
	}
}

type pdfArrItem struct {
	isStr bool
	isNum bool
	num   float64
	bytes []byte
}

func readPDFArray(stream []byte, start int) ([]pdfArrItem, int) {
	i := start + 1
	n := len(stream)
	var items []pdfArrItem
	for i < n {
		b := stream[i]
		if b == ']' {
			return items, i + 1
		}
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			i++
			continue
		}
		if b == '(' {
			bs, next := readPDFLiteralString(stream, i)
			items = append(items, pdfArrItem{isStr: true, bytes: bs})
			i = next
			continue
		}
		if b == '<' {
			bs, next := readPDFHexString(stream, i)
			items = append(items, pdfArrItem{isStr: true, bytes: bs})
			i = next
			continue
		}
		if (b >= '0' && b <= '9') || b == '-' || b == '+' || b == '.' {
			numStart := i
			i++
			for i < n && ((stream[i] >= '0' && stream[i] <= '9') || stream[i] == '.') {
				i++
			}
			if v, err := strconv.ParseFloat(string(stream[numStart:i]), 64); err == nil {
				items = append(items, pdfArrItem{isNum: true, num: v})
			}
			continue
		}
		i++
	}
	return items, i
}

func readPDFLiteralString(stream []byte, start int) ([]byte, int) {
	var out []byte
	depth := 1
	i := start + 1
	n := len(stream)
	for i < n && depth > 0 {
		b := stream[i]
		if b == '(' {
			depth++
			out = append(out, b)
			i++
			continue
		}
		if b == ')' {
			depth--
			i++
			if depth > 0 {
				out = append(out, b)
			}
			continue
		}
		if b == '\\' {
			i++
			if i >= n {
				break
			}
			esc := stream[i]
			switch esc {
			case 'n':
				out = append(out, '\n')
				i++
			case 'r':
				out = append(out, '\r')
				i++
			case 't':
				out = append(out, '\t')
				i++
			case 'b':
				out = append(out, '\b')
				i++
			case 'f':
				out = append(out, '\f')
				i++
			case '\r':
				i++
				if i < n && stream[i] == '\n' {
					i++
				}
			case '\n':
				i++
			default:
				if esc >= '0' && esc <= '7' {
					val := int(esc - '0')
					i++
					for k := 0; k < 2 && i < n && stream[i] >= '0' && stream[i] <= '7'; k++ {
						val = val*8 + int(stream[i]-'0')
						i++
					}
					out = append(out, byte(val))
				} else {
					out = append(out, esc)
					i++
				}
			}
			continue
		}
		out = append(out, b)
		i++
	}
	return out, i
}

func readPDFHexString(stream []byte, start int) ([]byte, int) {
	i := start + 1
	n := len(stream)
	var hexDigits []byte
	for i < n && stream[i] != '>' {
		b := stream[i]
		if (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') {
			hexDigits = append(hexDigits, b)
		}
		i++
	}
	if i < n && stream[i] == '>' {
		i++
	}
	if len(hexDigits)%2 != 0 {
		hexDigits = append(hexDigits, '0')
	}
	out := make([]byte, 0, len(hexDigits)/2)
	for j := 0; j < len(hexDigits); j += 2 {
		v, err := strconv.ParseUint(string(hexDigits[j:j+2]), 16, 8)
		if err == nil {
			out = append(out, byte(v))
		}
	}
	return out, i
}

func isPDFDelimiter(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == 0 ||
		b == '(' || b == ')' || b == '<' || b == '>' || b == '[' || b == ']' ||
		b == '{' || b == '}' || b == '/' || b == '%'
}
