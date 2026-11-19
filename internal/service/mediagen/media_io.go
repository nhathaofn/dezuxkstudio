package mediagen

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/service/media"
)

type mediaOutput struct {
	data     []byte
	filePath string
	mimeType string
}

func (m *mediaOutput) cleanup() {
	if m != nil && m.filePath != "" {
		_ = os.Remove(m.filePath)
		m.filePath = ""
	}
}

type localImageData struct {
	Raw      []byte
	Base64   string
	MimeType string
	DataURI  string
	Filename string
}

func (s *Service) readReferenceImage(path string) (localImageData, error) {
	baseName := filepath.Base(path)
	info, err := os.Stat(path)
	if err != nil {
		code := apperr.CodeUnavailable
		if os.IsNotExist(err) {
			code = apperr.CodeNotFound
		}
		return localImageData{}, apperr.Wrap(code, "Cannot access reference image", err).
			WithMsg("errors.mediagen.reference_unreadable", map[string]any{"name": baseName})
	}
	if info.IsDir() {
		return localImageData{}, apperr.Validation("Reference path must be an image file")
	}
	if info.Size() > s.cfg.MaxReferenceBytes {
		return localImageData{}, apperr.Validation("Reference image exceeds maximum allowed size").
			WithMsg("errors.mediagen.reference_too_large", map[string]any{"name": baseName})
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return localImageData{}, apperr.Wrap(apperr.CodeUnavailable, "Cannot read reference image", err).
			WithMsg("errors.mediagen.reference_unreadable", map[string]any{"name": baseName})
	}
	mimeType := strings.Split(http.DetectContentType(raw), ";")[0]
	if mimeType == "application/octet-stream" || !strings.HasPrefix(mimeType, "image/") {
		mimeType = domainmedia.MimeForExt(filepath.Ext(path))
	}
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/png"
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	return localImageData{
		Raw:      raw,
		Base64:   b64,
		MimeType: mimeType,
		DataURI:  "data:" + mimeType + ";base64," + b64,
		Filename: baseName,
	}, nil
}

func (s *Service) decodeDataOrDownload(ctx context.Context, rawDataURIOrB64, remoteURL, fallbackExt string, headers map[string]string, trustedURLs ...string) ([]byte, string, error) {
	if rawDataURIOrB64 != "" {
		if strings.HasPrefix(rawDataURIOrB64, "data:") {
			return parseDataURI(rawDataURIOrB64)
		}
		if strings.HasPrefix(rawDataURIOrB64, "http://") || strings.HasPrefix(rawDataURIOrB64, "https://") {
			return s.downloadURL(ctx, rawDataURIOrB64, headers, trustedURLs...)
		}
		dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawDataURIOrB64))
		if err != nil {
			return nil, "", apperr.Wrap(apperr.CodeUnavailable, "Invalid base64 payload from provider", err)
		}
		return dec, domainmedia.MimeForExt(fallbackExt), nil
	}
	if remoteURL != "" {
		if strings.HasPrefix(remoteURL, "data:") {
			return parseDataURI(remoteURL)
		}
		return s.downloadURL(ctx, remoteURL, headers, trustedURLs...)
	}
	return nil, "", apperr.Unavailable("Provider returned neither media data nor download URL")
}

