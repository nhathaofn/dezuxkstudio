package mediagen

import (
	"context"
	"slices"
	"strings"
	"time"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	"app/internal/progress"
)

const (
	// openAIPollActiveProgress and defaultPollActiveProgress represent baseline UI progress for single-step PollVideoJobOnce status probes.
	openAIPollActiveProgress  = 40
	defaultPollActiveProgress = 45
)

type videoInputs struct {
	start *localImageData
	end   *localImageData
	refs  []localImageData
}

type pollProbeOutcome struct {
	done        bool
	failed      bool
	status      string
	progress    int
	data        []byte
	filePath    string
	mimeType    string
	downloadURL string
	errText     string
}

func (s *Service) loadVideoInputs(norm domainmedia.VideoRequest) (videoInputs, func(), error) {
	releaseLeases := s.leaseInputs(append([]string{norm.StartImage, norm.EndImage}, norm.ReferenceImages...)...)
	var inputs videoInputs
	if norm.StartImage != "" {
		img, err := s.readReferenceImage(norm.StartImage)
		if err != nil {
			releaseLeases()
			return videoInputs{}, func() {}, err
		}
		inputs.start = &img
	}
	if norm.EndImage != "" {
		img, err := s.readReferenceImage(norm.EndImage)
		if err != nil {
			releaseLeases()
			return videoInputs{}, func() {}, err
		}
		inputs.end = &img
	}
	refs, err := s.readReferenceImages(norm.ReferenceImages)
	if err != nil {
		releaseLeases()
		return videoInputs{}, func() {}, err
	}
	inputs.refs = refs
	return inputs, releaseLeases, nil
}

func applyVideoModelDefaults(norm *domainmedia.VideoRequest, facts domainprovider.ModelFacts, defaultDurSec int, explicitDur, explicitRes bool) {
	if defaultDurSec <= 0 {
		defaultDurSec = domainmedia.DefaultVideoDurationSec
	}
	if !explicitDur && len(facts.Durations) > 0 {
		norm.DurationSec = pickNearestInt(facts.Durations, defaultDurSec)
	}
	if !explicitRes && len(facts.Resolutions) > 0 {
		if !slices.Contains(facts.Resolutions, norm.Resolution) {
			norm.Resolution = strings.ToLower(strings.TrimSpace(facts.Resolutions[0]))
		}
	}
}

// SubmitVideoJob submits a remote video task without waiting for completion,
// returning a non-secret VideoSubmitToken that PollVideoJobOnce can resume across restarts.
func (s *Service) SubmitVideoJob(ctx context.Context, req domainmedia.VideoRequest) (domainmedia.VideoSubmitToken, error) {
	explicitDuration := req.DurationSec > 0
	explicitResolution := strings.TrimSpace(req.Resolution) != ""

	norm, err := domainmedia.NormalizeVideoRequestWithLimits(req, s.maxVideoDurationSec, s.defaultVideoDurationSec)
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	inputs, releaseLeases, err := s.loadVideoInputs(norm)
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	defer releaseLeases()

	target, limits, err := s.resolveTarget(ctx, domainprovider.PurposeVideo, norm.ModelRef)
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	applyVideoModelDefaults(&norm, target.modelFacts.Facts, s.defaultVideoDurationSec, explicitDuration, explicitResolution)

	limCtx, release, err := s.acquireLimit(ctx, target, domainprovider.CapabilityVideo, limits)
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	defer release()

	callCtx, cancel := s.requestTimeoutCtx(limCtx, false)
	defer cancel()

	return s.submitVideoTarget(callCtx, target, norm, inputs)
}

// PollVideoJobOnce checks a previously submitted video job exactly once using fresh
// provider credentials resolved from token.ModelRef, downloading and saving output when complete.
func (s *Service) PollVideoJobOnce(ctx context.Context, token domainmedia.VideoSubmitToken) (domainmedia.VideoPollStatus, error) {
	if strings.TrimSpace(token.ProviderJobID) == "" || strings.TrimSpace(token.Protocol) == "" {
		return domainmedia.VideoPollStatus{Failed: true, ErrorText: "invalid video submit token"}, apperr.Validation("Invalid video submit token: provider_job_id and protocol are required")
	}

	target, _, err := s.resolveTarget(ctx, domainprovider.PurposeVideo, token.ModelRef)
	if err != nil {
		return domainmedia.VideoPollStatus{}, err
	}
	if token.Protocol != "" {
		target.protocol = token.Protocol
	}

	scopedCtx := withRateLimitScope(ctx, target.ownerID, domainprovider.CapabilityVideo)
	callCtx, cancel := s.requestTimeoutCtx(scopedCtx, true)
	defer cancel()

	out, err := s.pollVideoTargetOnce(callCtx, target, token)
	if err != nil {
		return domainmedia.VideoPollStatus{
			Failed:    out.failed,
			Status:    out.status,
			Progress:  out.progress,
			ErrorText: out.errText,
		}, err
	}
	if !out.done {
		return domainmedia.VideoPollStatus{
			Done:     false,
			Status:   out.status,
			Progress: out.progress,
		}, nil
	}

	return s.finalizeVideoPoll(ctx, target, token, out)
}

