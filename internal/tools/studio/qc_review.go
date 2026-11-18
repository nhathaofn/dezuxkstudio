package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/runner"
)

const (
	// qcDefaultPassScore is the baseline score on a 0..10 scale when technical probe passes without Vision critique.
	qcDefaultPassScore = 9.0
	// qcFailScoreCeiling caps OverallScore when any technical or visual error issue is present.
	qcFailScoreCeiling = 4.0
	// qcWarnScoreCeiling caps OverallScore when warning issues are present without hard errors.
	qcWarnScoreCeiling = 7.5
)

var qcResponseSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["verdict", "overall_score", "summary", "issues"],
	"properties": {
		"verdict": {"type": "string", "enum": ["pass", "warn", "fail"]},
		"overall_score": {"type": "number", "minimum": 0, "maximum": 10},
		"summary": {"type": "string"},
		"issues": {
			"type": "array",
			"items": {
				"type": "object",
				"additionalProperties": false,
				"required": ["severity", "category", "message"],
				"properties": {
					"severity": {"type": "string", "enum": ["info", "warn", "error"]},
					"category": {"type": "string"},
					"message": {"type": "string"}
				}
			}
		}
	}
}`)

// NewQCReviewNode returns tool.qc_review combining technical FFprobe checks, FFmpeg sample frames, and multimodal Vision critique.
func NewQCReviewNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	sampleDef := opts.StudioConfig.QCSampleFrameDefault
	sampleMax := opts.StudioConfig.QCSampleFrameMax
	maxRefs := opts.StudioConfig.QCMaxReferenceImages

	spec := engine.Spec{
		Type:        NodeTypeQCReview,
		Provider:    studioProviderID,
		Label:       "Technical & Visual QC Review",
		Description: "Inspects generated images or videos using FFprobe metadata, silence/stream validation, FFmpeg frame sampling, and multimodal Vision LLM evaluation against reference assets.",
		Inputs: []engine.Port{
			{Name: "media_path", Label: "Target Media", Type: engine.PortMedia},
			{Name: "reference_paths", Label: "Reference Assets", Type: engine.PortImage, Multiple: true, Optional: true},
		},
		Outputs: []engine.Port{
			{Name: "report", Label: "QC Report", Type: engine.PortText},
		},
		Params: []engine.Param{
			{Name: "target_kind", Label: "Target Kind", Kind: engine.ParamText},
			{Name: "target_id", Label: "Target ID", Kind: engine.ParamText},
			{Name: "prompt_expected", Label: "Expected Prompt", Kind: engine.ParamText},
			{Name: "vision_model_ref", Label: "Vision LLM Model", Kind: engine.ParamText},
			{Name: "sample_count", Label: "Sample Frames", Kind: engine.ParamInteger, Min: 1, Max: sampleMax, Default: strconv.Itoa(sampleDef)},
			{Name: "skip_vision", Label: "Technical Probe Only", Kind: engine.ParamToggle, Default: "false"},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.QCReviewInput) (contracts.QCReviewOutput, error) {
		if in.SampleCount < 0 || in.SampleCount > sampleMax {
			return contracts.QCReviewOutput{}, apperr.Validation(fmt.Sprintf("sample_count must be between 1 and %d", sampleMax)).
				WithMsg("errors.studio.invalid_sample_count", map[string]any{"min": 1, "max": sampleMax})
		}
		mediaPath, err := resolveMediaInput(ctx, opts, in.MediaPath.LocalURL, inputKindImage|inputKindVideo)
		if err != nil {
			return contracts.QCReviewOutput{}, err
		}
		if mediaPath == "" {
			return contracts.QCReviewOutput{}, apperr.Validation("media_path is required").
				WithMsg("errors.studio.media_required", nil)
		}

		var refPaths []string
		for _, rp := range in.ReferencePaths {
			resolved, rerr := resolveMediaInput(ctx, opts, rp.LocalURL, inputKindImage)
			if rerr != nil {
				return contracts.QCReviewOutput{}, rerr
			}
			if resolved != "" {
				refPaths = append(refPaths, resolved)
			}
		}
		if len(refPaths) > maxRefs {
			refPaths = refPaths[:maxRefs]
		}

		isVideo := isVideoExtension(mediaPath)
		out := contracts.QCReviewOutput{
			Verdict:      "pass",
			OverallScore: qcDefaultPassScore,
			Issues:       []contracts.QCReviewIssue{},
			SampleFrames: []string{},
		}

		progress.Report(ctx, progress.Update{Percent: 10, Message: "Running technical QC probe…", Key: "studio.progress.qc_probe"})

		var info contracts.ProbeMediaOutput
		var probed bool
		if ffprobeExe, berr := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFprobe); berr == nil {
			probeCtx, cancelProbe := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
			stopProbe := progress.KeepAlive(ctx)
			pOut, perr := probeWithFFprobe(probeCtx, ffprobeExe, mediaPath)
			stopProbe()
			cancelProbe()
			if perr != nil {
				if ctx.Err() != nil {
					return contracts.QCReviewOutput{}, ctx.Err()
				}
				out.Issues = append(out.Issues, contracts.QCReviewIssue{
					Severity: "error", Category: "technical",
					Message: "Media file could not be decoded by technical probe",
					MsgKey:  "studio.qc.issue.probe_failed",
				})
			} else {
				probed = true
				info = pOut
				out.FFprobeJSON = pOut.ProbeJSON
				if pOut.Width == 0 || pOut.Height == 0 {
					out.Issues = append(out.Issues, contracts.QCReviewIssue{
						Severity: "error", Category: "technical",
						Message: "Media contains no valid visual stream dimensions",
						MsgKey:  "studio.qc.issue.no_visual_stream",
					})
				}
				if isVideo && pOut.DurationSec <= 0 {
					out.Issues = append(out.Issues, contracts.QCReviewIssue{
						Severity: "error", Category: "technical",
						Message: "Video duration is zero or unreadable",
						MsgKey:  "studio.qc.issue.zero_duration",
					})
				}
				if isVideo && pOut.HasAudio && pOut.DurationSec > 0 {
					if ffmpegExe, ferr := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg); ferr == nil {
						silCtx, cancelSil := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
						stopSil := progress.KeepAlive(ctx)
						silentRatio := detectSilenceRatio(silCtx, ffmpegExe, mediaPath, pOut.DurationSec, opts.StudioConfig)
						stopSil()
						cancelSil()
						if silentRatio > opts.StudioConfig.QCSilenceWarnRatio {
							out.Issues = append(out.Issues, contracts.QCReviewIssue{
								Severity:  "warn",
								Category:  "audio",
								Message:   fmt.Sprintf("Audio track is %.0f%% silent", silentRatio*100),
								MsgKey:    "studio.qc.issue.audio_silent",
								MsgParams: map[string]string{"percent": strconv.Itoa(int(math.Round(silentRatio * 100)))},
							})
						}
					}
				}
			}
		} else if !isVideo {
			imgW, imgH, imgFmt, ierr := probeImageConfig(mediaPath)
			if ierr != nil || imgW <= 0 || imgH <= 0 {
				out.Issues = append(out.Issues, contracts.QCReviewIssue{
					Severity: "error", Category: "technical",
					Message: "Media file could not be decoded by technical probe",
					MsgKey:  "studio.qc.issue.probe_failed",
				})
			} else {
				probed = true
				info = contracts.ProbeMediaOutput{Width: imgW, Height: imgH, FormatName: imgFmt}
				out.FFprobeJSON = marshalProbeSummary(info)
			}
		} else if in.SkipVision {
			return contracts.QCReviewOutput{}, berr
		} else {
			out.Issues = append(out.Issues, contracts.QCReviewIssue{
				Severity: "warn", Category: "technical",
				Message: "Technical stream probe unavailable; ffprobe is not installed",
				MsgKey:  "studio.qc.issue.probe_unavailable",
			})
		}

		if in.SkipVision {
			finalizeTechnicalQC(&out)
			out.Report = formatQCReport(out)
			progress.Report(ctx, progress.Update{Percent: 100, Message: "QC probe complete", Key: "studio.progress.done"})
			return out, nil
		}

		var visionImages []string
		var tmpDir string
		if !isVideo {
			visionImages = append(visionImages, mediaPath)
			out.SampleFrames = []string{"frame_1"}
		} else {
			progress.Report(ctx, progress.Update{Percent: 35, Message: "Sampling frames for Vision QC…", Key: "studio.progress.qc_sample"})
			var sampleErr error
			var labels []string
			visionImages, labels, tmpDir, sampleErr = extractQCSampleFrames(ctx, opts, mediaPath, info.DurationSec, probed, in.SampleCount)
			if tmpDir != "" {
				defer func() { _ = os.RemoveAll(tmpDir) }()
			}
			if labels != nil {
				out.SampleFrames = labels
			}
			if sampleErr != nil {
				if ctx.Err() != nil {
					return contracts.QCReviewOutput{}, ctx.Err()
				}
				out.Issues = append(out.Issues, contracts.QCReviewIssue{
					Severity: "warn", Category: "technical",
					Message: "Could not sample video frames for vision review",
					MsgKey:  "studio.qc.issue.frame_sample_failed",
				})
			}
		}
		visionImages = append(visionImages, refPaths...)

		if opts.TextGen == nil || len(visionImages) == 0 {
			out.Issues = append(out.Issues, contracts.QCReviewIssue{
				Severity: "info", Category: "vision",
				Message: "Vision LLM critique skipped; technical validation only",
				MsgKey:  "studio.qc.issue.vision_skipped",
			})
			finalizeTechnicalQC(&out)
			out.Report = formatQCReport(out)
			progress.Report(ctx, progress.Update{Percent: 100, Message: "Technical QC complete", Key: "studio.progress.done"})
			return out, nil
		}

		progress.Report(ctx, progress.Update{Percent: 65, Message: "Running multimodal Vision QC…", Key: "studio.progress.qc_vision"})
		prompt := fmt.Sprintf("Review the target %s frame(s) against prompt expectation %q.",
			valueOr(in.TargetKind, "shot"), valueOr(in.PromptExpected, "cinematic visual fidelity & continuity"))
		if len(refPaths) > 0 {
			prompt += fmt.Sprintf(" The final %d image(s) are canonical reference sheets for continuity checking.", len(refPaths))
		}
		res, err := opts.TextGen.GenerateText(ctx, domainmedia.TextRequest{
			ModelRef:       in.VisionModelRef,
			SystemPrompt:   "You are a film post-production Visual QC inspector. Check composition, anatomy/hands, lighting continuity, artifacts, and prompt adherence. Return strict JSON.",
			UserPrompt:     prompt,
			ImageInputs:    visionImages,
			JSONSchemaName: "qc_review",
			JSONSchema:     qcResponseSchema,
		})
		if err != nil {
			if ctx.Err() != nil {
				return contracts.QCReviewOutput{}, ctx.Err()
			}
			out.Issues = append(out.Issues, contracts.QCReviewIssue{
				Severity: "warn", Category: "vision",
				Message: "Vision QC critique could not be completed",
				MsgKey:  "studio.qc.issue.vision_unavailable",
			})
			finalizeTechnicalQC(&out)
			out.Report = formatQCReport(out)
			progress.Report(ctx, progress.Update{Percent: 100, Message: "QC completed with warnings", Key: "studio.progress.done"})
			return out, nil
		}

		var parsed struct {
			Verdict      string                    `json:"verdict"`
			OverallScore float64                   `json:"overall_score"`
			Summary      string                    `json:"summary"`
			Issues       []contracts.QCReviewIssue `json:"issues"`
		}
		payload := res.ParsedJSON
		if len(payload) == 0 {
			payload = []byte(res.Text)
		}
		if jErr := json.Unmarshal(payload, &parsed); jErr != nil {
			out.Issues = append(out.Issues, contracts.QCReviewIssue{
				Severity: "warn", Category: "vision",
				Message: "Vision QC response was not valid JSON",
				MsgKey:  "studio.qc.issue.vision_invalid_json",
			})
		} else {
			out.VisionSummary = strings.TrimSpace(parsed.Summary)
			if !math.IsNaN(parsed.OverallScore) && !math.IsInf(parsed.OverallScore, 0) {
				out.OverallScore = math.Max(0, math.Min(10, parsed.OverallScore))
			}
			for _, iss := range parsed.Issues {
				sev := strings.ToLower(strings.TrimSpace(iss.Severity))
				if sev != "info" && sev != "warn" && sev != "error" {
					sev = "info"
				}
				out.Issues = append(out.Issues, contracts.QCReviewIssue{
					Severity: sev,
					Category: valueOr(iss.Category, "visual"),
					Message:  strings.TrimSpace(iss.Message),
				})
			}
			if parsed.Verdict == "fail" || parsed.Verdict == "warn" || parsed.Verdict == "pass" {
				out.Verdict = parsed.Verdict
			}
		}
		finalizeTechnicalQC(&out)
		out.Report = formatQCReport(out)
		progress.Report(ctx, progress.Update{Percent: 100, Message: "QC review complete", Key: "studio.progress.done"})
		return out, nil
	})
}

func probeImageConfig(path string) (int, int, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, "", err
	}
	defer func() { _ = f.Close() }()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, "", err
	}
	return cfg.Width, cfg.Height, format, nil
}

func extractQCSampleFrames(ctx context.Context, opts Options, mediaPath string, dur float64, probed bool, reqCount int) (framePaths, labels []string, tmpDir string, err error) {
	ffmpegExe, err := resolveBinary(ctx, opts.Binaries, domainbinaries.IDFFmpeg)
	if err != nil {
		return nil, nil, "", err
	}
	anchor := resolveStagingDir(opts)
	if anchor == "" {
		anchor = resolveMediaDir(opts)
	}
	if anchor == "" {
		anchor = resolveProjectsDir(opts)
	}
	if anchor == "" || !filepath.IsAbs(anchor) {
		anchor = filepath.Dir(mediaPath)
	}
	sampleRoot := filepath.Join(anchor, "qc_samples")
	if err := checkStudioFile(ctx, filepath.Join(sampleRoot, ".write-check"), true); err != nil {
		return nil, nil, "", err
	}
	if err := os.MkdirAll(sampleRoot, 0o750); err != nil {
		return nil, nil, "", err
	}
	tmpDir, err = os.MkdirTemp(sampleRoot, "run-*")
	if err != nil {
		return nil, nil, "", err
	}
	count := reqCount
	if count <= 0 {
		count = opts.StudioConfig.QCSampleFrameDefault
	}
	if count > opts.StudioConfig.QCSampleFrameMax {
		count = opts.StudioConfig.QCSampleFrameMax
	}
	endPad := opts.StudioConfig.ExtractFrameEndPadSec
	var stamps []float64
	if !probed || dur <= endPad*2 {
		stamps = []float64{0}
	} else {
		step := dur / float64(count+1)
		for i := 1; i <= count; i++ {
			stamps = append(stamps, math.Min(dur-endPad, step*float64(i)))
		}
	}
	ffCtx, cancel := context.WithTimeout(ctx, opts.StudioConfig.FFmpegTimeout)
	defer cancel()
	labels = []string{}
	for i, ts := range stamps {
		if ctx.Err() != nil {
			return nil, labels, tmpDir, ctx.Err()
		}
		p := filepath.Join(tmpDir, fmt.Sprintf("sample_%d.png", i+1))
		stopRun := progress.KeepAlive(ctx)
		res, runErr := runner.Run(ffCtx, ffmpegExe, "-y", "-ss", fmt.Sprintf("%.3f", ts), "-i", mediaPath, "-frames:v", "1", p)
		stopRun()
		if runErr == nil && res != nil && res.ExitCode == 0 {
			framePaths = append(framePaths, p)
			labels = append(labels, fmt.Sprintf("t=%.2fs", ts))
		}
	}
	if len(framePaths) == 0 {
		return nil, labels, tmpDir, fmt.Errorf("no sample frames extracted")
	}
	return framePaths, labels, tmpDir, nil
}

func finalizeTechnicalQC(out *contracts.QCReviewOutput) {
	hasErr, hasWarn := false, false
	for _, iss := range out.Issues {
		switch iss.Severity {
		case "error":
			hasErr = true
		case "warn":
			hasWarn = true
		}
	}
	if hasErr {
		out.Verdict = "fail"
		if out.OverallScore > qcFailScoreCeiling {
			out.OverallScore = qcFailScoreCeiling
		}
	} else if hasWarn && out.Verdict == "pass" {
		out.Verdict = "warn"
		if out.OverallScore > qcWarnScoreCeiling {
			out.OverallScore = qcWarnScoreCeiling
		}
	}
}

func formatQCReport(out contracts.QCReviewOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "QC Verdict: %s (%.1f/10)", strings.ToUpper(out.Verdict), out.OverallScore)
	if out.VisionSummary != "" {
		fmt.Fprintf(&b, " — %s", out.VisionSummary)
	}
	for _, iss := range out.Issues {
		fmt.Fprintf(&b, "\n[%s/%s] %s", iss.Severity, iss.Category, iss.Message)
	}
	return b.String()
}

func isVideoExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mov", ".webm", ".mkv", ".avi", ".m4v", ".mpeg", ".mpg", ".ts":
		return true
	default:
		return false
	}
}

func valueOr(val, def string) string {
	if strings.TrimSpace(val) != "" {
		return val
	}
	return def
}