func (s *Service) decodeMediaOrDownload(ctx context.Context, rawDataURIOrB64, remoteURL, fallbackExt string, headers map[string]string, trustedURLs ...string) (mediaOutput, error) {
	if rawDataURIOrB64 != "" {
		if strings.HasPrefix(rawDataURIOrB64, "data:") {
			raw, ct, err := parseDataURI(rawDataURIOrB64)
			return mediaOutput{data: raw, mimeType: ct}, err
		}
		if strings.HasPrefix(rawDataURIOrB64, "http://") || strings.HasPrefix(rawDataURIOrB64, "https://") {
			return s.downloadURLToFile(ctx, rawDataURIOrB64, headers, trustedURLs...)
		}
		dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawDataURIOrB64))
		if err != nil {
			return mediaOutput{}, apperr.Wrap(apperr.CodeUnavailable, "Invalid base64 payload from provider", err)
		}
		return mediaOutput{data: dec, mimeType: domainmedia.MimeForExt(fallbackExt)}, nil
	}
	if remoteURL != "" {
		if strings.HasPrefix(remoteURL, "data:") {
			raw, ct, err := parseDataURI(remoteURL)
			return mediaOutput{data: raw, mimeType: ct}, err
		}
		return s.downloadURLToFile(ctx, remoteURL, headers, trustedURLs...)
	}
	return mediaOutput{}, apperr.Unavailable("Provider returned neither media data nor download URL")
}

func parseDataURI(uri string) ([]byte, string, error) {
	comma := strings.IndexByte(uri, ',')
	if comma < 0 {
		return nil, "", apperr.Unavailable("Malformed data URI from provider")
	}
	meta := uri[len("data:"):comma]
	payload := uri[comma+1:]
	mimeType := strings.TrimSpace(strings.Split(meta, ";")[0])
	var raw []byte
	var err error
	if strings.Contains(meta, ";base64") {
		raw, err = base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	} else {
		var unesc string
		unesc, err = url.PathUnescape(payload)
		raw = []byte(unesc)
	}
	if err != nil {
		return nil, "", apperr.Wrap(apperr.CodeUnavailable, "Failed decoding data URI", err)
	}
	return raw, mimeType, nil
}

func readHeadBytes(out mediaOutput, max int) []byte {
	if out.filePath == "" {
		if len(out.data) <= max {
			return out.data
		}
		return out.data[:max]
	}
	f, err := os.Open(out.filePath)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, max)
	n, _ := io.ReadFull(f, buf)
	return buf[:n]
}

func (s *Service) saveOutput(ctx context.Context, kind domainmedia.Kind, outputDir, prefix, mimeType, fallbackExt string, data []byte, t targetSpec) (domainmedia.Result, error) {
	return s.saveMediaOutput(ctx, kind, outputDir, prefix, fallbackExt, mediaOutput{data: data, mimeType: mimeType}, t)
}