func (s *Service) finalizeVideoPoll(ctx context.Context, target targetSpec, token domainmedia.VideoSubmitToken, out pollProbeOutcome) (domainmedia.VideoPollStatus, error) {
	res, err := s.saveMediaOutput(ctx, domainmedia.KindVideo, token.OutputDir, "vid", ".mp4", mediaOutput{
		data:     out.data,
		filePath: out.filePath,
		mimeType: out.mimeType,
	}, target)
	if err != nil {
		return domainmedia.VideoPollStatus{}, err
	}
	res.Duration = float64(token.DurationSec)
	units := float64(token.DurationSec)
	if units <= 0 {
		units = 1
	}
	s.emitUsage(ctx, domainmedia.UsageEvent{
		ProviderID:  target.ownerID,
		ModelID:     target.modelID,
		Capability:  string(domainprovider.CapabilityVideo),
		Operation:   "generate_video",
		OutputUnits: units,
		Unit:        "second",
	})
	return domainmedia.VideoPollStatus{
		Done:        true,
		Status:      out.status,
		Progress:    100,
		DownloadURL: domainmedia.SanitizeRemoteURL(out.downloadURL),
		Result:      res,
	}, nil
}

// GenerateVideo submits, polls, and downloads a video through whichever built-in or custom
// provider ModelRef resolves to (or the user's configured default when ModelRef is empty).
func (s *Service) GenerateVideo(ctx context.Context, req domainmedia.VideoRequest) (domainmedia.Result, error) {
	explicitDuration := req.DurationSec > 0
	explicitResolution := strings.TrimSpace(req.Resolution) != ""

	norm, err := domainmedia.NormalizeVideoRequestWithLimits(req, s.maxVideoDurationSec, s.defaultVideoDurationSec)
	if err != nil {
		return domainmedia.Result{}, err
	}
	inputs, releaseLeases, err := s.loadVideoInputs(norm)
	if err != nil {
		return domainmedia.Result{}, err
	}
	defer releaseLeases()

	target, limits, err := s.resolveTarget(ctx, domainprovider.PurposeVideo, norm.ModelRef)
	if err != nil {
		return domainmedia.Result{}, err
	}
	applyVideoModelDefaults(&norm, target.modelFacts.Facts, s.defaultVideoDurationSec, explicitDuration, explicitResolution)

	limCtx, release, err := s.acquireLimit(ctx, target, domainprovider.CapabilityVideo, limits)
	if err != nil {
		return domainmedia.Result{}, err
	}
	defer release()

	callCtx, cleanup := s.prepareJobContext(limCtx, true)
	defer cleanup()

	progress.Report(callCtx, progress.Update{
		Percent: 10,
		Message: "Submitting video task…",
		Key:     "mediagen.progress.submitting_video",
	})

	token, err := s.submitVideoTarget(callCtx, target, norm, inputs)
	if err != nil {
		return domainmedia.Result{}, err
	}

	var doneOut pollProbeOutcome
	err = s.pollUntil(callCtx, "mediagen.progress.polling_video", videoPollMessage(target.protocol), map[string]string{"provider": target.providerName}, func() (bool, error) {
		out, pollErr := s.pollVideoTargetOnce(callCtx, target, token)
		if pollErr != nil {
			if out.failed {
				return true, pollErr
			}
			return false, pollErr
		}
		if !out.done {
			return false, nil
		}
		doneOut = out
		return true, nil
	})
	if err != nil {
		return domainmedia.Result{}, err
	}

	res, err := s.saveOutput(ctx, domainmedia.KindVideo, norm.OutputDir, "vid", doneOut.mimeType, ".mp4", doneOut.data, target)
	if err != nil {
		return domainmedia.Result{}, err
	}
	res.Duration = float64(norm.DurationSec)
	units := float64(norm.DurationSec)
	if units <= 0 {
		units = 1
	}
	s.emitUsage(ctx, domainmedia.UsageEvent{
		ProviderID:  target.ownerID,
		ModelID:     target.modelID,
		Capability:  string(domainprovider.CapabilityVideo),
		Operation:   "generate_video",
		OutputUnits: units,
		Unit:        "second",
	})
	progress.Report(callCtx, progress.Update{
		Percent: 100,
		Message: "Video ready",
		Key:     "mediagen.progress.done",
	})
	return res, nil
}

