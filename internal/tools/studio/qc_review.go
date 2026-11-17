package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainmedia "app/internal/domain/mediagen"
	"app/internal/engine"
	"app/internal/runner"
)

type qcReviewNode struct {
	opts Options
}

// NewQCReviewNode returns tool.qc_review combining technical FFprobe checks, FFmpeg sample frames, and multimodal Vision critique.
func NewQCReviewNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	return &qcReviewNode{opts: opts}
}

func (n *qcReviewNode) Spec() engine.Spec {
	return engine.Spec{
		Type:        NodeTypeQCReview,
		Provider:    "studio",
		Label:       "Technical & Visual QC Review",
		Description: "Inspects generated images or videos using FFprobe metadata, silence/stream validation, FFmpeg frame sampling, and multimodal Vision LLM evaluation against reference assets.",
		Inputs: []engine.Port{
			{Name: "media_path", Label: "Target Media", Type: engine.PortMedia},
		},
		Outputs: []engine.Port{
			{Name: "report", Label: "QC Report", Type: engine.PortText},
		},
		Params: []engine.Param{
			{Name: "vision_model_ref", Label: "Vision LLM Model", Kind: engine.ParamText},
			{Name: "sample_count", Label: "Sample Frames", Kind: engine.ParamInteger, Min: 1, Max: 6, Default: "3"},
			{Name: "skip_vision", Label: "Technical Probe Only", Kind: engine.ParamToggle, Default: "false"},
		},
	}
}

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

func (n *qcReviewNode) Run(ctx context.Context, raw json.RawMessage) (any, error) {
	var in contracts.QCReviewInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, apperr.Validation("invalid qc_review input").WithDetails(map[string]any{"cause": err.Error()})
	}
	mediaPath, err := resolveInputFile(ctx, n.opts, in.MediaPath)
	if err != nil {
		return nil, err
	}
	if mediaPath == "" {
		return nil, apperr.Validation("media_path is required")
	}

	var refPaths []string
	for _, rp := range in.ReferencePaths {
		resolved, err := resolveInputFile(ctx, n.opts, rp)
		if err != nil {
			return nil, err
		}
		if resolved != "" {
			refPaths = append(refPaths, resolved)
		}
	}

	isVideo := isVideoExtension(mediaPath)
	out := contracts.QCReviewOutput{
		Verdict:      "pass",
		OverallScore: 9.0,
		Issues:       []contracts.QCReviewIssue{},
	}

	var info contracts.ProbeMediaOutput
	var probed bool
	if ffprobeExe, err := resolveBinary(ctx, n.opts.Binaries, domainbinaries.IDFFprobe); err == nil {
		probeCtx, cancelProbe := context.WithTimeout(ctx, n.opts.StudioConfig.FFprobeTimeout)
		defer cancelProbe()
		if pOut, perr := probeWithFFprobe(probeCtx, ffprobeExe, mediaPath); perr == nil {
			probed = true
			info = pOut
			out.FFprobeJSON = pOut.ProbeJSON
			if pOut.Width == 0 || pOut.Height == 0 {
				out.Issues = append(out.Issues, contracts.QCReviewIssue{
					Severity: "error", Category: "technical", Message: "Media contains no valid visual stream dimensions",
				})
			}
			if isVideo && pOut.DurationSec <= 0 {
				out.Issues = append(out.Issues, contracts.QCReviewIssue{
					Severity: "error", Category: "technical", Message: "Video duration is zero or unreadable",
				})
			}
			if isVideo && pOut.HasAudio && pOut.DurationSec > 0 {
				if ffmpegExe, ferr := resolveBinary(ctx, n.opts.Binaries, domainbinaries.IDFFmpeg); ferr == nil {
					silentRatio := detectSilenceRatio(probeCtx, ffmpegExe, mediaPath, pOut.DurationSec)
					if silentRatio > 0.90 {
						out.Issues = append(out.Issues, contracts.QCReviewIssue{
							Severity: "warn", Category: "audio", Message: fmt.Sprintf("Audio track is %.0f%% silent", silentRatio*100),
						})
					}
				}
			}
		}
	}

	if in.SkipVision {
		finalizeTechnicalQC(&out)
		return out, nil
	}

	// Collect visual inputs (directly if image, or temporary sampled frames if video)
	var visionImages []string
	var tmpDir string
	if !isVideo {
		visionImages = append(visionImages, mediaPath)
		out.SampleFrames = []string{mediaPath}
	} else {
		var sampleErr error
		visionImages, out.SampleFrames, tmpDir, sampleErr = n.extractQCSampleFrames(ctx, mediaPath, info.DurationSec, probed, in.SampleCount)
		if tmpDir != "" {
			defer func() { _ = os.RemoveAll(tmpDir) }()
		}
		if sampleErr != nil {
			out.Issues = append(out.Issues, contracts.QCReviewIssue{
				Severity: "warn", Category: "technical", Message: "Could not sample video frames for vision review: " + sampleErr.Error(),
			})
		}
	}
	if len(refPaths) > 2 {
		refPaths = refPaths[:2]
	}
	visionImages = append(visionImages, refPaths...)

	if n.opts.TextGen == nil || len(visionImages) == 0 {
		out.Issues = append(out.Issues, contracts.QCReviewIssue{
			Severity: "info", Category: "vision", Message: "Vision LLM critique skipped; technical validation only",
		})
		finalizeTechnicalQC(&out)
		return out, nil
	}

	prompt := fmt.Sprintf("Review the target %s frame(s) against prompt expectation %q.",
		valueOr(in.TargetKind, "shot"), valueOr(in.PromptExpected, "cinematic visual fidelity & continuity"))
	if len(refPaths) > 0 {
		prompt += fmt.Sprintf(" The final %d image(s) are canonical reference sheets for continuity checking.", len(refPaths))
	}
	res, err := n.opts.TextGen.GenerateText(ctx, domainmedia.TextRequest{
		ModelRef:       in.VisionModelRef,
		SystemPrompt:   "You are a film post-production Visual QC inspector. Check composition, anatomy/hands, lighting continuity, artifacts, and prompt adherence. Return strict JSON.",
		UserPrompt:     prompt,
		ImageInputs:    visionImages,
		JSONSchemaName: "qc_review",
		JSONSchema:     qcResponseSchema,
	})
	if err != nil {
		out.Issues = append(out.Issues, contracts.QCReviewIssue{
			Severity: "warn", Category: "vision", Message: "Vision QC unavailable: " + err.Error(),
		})
		finalizeTechnicalQC(&out)
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
	if jErr := json.Unmarshal(payload, &parsed); jErr == nil {
		out.VisionSummary = parsed.Summary
		out.OverallScore = parsed.OverallScore
		out.Issues = append(out.Issues, parsed.Issues...)
		if parsed.Verdict == "fail" || parsed.Verdict == "warn" || parsed.Verdict == "pass" {
			out.Verdict = parsed.Verdict
		}
	}
	finalizeTechnicalQC(&out)
	return out, nil
}