func (s *Service) saveMediaOutput(ctx context.Context, kind domainmedia.Kind, outputDir, prefix, fallbackExt string, out mediaOutput, t targetSpec) (domainmedia.Result, error) {
	defer out.cleanup()

	mimeType := out.mimeType
	if mimeType == "" || mimeType == "application/octet-stream" {
		if kind == domainmedia.KindImage {
			sniff := strings.Split(http.DetectContentType(readHeadBytes(out, 512)), ";")[0]
			if strings.HasPrefix(sniff, "image/") {
				mimeType = sniff
			}
		}
		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = domainmedia.MimeForExt(fallbackExt)
		}
	}
	ext := domainmedia.ExtForMime(mimeType, fallbackExt)
	if ext == "" {
		ext = fallbackExt
	}

	mediaDir := s.getMediaDir()
	dir := strings.TrimSpace(outputDir)
	if dir == "" {
		sub := "images"
		switch kind {
		case domainmedia.KindVideo:
			sub = "videos"
		case domainmedia.KindTTS:
			sub = "tts"
		}
		dir = filepath.Join(mediaDir, sub)
	}
	if !filepath.IsAbs(dir) && mediaDir != "" {
		dir = filepath.Join(mediaDir, dir)
	}

	name := uniqueFilename(prefix, ext)
	fullPath := filepath.Join(dir, name)
	if err := engine.CheckFile(ctx, fullPath, true); err != nil {
		return domainmedia.Result{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return domainmedia.Result{}, apperr.Wrap(apperr.CodeUnavailable, "Failed creating media output directory", err)
	}

	var sizeBytes int64
	if out.filePath != "" {
		info, err := os.Stat(out.filePath)
		if err != nil {
			return domainmedia.Result{}, apperr.Wrap(apperr.CodeUnavailable, "Failed inspecting downloaded media output", err)
		}
		sizeBytes = info.Size()
		if err := fsutil.WriteAtomic(fullPath, 0o600, func(w io.Writer) error {
			src, err := os.Open(out.filePath)
			if err != nil {
				return err
			}
			defer src.Close()
			_, err = io.Copy(w, src)
			return err
		}); err != nil {
			return domainmedia.Result{}, apperr.Wrap(apperr.CodeUnavailable, "Failed writing generated media output", err)
		}
	} else {
		sizeBytes = int64(len(out.data))
		if err := fsutil.WriteFileAtomic(fullPath, out.data, 0o600); err != nil {
			return domainmedia.Result{}, apperr.Wrap(apperr.CodeUnavailable, "Failed writing generated media output", err)
		}
	}

	if reg := s.getRegistrar(); reg != nil {
		sourceNode := "mediagen"
		switch kind {
		case domainmedia.KindImage:
			sourceNode = "tool.generate_image"
		case domainmedia.KindVideo:
			sourceNode = "tool.generate_video"
		case domainmedia.KindTTS:
			sourceNode = "tool.generate_tts"
		}
		regRoot := mediaDir
		if strings.TrimSpace(outputDir) != "" {
			regRoot = outputDir
		}
		if regRoot == "" {
			regRoot = dir
		}
		asset, err := reg.RegisterAsset(ctx, media.RegisterAssetParams{
			Kind:           string(kind),
			SourceNode:     sourceNode,
			FilePath:       fullPath,
			RegisteredRoot: regRoot,
			MIMEType:       mimeType,
			SizeBytes:      sizeBytes,
		})
		if err != nil {
			s.log.WarnContext(ctx, "mediagen: failed to register generated media asset",
				"kind", string(kind),
				"source_node", sourceNode,
				"error", err,
			)
		} else if asset != nil {
			s.setAssetID(fullPath, asset.ID)
		}
	}

	return domainmedia.Result{
		Kind:      kind,
		Path:      fullPath,
		LocalURL:  fullPath,
		MimeType:  mimeType,
		Provider:  t.providerName,
		Model:     t.modelID,
		SizeBytes: sizeBytes,
	}, nil
}

func (s *Service) setAssetID(path, assetID string) {
	s.assetsMu.Lock()
	defer s.assetsMu.Unlock()
	if s.assets == nil {
		s.assets = make(map[string]string)
	}
	if _, exists := s.assets[path]; !exists {
		if len(s.assets) >= maxCachedAssetIDs && len(s.assetsOrder) > 0 {
			evict := s.assetsOrder[0]
			delete(s.assets, evict)
			s.assetsOrder = s.assetsOrder[1:]
			if cap(s.assetsOrder) > maxCachedAssetIDs*2 {
				compacted := make([]string, len(s.assetsOrder), maxCachedAssetIDs)
				copy(compacted, s.assetsOrder)
				s.assetsOrder = compacted
			}
		}
		s.assetsOrder = append(s.assetsOrder, path)
	}
	s.assets[path] = assetID
}

// AssetID returns the registered asset ID for a generated media file path, or empty string if not found.
func (s *Service) AssetID(path string) string {
	s.assetsMu.RLock()
	defer s.assetsMu.RUnlock()
	if s.assets == nil {
		return ""
	}
	return s.assets[path]
}

// SetRegistrar sets or updates the media asset registrar thread-safely.

func uniqueFilename(prefix, ext string) string {
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	ts := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s-%s-%s%s", prefix, ts, hex.EncodeToString(rnd[:]), ext)
}

func (s *Service) readReferenceImages(paths []string) ([]localImageData, error) {
	refs := make([]localImageData, 0, len(paths))
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		img, err := s.readReferenceImage(p)
		if err != nil {
			return nil, err
		}
		refs = append(refs, img)
	}
	return refs, nil
}

// LeaseRegistry tracks active job leases on files/directories to protect them from background GC.