func videoPollMessage(protocol string) string {
	switch protocol {
	case "openai-video":
		return "Generating OpenAI video…"
	case "gemini-aistudio-video", "gemini-vertex-video":
		return "Generating Gemini/Veo video…"
	case "ark-video":
		return "Generating Ark video…"
	case "dashscope-video":
		return "Generating DashScope video…"
	case "minimax-video":
		return "Generating MiniMax video…"
	case "kling-video":
		return "Generating Kling video…"
	case "vidu-video":
		return "Generating Vidu video…"
	case "grok-video":
		return "Generating Grok video…"
	case "agnes-video":
		return "Generating Agnes video…"
	default:
		return "Generating video…"
	}
}

func (s *Service) submitVideoTarget(ctx context.Context, t targetSpec, req domainmedia.VideoRequest, in videoInputs) (domainmedia.VideoSubmitToken, error) {
	var tok domainmedia.VideoSubmitToken
	var err error
	switch t.protocol {
	case "openai-video":
		tok, err = s.submitOpenAIVideo(ctx, t, req, in)
	case "gemini-aistudio-video", "gemini-vertex-video":
		tok, err = s.submitGeminiVideo(ctx, t, req, in)
	case "ark-video":
		tok, err = s.submitArkVideo(ctx, t, req, in)
	case "dashscope-video":
		tok, err = s.submitDashScopeVideo(ctx, t, req, in)
	case "minimax-video":
		tok, err = s.submitMiniMaxVideo(ctx, t, req, in)
	case "kling-video":
		tok, err = s.submitKlingVideo(ctx, t, req, in)
	case "vidu-video":
		tok, err = s.submitViduVideo(ctx, t, req, in)
	case "grok-video":
		tok, err = s.submitGrokVideo(ctx, t, req, in)
	case "agnes-video":
		tok, err = s.submitAgnesVideo(ctx, t, req, in)
	default:
		return domainmedia.VideoSubmitToken{}, apperr.Validation("Unsupported video protocol: " + t.protocol)
	}
	if err != nil {
		return domainmedia.VideoSubmitToken{}, err
	}
	tok.Protocol = t.protocol
	tok.ProviderID = t.ownerID
	tok.ModelRef = t.resolvedRef
	tok.ModelID = t.modelID
	tok.OutputDir = req.OutputDir
	tok.DurationSec = req.DurationSec
	tok.SubmittedAtMs = time.Now().UnixMilli()
	return tok, nil
}

func (s *Service) pollVideoTargetOnce(ctx context.Context, t targetSpec, tok domainmedia.VideoSubmitToken) (pollProbeOutcome, error) {
	switch tok.Protocol {
	case "openai-video":
		return s.pollOpenAIVideoOnce(ctx, t, tok)
	case "gemini-aistudio-video", "gemini-vertex-video":
		return s.pollGeminiVideoOnce(ctx, t, tok)
	case "ark-video":
		return s.pollArkVideoOnce(ctx, t, tok)
	case "dashscope-video":
		return s.pollDashScopeVideoOnce(ctx, t, tok)
	case "minimax-video":
		return s.pollMiniMaxVideoOnce(ctx, t, tok)
	case "kling-video":
		return s.pollKlingVideoOnce(ctx, t, tok)
	case "vidu-video":
		return s.pollViduVideoOnce(ctx, t, tok)
	case "grok-video":
		return s.pollGrokVideoOnce(ctx, t, tok)
	case "agnes-video":
		return s.pollAgnesVideoOnce(ctx, t, tok)
	default:
		return pollProbeOutcome{failed: true, errText: "unsupported video protocol"}, apperr.Validation("Unsupported video protocol: " + tok.Protocol)
	}
}

func pickNearestInt(choices []int, target int) int {
	best := choices[0]
	bestDiff := absInt(best - target)
	for _, c := range choices[1:] {
		if c == target {
			return c
		}
		if d := absInt(c - target); d < bestDiff {
			best = c
			bestDiff = d
		}
	}
	return best
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