func (n *qcReviewNode) extractQCSampleFrames(ctx context.Context, mediaPath string, dur float64, probed bool, reqCount int) (framePaths, labels []string, tmpDir string, err error) {
	ffmpegExe, err := resolveBinary(ctx, n.opts.Binaries, domainbinaries.IDFFmpeg)
	if err != nil {
		return nil, nil, "", err
	}
	tmpDir, err = os.MkdirTemp("", "dezuxk-qc-*")
	if err != nil {
		return nil, nil, "", err
	}
	count := clampInt(reqCount, 3, 1, 6)
	var stamps []float64
	if !probed || dur <= 0.1 {
		stamps = []float64{0}
	} else {
		step := dur / float64(count+1)
		for i := 1; i <= count; i++ {
			stamps = append(stamps, math.Min(dur-0.05, step*float64(i)))
		}
	}
	ffCtx, cancel := context.WithTimeout(ctx, n.opts.StudioConfig.FFmpegTimeout)
	defer cancel()
	for i, ts := range stamps {
		p := filepath.Join(tmpDir, fmt.Sprintf("sample_%d.png", i+1))
		res, runErr := runner.Run(ffCtx, ffmpegExe, "-y", "-ss", fmt.Sprintf("%.3f", ts), "-i", mediaPath, "-frames:v", "1", "-q:v", "2", p)
		if runErr == nil && res.ExitCode == 0 {
			framePaths = append(framePaths, p)
			labels = append(labels, fmt.Sprintf("t=%.2fs", ts))
		}
	}
	if len(framePaths) == 0 {
		return nil, nil, tmpDir, fmt.Errorf("no sample frames extracted")
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
		if out.OverallScore > 4.0 {
			out.OverallScore = 4.0
		}
	} else if hasWarn && out.Verdict == "pass" {
		out.Verdict = "warn"
		if out.OverallScore > 7.5 {
			out.OverallScore = 7.5
		}
	}
}

func isVideoExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mov", ".webm", ".mkv", ".avi", ".m4v":
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
