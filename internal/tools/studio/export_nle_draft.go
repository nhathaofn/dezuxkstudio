package studio

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/contracts"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/fsutil"
	"app/internal/progress"
	"app/internal/tools/mediautil"
)

const (
	// capCutDraftSchemaVersion is the CapCut / Jianying desktop draft_content.json schema version identifier.
	capCutDraftSchemaVersion = 360000
	// capCutSubtitleFontSize is the normalized CapCut desktop subtitle scale unit (8.0 = standard 1080p caption).
	capCutSubtitleFontSize = 8.0
	// defaultExportProjectName is the neutral default slug used when project_name is omitted.
	defaultExportProjectName = "studio_timeline"
)

// NewExportNLEDraftNode builds tool.export_nle_draft supporting CapCut/Jianying Draft (.zip), FCPXML v1.10, and OpenTimelineIO (.otio).
func NewExportNLEDraftNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)

	spec := engine.Spec{
		Type:        NodeTypeExportNLEDraft,
		Provider:    studioProviderID,
		Label:       "NLE Draft Exporter",
		Description: "Exports a multi-track EditTimeline to a CapCut/Jianying draft zip bundle (with optional copied media assets), Final Cut Pro XML (FCPXML v1.10), or OpenTimelineIO (.otio).",
		Inputs: []engine.Port{
			{Name: "timeline_json", Label: "EditTimeline JSON", Type: engine.PortText},
		},
		Outputs: []engine.Port{
			{Name: "export_path", Label: "Exported Draft File Path", Type: engine.PortText},
		},
		Params: []engine.Param{
			{Name: "format", Label: "Export Format", Kind: engine.ParamSelect, Options: optionStrings(domainstudio.NLEExportFormats()), Default: string(domainstudio.NLEFormatCapCut)},
			{Name: "output_dir", Label: "Output Directory", Kind: engine.ParamText, Role: engine.ParamRoleOutputDir},
			{Name: "project_name", Label: "Draft Project Name", Kind: engine.ParamText, Default: defaultExportProjectName},
			{Name: "include_media_copy", Label: "Include Media Files in Draft Zip", Kind: engine.ParamToggle, Default: "true"},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.ExportNLEDraftInput) (contracts.ExportNLEDraftOutput, error) {
		trimmed := strings.TrimSpace(in.TimelineJSON)
		if trimmed == "" {
			return contracts.ExportNLEDraftOutput{}, apperr.Validation("timeline_json is required").
				WithMsg("errors.studio.timeline_required", nil)
		}
		var tl domainstudio.EditTimeline
		if err := json.Unmarshal([]byte(trimmed), &tl); err != nil {
			return contracts.ExportNLEDraftOutput{}, apperr.Wrap(apperr.CodeValidation, "invalid timeline_json", err).
				WithMsg("errors.studio.invalid_timeline_json", nil)
		}
		if err := tl.ValidateWithMaxDim(opts.StudioConfig.MaxVideoDimension); err != nil {
			return contracts.ExportNLEDraftOutput{}, err
		}

		format := domainstudio.NLEExportFormat(strings.ToLower(strings.TrimSpace(in.Format)))
		if format == "" {
			format = domainstudio.NLEFormatCapCut
		}
		if !format.Valid() {
			return contracts.ExportNLEDraftOutput{}, apperr.Validation(fmt.Sprintf("unsupported export format %q", format)).
				WithMsg("errors.studio.invalid_export_format", map[string]any{"format": string(format)})
		}

		includeMedia := true
		if in.IncludeMediaCopy != nil {
			includeMedia = *in.IncludeMediaCopy
		}

		projectName := strings.TrimSpace(in.ProjectName)
		if projectName == "" {
			projectName = defaultExportProjectName
		}
		safeSlug := domainstudio.Slugify(projectName)

		outDir, err := resolveNodeOutputDir(ctx, opts, in.OutputDir, filepath.Join("studio", "exports"))
		if err != nil {
			return contracts.ExportNLEDraftOutput{}, err
		}

		for ti := range tl.Tracks {
			allowedKind := inputKindVideo | inputKindImage
			if tl.Tracks[ti].Kind != domainstudio.TrackVideo {
				allowedKind = inputKindAudio
			}
			for ci := range tl.Tracks[ti].Clips {
				if err := ctx.Err(); err != nil {
					return contracts.ExportNLEDraftOutput{}, err
				}
				rawP := strings.TrimSpace(tl.Tracks[ti].Clips[ci].FilePath)
				if rawP == "" {
					continue
				}
				p, rerr := resolveMediaInput(ctx, opts, rawP, allowedKind)
				if rerr != nil {
					return contracts.ExportNLEDraftOutput{}, rerr
				}
				tl.Tracks[ti].Clips[ci].FilePath = p
			}
		}

		progress.Report(ctx, progress.Update{
			Percent: 20,
			Message: fmt.Sprintf("Exporting %s project…", format),
			Key:     "studio.progress.export_nle",
			Params: map[string]string{
				"format":  string(format),
				"current": "0",
				"total":   "1",
			},
		})

		switch format {
		case domainstudio.NLEFormatCapCut, domainstudio.NLEFormatJianying:
			outPath := filepath.Join(outDir, uniqueExportFilename(safeSlug+"_"+string(format)+"_draft", ".zip"))
			if err := checkStudioFile(ctx, outPath, true); err != nil {
				return contracts.ExportNLEDraftOutput{}, err
			}
			fileCount, totalBytes, err := exportCapCutJianyingZip(ctx, tl, format, projectName, outPath, includeMedia, opts.StudioConfig.ExportMaxZipBytes)
			if err != nil {
				return contracts.ExportNLEDraftOutput{}, err
			}
			assetID := registerStudioAsset(ctx, opts, "archive", NodeTypeExportNLEDraft, outPath, outDir)
			progress.Report(ctx, progress.Update{Percent: 100, Message: "NLE draft exported", Key: "studio.progress.done"})
			return contracts.ExportNLEDraftOutput{
				ExportPath: outPath,
				AssetID:    assetID,
				Format:     string(format),
				FileCount:  fileCount,
				TotalBytes: totalBytes,
			}, nil

		case domainstudio.NLEFormatFCPXML:
			outPath := filepath.Join(outDir, uniqueExportFilename(safeSlug, ".fcpxml"))
			if err := checkStudioFile(ctx, outPath, true); err != nil {
				return contracts.ExportNLEDraftOutput{}, err
			}
			xmlBytes, err := buildFCPXML(tl, projectName)
			if err != nil {
				return contracts.ExportNLEDraftOutput{}, apperr.Internal(err)
			}
			if err := fsutil.WriteFileAtomic(outPath, xmlBytes, 0o600); err != nil {
				return contracts.ExportNLEDraftOutput{}, ioUnavailableErr("Failed to write FCPXML draft file", err)
			}
			assetID := registerStudioAsset(ctx, opts, "export", NodeTypeExportNLEDraft, outPath, outDir)
			progress.Report(ctx, progress.Update{Percent: 100, Message: "FCPXML exported", Key: "studio.progress.done"})
			return contracts.ExportNLEDraftOutput{
				ExportPath: outPath,
				AssetID:    assetID,
				Format:     string(format),
				FileCount:  1,
				TotalBytes: int64(len(xmlBytes)),
			}, nil

		case domainstudio.NLEFormatOTIO:
			outPath := filepath.Join(outDir, uniqueExportFilename(safeSlug, ".otio"))
			if err := checkStudioFile(ctx, outPath, true); err != nil {
				return contracts.ExportNLEDraftOutput{}, err
			}
			otioBytes, err := buildOTIOJSON(tl, projectName)
			if err != nil {
				return contracts.ExportNLEDraftOutput{}, apperr.Internal(err)
			}
			if err := fsutil.WriteFileAtomic(outPath, otioBytes, 0o600); err != nil {
				return contracts.ExportNLEDraftOutput{}, ioUnavailableErr("Failed to write OTIO timeline file", err)
			}
			assetID := registerStudioAsset(ctx, opts, "export", NodeTypeExportNLEDraft, outPath, outDir)
			progress.Report(ctx, progress.Update{Percent: 100, Message: "OTIO exported", Key: "studio.progress.done"})
			return contracts.ExportNLEDraftOutput{
				ExportPath: outPath,
				AssetID:    assetID,
				Format:     string(format),
				FileCount:  1,
				TotalBytes: int64(len(otioBytes)),
			}, nil
		}

		return contracts.ExportNLEDraftOutput{}, apperr.Validation("unsupported export format")
	})
}

func exportCapCutJianyingZip(
	ctx context.Context,
	tl domainstudio.EditTimeline,
	format domainstudio.NLEExportFormat,
	projectName, outPath string,
	includeMedia bool,
	maxZipBytes int64,
) (fileCount int, totalBytes int64, err error) {
	if maxZipBytes <= 0 {
		maxZipBytes = withStudioDefaults(config.StudioConfig{}).ExportMaxZipBytes
	}

	draftID := strings.ToUpper(uuid.NewString())
	nowUs := time.Now().UnixMicro()
	durationUs := tl.DurationMs() * 1000

	type matEntry struct {
		id         string
		srcPath    string
		zipRelPath string
		isAudio    bool
		isImage    bool
		durationUs int64
	}
	matByKey := make(map[string]matEntry)
	var materials []matEntry

	for _, tr := range tl.Tracks {
		isAudTrack := tr.Kind != domainstudio.TrackVideo && tr.Kind != domainstudio.TrackText
		for _, cl := range tr.Clips {
			if cl.FilePath == "" {
				continue
			}
			key := fmt.Sprintf("%v|%s", isAudTrack, cl.FilePath)
			if _, ok := matByKey[key]; ok {
				continue
			}
			matID := strings.ToUpper(uuid.NewString())
			base := filepath.Base(cl.FilePath)
			zipRel := "materials/" + fmt.Sprintf("%02d_%s", len(materials)+1, base)
			m := matEntry{
				id:         matID,
				srcPath:    cl.FilePath,
				zipRelPath: zipRel,
				isAudio:    isAudTrack,
				isImage:    mediautil.IsSupportedImageExt(cl.FilePath),
				durationUs: max(cl.SourceOutMs*1000, cl.TimelineSpanMs()*1000),
			}
			matByKey[key] = m
			materials = append(materials, m)
		}
	}

	var videoMats []map[string]any
	var audioMats []map[string]any
	var textMats []map[string]any
	var metaMaterials []map[string]any

	for _, m := range materials {
		matPath := m.srcPath
		if includeMedia {
			matPath = m.zipRelPath
		}
		if m.isAudio {
			audioMats = append(audioMats, map[string]any{
				"id":       m.id,
				"name":     filepath.Base(m.srcPath),
				"path":     matPath,
				"type":     "extract_music",
				"duration": m.durationUs,
			})
			metaMaterials = append(metaMaterials, map[string]any{
				"id":        m.id,
				"file_Path": matPath,
				"type":      2,
				"duration":  m.durationUs,
			})
		} else {
			matType := "video"
			if m.isImage {
				matType = "photo"
			}
			videoMats = append(videoMats, map[string]any{
				"id":       m.id,
				"name":     filepath.Base(m.srcPath),
				"path":     matPath,
				"type":     matType,
				"width":    tl.Width,
				"height":   tl.Height,
				"duration": m.durationUs,
			})
			metaMaterials = append(metaMaterials, map[string]any{
				"id":        m.id,
				"file_Path": matPath,
				"type":      0,
				"width":     tl.Width,
				"height":    tl.Height,
				"duration":  m.durationUs,
			})
		}
	}

	var capcutTracks []map[string]any
	for _, tr := range tl.Tracks {
		isAudTrack := tr.Kind != domainstudio.TrackVideo && tr.Kind != domainstudio.TrackText
		trackType := "video"
		if isAudTrack {
			trackType = "audio"
		}
		var segs []map[string]any
		for _, cl := range tr.Clips {
			key := fmt.Sprintf("%v|%s", isAudTrack, cl.FilePath)
			m := matByKey[key]
			segs = append(segs, map[string]any{
				"id":          strings.ToUpper(uuid.NewString()),
				"material_id": m.id,
				"source_timerange": map[string]int64{
					"start":    cl.SourceInMs * 1000,
					"duration": (cl.SourceOutMs - cl.SourceInMs) * 1000,
				},
				"target_timerange": map[string]int64{
					"start":    cl.TimelineStartMs * 1000,
					"duration": cl.TimelineSpanMs() * 1000,
				},
				"speed":  cl.Speed,
				"volume": tr.Volume * cl.Volume,
			})
		}
		capcutTracks = append(capcutTracks, map[string]any{
			"id":       strings.ToUpper(uuid.NewString()),
			"type":     trackType,
			"name":     tr.Name,
			"segments": segs,
		})
	}

	if len(tl.Subtitles) > 0 {
		var textSegs []map[string]any
		for _, sub := range tl.Subtitles {
			txtID := strings.ToUpper(uuid.NewString())
			textMats = append(textMats, map[string]any{
				"id":        txtID,
				"type":      "subtitle",
				"content":   sub.Text,
				"speaker":   sub.Speaker,
				"font_size": capCutSubtitleFontSize,
			})
			textSegs = append(textSegs, map[string]any{
				"id":          strings.ToUpper(uuid.NewString()),
				"material_id": txtID,
				"target_timerange": map[string]int64{
					"start":    sub.StartMs * 1000,
					"duration": (sub.EndMs - sub.StartMs) * 1000,
				},
			})
		}
		capcutTracks = append(capcutTracks, map[string]any{
			"id":       strings.ToUpper(uuid.NewString()),
			"type":     "text",
			"name":     "Subtitles",
			"segments": textSegs,
		})
	}

	draftContent := map[string]any{
		"id":       draftID,
		"name":     projectName,
		"version":  capCutDraftSchemaVersion,
		"fps":      float64(tl.FPS),
		"duration": durationUs,
		"canvas_config": map[string]any{
			"width":  tl.Width,
			"height": tl.Height,
			"ratio":  "original",
		},
		"platform": map[string]any{
			"os":         runtime.GOOS,
			"app_source": string(format),
		},
		"materials": map[string]any{
			"videos": videoMats,
			"audios": audioMats,
			"texts":  textMats,
		},
		"tracks": capcutTracks,
	}
	draftMeta := map[string]any{
		"draft_id":          draftID,
		"draft_name":        projectName,
		"tm_duration":       durationUs,
		"tm_draft_create":   nowUs,
		"tm_draft_modified": nowUs,
		"draft_materials":   metaMaterials,
	}

	contentBytes, err := json.MarshalIndent(draftContent, "", "  ")
	if err != nil {
		return 0, 0, err
	}
	metaBytes, err := json.MarshalIndent(draftMeta, "", "  ")
	if err != nil {
		return 0, 0, err
	}

	writeErr := fsutil.WriteAtomic(outPath, 0o600, func(dst io.Writer) error {
		fileCount = 0
		totalBytes = 0
		zw := zip.NewWriter(dst)

		writeEntry := func(name string, data []byte) error {
			if totalBytes+int64(len(data)) > maxZipBytes {
				return apperr.Validation(fmt.Sprintf("exported draft exceeds studio.export_max_zip_bytes (%d bytes)", maxZipBytes)).
					WithMsg("errors.studio.export_too_large", map[string]any{"max_bytes": maxZipBytes})
			}
			w, werr := zw.Create(name)
			if werr != nil {
				return ioUnavailableErr("Failed to create NLE draft zip entry", werr)
			}
			if _, werr := w.Write(data); werr != nil {
				return ioUnavailableErr("Failed to write NLE draft zip entry", werr)
			}
			fileCount++
			totalBytes += int64(len(data))
			return nil
		}

		prefix := domainstudio.Slugify(projectName) + "/"
		if err := writeEntry(prefix+"draft_content.json", append(contentBytes, '\n')); err != nil {
			_ = zw.Close()
			return err
		}
		if err := writeEntry(prefix+"draft_meta_info.json", append(metaBytes, '\n')); err != nil {
			_ = zw.Close()
			return err
		}

		if includeMedia {
			seenFiles := make(map[string]bool)
			totalMats := len(materials)
			for idx, m := range materials {
				if err := ctx.Err(); err != nil {
					_ = zw.Close()
					return err
				}
				if seenFiles[m.zipRelPath] {
					continue
				}
				seenFiles[m.zipRelPath] = true

				progress.Report(ctx, progress.Update{
					Percent: 25.0 + float64(idx+1)/float64(max(1, totalMats))*70.0,
					Message: fmt.Sprintf("Packaging %s media %d/%d…", format, idx+1, totalMats),
					Key:     "studio.progress.export_nle",
					Params: map[string]string{
						"format":  string(format),
						"current": strconv.Itoa(idx + 1),
						"total":   strconv.Itoa(totalMats),
					},
				})

				st, serr := os.Stat(m.srcPath)
				if serr != nil {
					_ = zw.Close()
					return apperr.NotFound(fmt.Sprintf("media file missing for export: %s", filepath.Base(m.srcPath))).
						WithMsg("errors.studio.media_not_found", nil)
				}
				if st.IsDir() {
					_ = zw.Close()
					return apperr.Validation("timeline media path must be a regular file").
						WithMsg("errors.studio.input_not_file", nil)
				}
				if totalBytes+st.Size() > maxZipBytes {
					_ = zw.Close()
					return apperr.Validation(fmt.Sprintf("exported draft media exceeds studio.export_max_zip_bytes (%d bytes)", maxZipBytes)).
						WithMsg("errors.studio.export_too_large", map[string]any{"max_bytes": maxZipBytes})
				}

				src, oerr := os.Open(m.srcPath)
				if oerr != nil {
					_ = zw.Close()
					return ioUnavailableErr("Failed to open source media file for NLE draft", oerr)
				}
				hdr := &zip.FileHeader{
					Name:     prefix + m.zipRelPath,
					Method:   zip.Deflate,
					Modified: st.ModTime(),
				}
				if mediautil.IsPrecompressedExt(m.srcPath) {
					hdr.Method = zip.Store
				}
				w, werr := zw.CreateHeader(hdr)
				if werr != nil {
					_ = src.Close()
					_ = zw.Close()
					return ioUnavailableErr("Failed to add media file to NLE draft zip", werr)
				}
				written, cerr := io.Copy(w, io.LimitReader(src, maxZipBytes-totalBytes+1))
				_ = src.Close()
				if cerr != nil {
					_ = zw.Close()
					return ioUnavailableErr("Failed to stream media file into NLE draft zip", cerr)
				}
				totalBytes += written
				if totalBytes > maxZipBytes {
					_ = zw.Close()
					return apperr.Validation(fmt.Sprintf("exported draft media exceeds studio.export_max_zip_bytes (%d bytes)", maxZipBytes)).
						WithMsg("errors.studio.export_too_large", map[string]any{"max_bytes": maxZipBytes})
				}
				fileCount++
			}
		}

		if err := zw.Close(); err != nil {
			return ioUnavailableErr("Failed to finalize NLE draft zip archive", err)
		}
		return nil
	})
	if writeErr != nil {
		var appErr *apperr.Error
		if os.IsExist(writeErr) || ctx.Err() != nil {
			if ctx.Err() != nil {
				return 0, 0, ctx.Err()
			}
		}
		if apperr.IsCode(writeErr, apperr.CodeValidation) || apperr.IsCode(writeErr, apperr.CodeNotFound) || apperr.IsCode(writeErr, apperr.CodeUnauthorized) {
			return 0, 0, writeErr
		}
		_ = appErr
		return 0, 0, ioUnavailableErr("Failed to write NLE draft zip archive", writeErr)
	}
	return fileCount, totalBytes, nil
}

type fcpxmlDoc struct {
	XMLName   xml.Name      `xml:"fcpxml"`
	Version   string        `xml:"version,attr"`
	Resources fcpxResources `xml:"resources"`
	Library   fcpxLibrary   `xml:"library"`
}

type fcpxResources struct {
	Format fcpxFormat  `xml:"format"`
	Assets []fcpxAsset `xml:"asset"`
}

type fcpxFormat struct {
	ID            string `xml:"id,attr"`
	Name          string `xml:"name,attr"`
	FrameDuration string `xml:"frameDuration,attr"`
	Width         int    `xml:"width,attr"`
	Height        int    `xml:"height,attr"`
}

type fcpxAsset struct {
	ID       string       `xml:"id,attr"`
	Name     string       `xml:"name,attr"`
	Start    string       `xml:"start,attr"`
	Duration string       `xml:"duration,attr"`
	HasVideo int          `xml:"hasVideo,attr,omitempty"`
	HasAudio int          `xml:"hasAudio,attr,omitempty"`
	MediaRep fcpxMediaRep `xml:"media-rep"`
}

type fcpxMediaRep struct {
	Kind string `xml:"kind,attr"`
	Src  string `xml:"src,attr"`
}

type fcpxLibrary struct {
	Event fcpxEvent `xml:"event"`
}

type fcpxEvent struct {
	Name    string      `xml:"name,attr"`
	Project fcpxProject `xml:"project"`
}

type fcpxProject struct {
	Name     string       `xml:"name,attr"`
	Sequence fcpxSequence `xml:"sequence"`
}

type fcpxSequence struct {
	Format   string    `xml:"format,attr"`
	Duration string    `xml:"duration,attr"`
	Spine    fcpxSpine `xml:"spine"`
}

type fcpxSpine struct {
	Items []any `xml:",any"`
}

type fcpxAssetClip struct {
	XMLName  xml.Name `xml:"asset-clip"`
	Ref      string   `xml:"ref,attr"`
	Offset   string   `xml:"offset,attr"`
	Name     string   `xml:"name,attr"`
	Start    string   `xml:"start,attr"`
	Duration string   `xml:"duration,attr"`
	Lane     int      `xml:"lane,attr,omitempty"`
}

type fcpxGap struct {
	XMLName  xml.Name `xml:"gap"`
	Name     string   `xml:"name,attr"`
	Offset   string   `xml:"offset,attr"`
	Duration string   `xml:"duration,attr"`
}

type fcpxTransition struct {
	XMLName  xml.Name `xml:"transition"`
	Name     string   `xml:"name,attr"`
	Offset   string   `xml:"offset,attr"`
	Duration string   `xml:"duration,attr"`
}

type fcpxTitle struct {
	XMLName  xml.Name     `xml:"title"`
	Ref      string       `xml:"ref,attr,omitempty"`
	Lane     int          `xml:"lane,attr"`
	Offset   string       `xml:"offset,attr"`
	Name     string       `xml:"name,attr"`
	Duration string       `xml:"duration,attr"`
	Text     fcpxTextElem `xml:"text"`
}

type fcpxTextElem struct {
	TextStyle string `xml:"text-style"`
}

func fcpxTime(ms int64) string {
	return fmt.Sprintf("%d/1000s", max(0, ms))
}

func filePathToFileURI(p string) string {
	slashed := filepath.ToSlash(p)
	if strings.HasPrefix(slashed, "//") {
		rest := strings.TrimPrefix(slashed, "//")
		if host, pathPart, ok := strings.Cut(rest, "/"); ok && host != "" {
			u := url.URL{Scheme: "file", Host: host, Path: "/" + pathPart}
			return u.String()
		}
	}
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

func buildFCPXML(tl domainstudio.EditTimeline, projectName string) ([]byte, error) {
	assetIDs := make(map[string]string)
	var assets []fcpxAsset
	for _, tr := range tl.Tracks {
		isAud := tr.Kind != domainstudio.TrackVideo && tr.Kind != domainstudio.TrackText
		for _, cl := range tr.Clips {
			if cl.FilePath == "" {
				continue
			}
			if _, ok := assetIDs[cl.FilePath]; ok {
				continue
			}
			id := fmt.Sprintf("a%d", len(assets)+1)
			assetIDs[cl.FilePath] = id
			hasVid := 1
			if isAud {
				hasVid = 0
			}
			assets = append(assets, fcpxAsset{
				ID:       id,
				Name:     filepath.Base(cl.FilePath),
				Start:    "0s",
				Duration: fcpxTime(max(cl.SourceOutMs, cl.TimelineSpanMs())),
				HasVideo: hasVid,
				HasAudio: 1,
				MediaRep: fcpxMediaRep{
					Kind: "original-media",
					Src:  filePathToFileURI(cl.FilePath),
				},
			})
		}
	}

	var spineItems []any
	var cursorMs int64
	lane := -1

	for _, tr := range tl.Tracks {
		if tr.Kind == domainstudio.TrackVideo {
			for i, cl := range tr.Clips {
				if cl.TimelineStartMs > cursorMs {
					spineItems = append(spineItems, fcpxGap{
						Name:     "Gap",
						Offset:   fcpxTime(cursorMs),
						Duration: fcpxTime(cl.TimelineStartMs - cursorMs),
					})
				}
				if i > 0 && cl.TransitionIn != domainstudio.TransitionNone && cl.TransitionMs > 0 {
					spineItems = append(spineItems, fcpxTransition{
						Name:     string(cl.TransitionIn),
						Offset:   fcpxTime(cl.TimelineStartMs),
						Duration: fcpxTime(cl.TransitionMs),
					})
				}
				name := cl.Label
				if name == "" {
					name = filepath.Base(cl.FilePath)
				}
				spineItems = append(spineItems, fcpxAssetClip{
					Ref:      assetIDs[cl.FilePath],
					Offset:   fcpxTime(cl.TimelineStartMs),
					Name:     name,
					Start:    fcpxTime(cl.SourceInMs),
					Duration: fcpxTime(cl.TimelineSpanMs()),
				})
				cursorMs = max(cursorMs, cl.TimelineEndMs())
			}
		} else if tr.Kind != domainstudio.TrackText {
			for _, cl := range tr.Clips {
				name := cl.Label
				if name == "" {
					name = filepath.Base(cl.FilePath)
				}
				spineItems = append(spineItems, fcpxAssetClip{
					Ref:      assetIDs[cl.FilePath],
					Lane:     lane,
					Offset:   fcpxTime(cl.TimelineStartMs),
					Name:     name,
					Start:    fcpxTime(cl.SourceInMs),
					Duration: fcpxTime(cl.TimelineSpanMs()),
				})
			}
			lane--
		}
	}

	for _, sub := range tl.Subtitles {
		spineItems = append(spineItems, fcpxTitle{
			Lane:     1,
			Offset:   fcpxTime(sub.StartMs),
			Name:     sub.Speaker,
			Duration: fcpxTime(sub.EndMs - sub.StartMs),
			Text:     fcpxTextElem{TextStyle: sub.Text},
		})
	}

	doc := fcpxmlDoc{
		Version: "1.10",
		Resources: fcpxResources{
			Format: fcpxFormat{
				ID:            "r1",
				Name:          fmt.Sprintf("FFVideoFormat%dp%d", tl.Height, tl.FPS),
				FrameDuration: fmt.Sprintf("100/%ds", tl.FPS*100),
				Width:         tl.Width,
				Height:        tl.Height,
			},
			Assets: assets,
		},
		Library: fcpxLibrary{
			Event: fcpxEvent{
				Name: projectName,
				Project: fcpxProject{
					Name: projectName,
					Sequence: fcpxSequence{
						Format:   "r1",
						Duration: fcpxTime(tl.DurationMs()),
						Spine:    fcpxSpine{Items: spineItems},
					},
				},
			},
		},
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	header := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE fcpxml>\n"
	return append([]byte(header), append(body, '\n')...), nil
}

func buildOTIOJSON(tl domainstudio.EditTimeline, projectName string) ([]byte, error) {
	rate := float64(tl.FPS)
	rational := func(ms int64) map[string]any {
		frames := float64(ms) * rate / 1000.0
		return map[string]any{
			"OTIO_SCHEMA": "RationalTime.1",
			"rate":        rate,
			"value":       frames,
		}
	}
	timeRange := func(startMs, durMs int64) map[string]any {
		return map[string]any{
			"OTIO_SCHEMA": "TimeRange.1",
			"start_time":  rational(startMs),
			"duration":    rational(durMs),
		}
	}

	var otioTracks []map[string]any
	for _, tr := range tl.Tracks {
		otioKind := "Video"
		if tr.Kind != domainstudio.TrackVideo && tr.Kind != domainstudio.TrackText {
			otioKind = "Audio"
		}
		var children []map[string]any
		var cursorMs int64
		for i, cl := range tr.Clips {
			if cl.TimelineStartMs > cursorMs {
				children = append(children, map[string]any{
					"OTIO_SCHEMA":  "Gap.1",
					"name":         "Gap",
					"source_range": timeRange(0, cl.TimelineStartMs-cursorMs),
				})
			}
			if i > 0 && cl.TransitionIn != domainstudio.TransitionNone && cl.TransitionMs > 0 {
				children = append(children, map[string]any{
					"OTIO_SCHEMA":     "Transition.1",
					"name":            string(cl.TransitionIn),
					"transition_type": "SMPTE_Dissolve",
					"in_offset":       rational(cl.TransitionMs / 2),
					"out_offset":      rational(cl.TransitionMs - cl.TransitionMs/2),
				})
			}
			name := cl.Label
			if name == "" {
				name = filepath.Base(cl.FilePath)
			}
			children = append(children, map[string]any{
				"OTIO_SCHEMA": "Clip.1",
				"name":        name,
				"media_reference": map[string]any{
					"OTIO_SCHEMA":     "ExternalReference.1",
					"target_url":      filePathToFileURI(cl.FilePath),
					"available_range": timeRange(0, max(cl.SourceOutMs, cl.TimelineSpanMs())),
				},
				"source_range": timeRange(cl.SourceInMs, cl.SourceOutMs-cl.SourceInMs),
				"metadata": map[string]any{
					"speed":             cl.Speed,
					"volume":            cl.Volume,
					"timeline_start_ms": cl.TimelineStartMs,
				},
			})
			cursorMs = max(cursorMs, cl.TimelineEndMs())
		}
		otioTracks = append(otioTracks, map[string]any{
			"OTIO_SCHEMA": "Track.1",
			"name":        tr.Name,
			"kind":        otioKind,
			"children":    children,
			"metadata": map[string]any{
				"muted":  tr.Muted,
				"solo":   tr.Solo,
				"locked": tr.Locked,
				"volume": tr.Volume,
			},
		})
	}

	if len(tl.Subtitles) > 0 {
		var subMarkers []map[string]any
		for _, sub := range tl.Subtitles {
			subMarkers = append(subMarkers, map[string]any{
				"OTIO_SCHEMA":  "Marker.1",
				"name":         sub.Text,
				"color":        "CYAN",
				"marked_range": timeRange(sub.StartMs, sub.EndMs-sub.StartMs),
				"metadata": map[string]any{
					"speaker":      sub.Speaker,
					"style_preset": sub.StylePreset,
				},
			})
		}
		otioTracks = append(otioTracks, map[string]any{
			"OTIO_SCHEMA": "Track.1",
			"name":        "Subtitles",
			"kind":        "Video",
			"children":    []map[string]any{},
			"markers":     subMarkers,
		})
	}

	doc := map[string]any{
		"OTIO_SCHEMA": "Timeline.1",
		"name":        projectName,
		"tracks": map[string]any{
			"OTIO_SCHEMA": "Stack.1",
			"name":        "tracks",
			"children":    otioTracks,
		},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
