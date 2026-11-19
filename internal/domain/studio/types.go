// Package studio defines pure domain models, invariants, and repository contracts
// for DezuxkStudio's AI film production studio.
package studio

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"app/internal/apperr"
	domainmedia "app/internal/domain/mediagen"
)

const (
	// MinShotDurationSec and MaxShotDurationSec define domain bounds for shot durations when no runtime StudioConfig override is supplied.
	MinShotDurationSec = 1
	MaxShotDurationSec = 60

	// BaselineVideoWidth, BaselineVideoHeight, and BaselineVideoFPS represent default 1080p/24fps studio domain geometry when caller dimensions are omitted.
	BaselineVideoWidth  = 1920
	BaselineVideoHeight = 1080
	BaselineVideoFPS    = 24
	DefaultBGMVolume    = 0.22

	// MaxSlugLength bounds filesystem-safe project and episode directory slugs.
	MaxSlugLength = 64
)

func evenDim(v int) int {
	if v < 2 {
		return 2
	}
	if v%2 != 0 {
		return v + 1
	}
	return v
}

// ResolutionForAspectRatio computes an even-rounded WxH pixel resolution for aspect scaled relative to fallback
// (defaulting to BaselineVideoWidth x BaselineVideoHeight when fallback is empty or invalid).
func ResolutionForAspectRatio(aspect, fallback string) string {
	baseW, baseH := ParseResolutionDims(fallback, BaselineVideoWidth, BaselineVideoHeight)
	shortEdge := min(baseW, baseH)
	longEdge := max(baseW, baseH)

	key := strings.ToLower(strings.TrimSpace(aspect))
	if parts := strings.Split(key, ":"); len(parts) == 2 {
		aw, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		ah, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 == nil && err2 == nil && aw > 0 && ah > 0 {
			if baseW*ah == baseH*aw {
				return fmt.Sprintf("%dx%d", evenDim(baseW), evenDim(baseH))
			}
			// Ultrawide aspect ratios wider than 16:9 preserve the base long edge as width.
			if aw*9 > ah*16 {
				h := evenDim(int(math.Round(float64(longEdge) * float64(ah) / float64(aw))))
				return fmt.Sprintf("%dx%d", evenDim(longEdge), h)
			}
			if aw >= ah {
				w := evenDim(int(math.Round(float64(shortEdge) * float64(aw) / float64(ah))))
				return fmt.Sprintf("%dx%d", w, evenDim(shortEdge))
			}
			h := evenDim(int(math.Round(float64(shortEdge) * float64(ah) / float64(aw))))
			return fmt.Sprintf("%dx%d", evenDim(shortEdge), h)
		}
	}
	if ew, eh, ok := domainmedia.ParseWxH(key); ok {
		return fmt.Sprintf("%dx%d", evenDim(ew), evenDim(eh))
	}
	return fmt.Sprintf("%dx%d", evenDim(baseW), evenDim(baseH))
}

// ParseResolutionDims parses "WxH" pixels, falling back to fallbackWidth/fallbackHeight (or BaselineVideoWidth x BaselineVideoHeight).
func ParseResolutionDims(res string, fallbackWidth, fallbackHeight int) (int, int) {
	if fallbackWidth <= 0 {
		fallbackWidth = BaselineVideoWidth
	}
	if fallbackHeight <= 0 {
		fallbackHeight = BaselineVideoHeight
	}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(res)), "x")
	if len(parts) != 2 {
		return fallbackWidth, fallbackHeight
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return fallbackWidth, fallbackHeight
	}
	return w, h
}

// Genre identifies the storytelling format of a film studio project.
type Genre string

const (
	GenreDrama     Genre = "drama"
	GenreNarration Genre = "narration"
)

// Genres returns all supported project genres in display order.
func Genres() []Genre {
	return []Genre{GenreDrama, GenreNarration}
}

// ProductionMode identifies how video shots are generated.
type ProductionMode string

const (
	ModeStoryboardI2V ProductionMode = "storyboard_i2v"
	ModeR2V           ProductionMode = "r2v"
)

// ProductionModes returns all supported production modes in display order.
func ProductionModes() []ProductionMode {
	return []ProductionMode{ModeStoryboardI2V, ModeR2V}
}

// AssetKind classifies an entry in the project's asset bible.
type AssetKind string

const (
	AssetCharacter AssetKind = "character"
	AssetScene     AssetKind = "scene"
	AssetProp      AssetKind = "prop"
)

// AssetKinds returns all supported asset kinds in display order.
func AssetKinds() []AssetKind {
	return []AssetKind{AssetCharacter, AssetScene, AssetProp}
}

// ShotMode identifies how a shot's visual keyframes/video are produced.
type ShotMode string

const (
	ShotModeSingle  ShotMode = "single"
	ShotModeGrid2x2 ShotMode = "grid_2x2"
	ShotModeGrid3x3 ShotMode = "grid_3x3"
	ShotModeR2V     ShotMode = "r2v"
)

// ShotModes returns all supported shot modes in display order.
func ShotModes() []ShotMode {
	return []ShotMode{ShotModeSingle, ShotModeGrid2x2, ShotModeGrid3x3, ShotModeR2V}
}

// ShotStatus tracks the production lifecycle of a single shot.
type ShotStatus string

const (
	ShotPending       ShotStatus = "pending"
	ShotKeyframeReady ShotStatus = "keyframe_ready"
	ShotVideoReady    ShotStatus = "video_ready"
	ShotTTSReady      ShotStatus = "tts_ready"
	ShotQCPassed      ShotStatus = "qc_passed"
	ShotFailed        ShotStatus = "failed"
)

// ShotStatuses returns all valid shot statuses in lifecycle order.
func ShotStatuses() []ShotStatus {
	return []ShotStatus{
		ShotPending,
		ShotKeyframeReady,
		ShotVideoReady,
		ShotTTSReady,
		ShotQCPassed,
		ShotFailed,
	}
}

// EpisodeStage tracks the current ArcReel workflow step of an episode.
type EpisodeStage string

const (
	StageScript     EpisodeStage = "script"
	StageAssets     EpisodeStage = "assets"
	StageStoryboard EpisodeStage = "storyboard"
	StageVideo      EpisodeStage = "video"
	StageExport     EpisodeStage = "export"
)

// EpisodeStages returns all Phase 1 episode stages in workflow order.
func EpisodeStages() []EpisodeStage {
	return []EpisodeStage{
		StageScript,
		StageAssets,
		StageStoryboard,
		StageVideo,
		StageExport,
	}
}

// OverrunPolicy defines how timeline_auto_assemble handles speech audio longer than its video shot.
type OverrunPolicy string

const (
	OverrunWarn            OverrunPolicy = "warn"
	OverrunFreezeLastFrame OverrunPolicy = "freeze_last_frame"
	OverrunSpeedFit        OverrunPolicy = "speed_fit"
)

// OverrunPolicies returns all supported timeline speech overrun policies.
func OverrunPolicies() []OverrunPolicy {
	return []OverrunPolicy{OverrunWarn, OverrunFreezeLastFrame, OverrunSpeedFit}
}

// Valid reports whether p is a supported OverrunPolicy.
func (p OverrunPolicy) Valid() bool {
	return slices.Contains(OverrunPolicies(), p)
}

// ValidateOverrunPolicy checks that p is a supported OverrunPolicy.
func ValidateOverrunPolicy(p OverrunPolicy) error {
	if !p.Valid() {
		return apperr.Validation(fmt.Sprintf("unsupported overrun policy %q", p)).
			WithMsg("errors.studio.invalid_overrun_policy", map[string]any{"value": string(p)})
	}
	return nil
}

// Project represents a film studio project backed by SQLite + workspace JSON/media files.
type Project struct {
	ID                 string         `json:"id"`
	Slug               string         `json:"slug"`
	Title              string         `json:"title"`
	Genre              Genre          `json:"genre"`
	ProductionMode     ProductionMode `json:"production_mode"`
	AspectRatio        string         `json:"aspect_ratio"`
	StylePrompt        string         `json:"style_prompt"`
	RootDir            string         `json:"root_dir"`
	AutoApproveBatches bool           `json:"auto_approve_batches"`
	DefaultTextModel   string         `json:"default_text_model"`
	DefaultImageModel  string         `json:"default_image_model"`
	DefaultVideoModel  string         `json:"default_video_model"`
	DefaultTTSModel    string         `json:"default_tts_model"`
	CreatedAt          int64          `json:"created_at"`
	UpdatedAt          int64          `json:"updated_at"`
}

// Episode represents one episode within a project.
type Episode struct {
	ID               string       `json:"id"`
	ProjectID        string       `json:"project_id"`
	Code             string       `json:"code"`
	Title            string       `json:"title"`
	Stage            EpisodeStage `json:"stage"`
	RawScriptPath    string       `json:"raw_script_path"`
	ScriptJSONPath   string       `json:"script_json_path"`
	AssetsJSONPath   string       `json:"assets_json_path"`
	ShotsJSONPath    string       `json:"shots_json_path"`
	TimelineJSONPath string       `json:"timeline_json_path"`
	CreatedAt        int64        `json:"created_at"`
	UpdatedAt        int64        `json:"updated_at"`
}

// NormalizedScript is the structured screenplay representation produced by tool.script_normalize.
type NormalizedScript struct {
	Title       string   `json:"title"`
	Genre       Genre    `json:"genre"`
	AspectRatio string   `json:"aspect_ratio"`
	Logline     string   `json:"logline"`
	Language    string   `json:"language,omitempty"`
	Characters  []string `json:"characters,omitempty"`
	Scenes      []Scene  `json:"scenes"`
}

// Scene groups dramatic or narrative beats sharing a location and time.
type Scene struct {
	Code      string `json:"code"`
	Title     string `json:"title"`
	Location  string `json:"location,omitempty"`
	TimeOfDay string `json:"time_of_day,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Beats     []Beat `json:"beats"`
}

// Beat is one narrative unit that maps to one or more storyboard shots.
type Beat struct {
	Code        string             `json:"code"`
	Description string             `json:"description"`
	VisualHint  string             `json:"visual_hint,omitempty"`
	CameraHint  string             `json:"camera_hint,omitempty"`
	DurationSec int                `json:"duration_sec"`
	AssetTags   []string           `json:"asset_tags,omitempty"`
	Dialogues   []DialogueLine     `json:"dialogues,omitempty"`
	Narrations  []NarrationSegment `json:"narrations,omitempty"`
}

// DialogueLine represents a spoken line by a character in Drama mode.
type DialogueLine struct {
	Speaker string `json:"speaker"`
	Line    string `json:"line"`
	Emotion string `json:"emotion,omitempty"`
	Cue     string `json:"cue,omitempty"`
}

// NarrationSegment represents a voiceover segment in Narration mode.
type NarrationSegment struct {
	Text         string  `json:"text"`
	Tone         string  `json:"tone,omitempty"`
	EstimatedSec float64 `json:"estimated_sec,omitempty"`
}

// AssetEntry represents a reusable visual/voice bible item (character, scene, or prop).
type AssetEntry struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	Kind           AssetKind `json:"kind"`
	Name           string    `json:"name"`
	CodeTag        string    `json:"code_tag"`
	Description    string    `json:"description"`
	Prompt         string    `json:"prompt"`
	PrimaryAssetID string    `json:"primary_asset_id"`
	PrimaryPath    string    `json:"primary_path"`
	VoiceProvider  string    `json:"voice_provider,omitempty"`
	VoiceID        string    `json:"voice_id,omitempty"`
	VoiceStyle     string    `json:"voice_style,omitempty"`
	UpdatedAt      int64     `json:"updated_at"`
}

// Shot represents a single production shot in an episode storyboard.
type Shot struct {
	ID              string         `json:"id"`
	EpisodeID       string         `json:"episode_id"`
	Seq             int            `json:"seq"`
	SceneCode       string         `json:"scene_code"`
	Title           string         `json:"title"`
	ShotMode        ShotMode       `json:"shot_mode"`
	DurationSec     int            `json:"duration_sec"`
	ImagePrompt     string         `json:"image_prompt"`
	VideoPrompt     string         `json:"video_prompt"`
	NarrationText   string         `json:"narration_text"`
	Dialogues       []DialogueLine `json:"dialogues,omitempty"`
	RefAssetTags    []string       `json:"ref_asset_tags,omitempty"`
	KeyframeAssetID string         `json:"keyframe_asset_id"`
	KeyframePath    string         `json:"keyframe_path"`
	VideoAssetID    string         `json:"video_asset_id"`
	VideoPath       string         `json:"video_path"`
	AudioAssetID    string         `json:"audio_asset_id"`
	AudioPath       string         `json:"audio_path"`
	Status          ShotStatus     `json:"status"`
	ErrorText       string         `json:"error_text"`
	UpdatedAt       int64          `json:"updated_at"`
}

// ShotPromptPackage wraps the authored shots produced by tool.author_shot_prompts.
type ShotPromptPackage struct {
	EpisodeTitle   string         `json:"episode_title,omitempty"`
	ProductionMode ProductionMode `json:"production_mode"`
	Shots          []Shot         `json:"shots"`
}

// AssetBiblePackage wraps the extracted asset bible produced by tool.extract_assets.
type AssetBiblePackage struct {
	StylePrompt string       `json:"style_prompt,omitempty"`
	Assets      []AssetEntry `json:"assets"`
}

// ValidateGenre checks that g is a supported genre.
func ValidateGenre(g Genre) error {
	if !slices.Contains(Genres(), g) {
		return apperr.Validation(fmt.Sprintf("unsupported genre %q", g)).
			WithMsg("errors.studio.invalid_genre", map[string]any{"value": string(g)})
	}
	return nil
}

// ValidateProductionMode checks that m is a supported production mode.
func ValidateProductionMode(m ProductionMode) error {
	if !slices.Contains(ProductionModes(), m) {
		return apperr.Validation(fmt.Sprintf("unsupported production mode %q", m)).
			WithMsg("errors.studio.invalid_production_mode", map[string]any{"value": string(m)})
	}
	return nil
}

// ValidateAssetKind checks that k is a supported asset kind.
func ValidateAssetKind(k AssetKind) error {
	if !slices.Contains(AssetKinds(), k) {
		return apperr.Validation(fmt.Sprintf("unsupported asset kind %q", k)).
			WithMsg("errors.studio.invalid_asset_kind", map[string]any{"value": string(k)})
	}
	return nil
}

// ValidateShotMode checks that m is a supported shot mode.
func ValidateShotMode(m ShotMode) error {
	if !slices.Contains(ShotModes(), m) {
		return apperr.Validation(fmt.Sprintf("unsupported shot mode %q", m)).
			WithMsg("errors.studio.invalid_shot_mode", map[string]any{"value": string(m)})
	}
	return nil
}

// ValidateShotStatus checks that s is a supported shot status.
func ValidateShotStatus(s ShotStatus) error {
	if !slices.Contains(ShotStatuses(), s) {
		return apperr.Validation(fmt.Sprintf("unsupported shot status %q", s)).
			WithMsg("errors.studio.invalid_shot_status", map[string]any{"value": string(s)})
	}
	return nil
}

// ValidateEpisodeStage checks that s is a valid episode stage.
func ValidateEpisodeStage(s EpisodeStage) error {
	if !slices.Contains(EpisodeStages(), s) {
		return apperr.Validation(fmt.Sprintf("unsupported episode stage %q", s)).
			WithMsg("errors.studio.invalid_stage", map[string]any{"value": string(s)})
	}
	return nil
}

// Normalize trims fields and applies defaults for a Project.
func (p *Project) Normalize() {
	p.ID = strings.TrimSpace(p.ID)
	p.Title = strings.TrimSpace(p.Title)
	p.Slug = strings.TrimSpace(p.Slug)
	if p.Slug == "" && p.Title != "" {
		p.Slug = Slugify(p.Title)
	}
	if p.Genre == "" {
		p.Genre = GenreDrama
	}
	if p.ProductionMode == "" {
		p.ProductionMode = ModeStoryboardI2V
	}
	p.AspectRatio = strings.TrimSpace(p.AspectRatio)
	if p.AspectRatio == "" {
		p.AspectRatio = domainmedia.DefaultVideoAspectRatio
	}
	p.StylePrompt = strings.TrimSpace(p.StylePrompt)
	p.RootDir = strings.TrimSpace(p.RootDir)
	p.DefaultTextModel = strings.TrimSpace(p.DefaultTextModel)
	p.DefaultImageModel = strings.TrimSpace(p.DefaultImageModel)
	p.DefaultVideoModel = strings.TrimSpace(p.DefaultVideoModel)
	p.DefaultTTSModel = strings.TrimSpace(p.DefaultTTSModel)
}

// Validate normalizes and verifies Project invariants.
func (p *Project) Validate() error {
	p.Normalize()
	if p.Title == "" {
		return apperr.Validation("project title is required").
			WithMsg("errors.studio.project_title_required", nil)
	}
	if p.Slug == "" {
		return apperr.Validation("project slug is required").
			WithMsg("errors.studio.project_slug_required", nil)
	}
	if err := ValidateGenre(p.Genre); err != nil {
		return err
	}
	if err := ValidateProductionMode(p.ProductionMode); err != nil {
		return err
	}
	if !slices.Contains(domainmedia.VideoAspectRatios(), p.AspectRatio) {
		return apperr.Validation(fmt.Sprintf("unsupported aspect ratio %q", p.AspectRatio)).
			WithMsg("errors.mediagen.invalid_aspect_ratio", map[string]any{"value": p.AspectRatio})
	}
	return nil
}

// Normalize trims fields and applies defaults for an Episode.
func (e *Episode) Normalize() {
	e.ID = strings.TrimSpace(e.ID)
	e.ProjectID = strings.TrimSpace(e.ProjectID)
	e.Code = strings.TrimSpace(e.Code)
	e.Title = strings.TrimSpace(e.Title)
	if e.Code == "" {
		e.Code = "ep01"
	} else {
		e.Code = Slugify(e.Code)
	}
	if e.Title == "" {
		e.Title = strings.ToUpper(e.Code)
	}
	if e.Stage == "" {
		e.Stage = StageScript
	}
}

// Validate normalizes and verifies Episode invariants.
func (e *Episode) Validate() error {
	e.Normalize()
	if e.ProjectID == "" {
		return apperr.Validation("project_id is required")
	}
	if e.Code == "" {
		return apperr.Validation("episode code is required")
	}
	if e.Title == "" {
		return apperr.Validation("episode title is required")
	}
	return ValidateEpisodeStage(e.Stage)
}

// Normalize trims fields and applies defaults for an AssetEntry.
func (a *AssetEntry) Normalize() {
	a.ID = strings.TrimSpace(a.ID)
	a.ProjectID = strings.TrimSpace(a.ProjectID)
	a.Name = strings.TrimSpace(a.Name)
	a.CodeTag = NormalizeCodeTag(a.CodeTag, a.Name)
	a.Description = strings.TrimSpace(a.Description)
	a.Prompt = strings.TrimSpace(a.Prompt)
	a.PrimaryAssetID = strings.TrimSpace(a.PrimaryAssetID)
	a.PrimaryPath = strings.TrimSpace(a.PrimaryPath)
	a.VoiceProvider = strings.TrimSpace(a.VoiceProvider)
	a.VoiceID = strings.TrimSpace(a.VoiceID)
	a.VoiceStyle = strings.TrimSpace(a.VoiceStyle)
	if a.Kind == "" {
		a.Kind = AssetCharacter
	}
}

// Validate normalizes and verifies AssetEntry invariants.
func (a *AssetEntry) Validate() error {
	a.Normalize()
	if a.ProjectID == "" {
		return apperr.Validation("project_id is required")
	}
	if a.Name == "" {
		return apperr.Validation("asset name is required").
			WithMsg("errors.studio.asset_name_required", nil)
	}
	if a.CodeTag == "" {
		return apperr.Validation("asset code_tag is required")
	}
	return ValidateAssetKind(a.Kind)
}

// Normalize trims fields and applies defaults for a Shot.
func (s *Shot) Normalize() {
	s.NormalizeWithDefaultDuration(domainmedia.DefaultVideoDurationSec)
}

// NormalizeWithDefaultDuration trims fields and populates unset shot fields using defaultDurationSec.
func (s *Shot) NormalizeWithDefaultDuration(defaultDurationSec int) {
	if defaultDurationSec < MinShotDurationSec {
		defaultDurationSec = domainmedia.DefaultVideoDurationSec
	}
	s.ID = strings.TrimSpace(s.ID)
	s.EpisodeID = strings.TrimSpace(s.EpisodeID)
	s.SceneCode = strings.TrimSpace(s.SceneCode)
	s.Title = strings.TrimSpace(s.Title)
	if s.ShotMode == "" {
		s.ShotMode = ShotModeSingle
	}
	if s.DurationSec <= 0 {
		s.DurationSec = defaultDurationSec
	}
	if s.Status == "" {
		s.Status = ShotPending
	}
	s.ImagePrompt = strings.TrimSpace(s.ImagePrompt)
	s.VideoPrompt = strings.TrimSpace(s.VideoPrompt)
	s.NarrationText = strings.TrimSpace(s.NarrationText)
	s.KeyframeAssetID = strings.TrimSpace(s.KeyframeAssetID)
	s.KeyframePath = strings.TrimSpace(s.KeyframePath)
	s.VideoAssetID = strings.TrimSpace(s.VideoAssetID)
	s.VideoPath = strings.TrimSpace(s.VideoPath)
	s.AudioAssetID = strings.TrimSpace(s.AudioAssetID)
	s.AudioPath = strings.TrimSpace(s.AudioPath)
	s.ErrorText = strings.TrimSpace(s.ErrorText)
	s.RefAssetTags = cleanTags(s.RefAssetTags)
}

// Validate normalizes and verifies Shot invariants using default domain duration limits.
func (s *Shot) Validate() error {
	return s.ValidateWithLimits(MaxShotDurationSec, domainmedia.DefaultVideoDurationSec)
}

// ValidateWithMaxDuration normalizes and verifies Shot invariants with a configurable maxDurationSec bound.
func (s *Shot) ValidateWithMaxDuration(maxDurationSec int) error {
	return s.ValidateWithLimits(maxDurationSec, domainmedia.DefaultVideoDurationSec)
}

// ValidateWithLimits normalizes and verifies Shot invariants using configurable max and default duration bounds.
func (s *Shot) ValidateWithLimits(maxDurationSec, defaultDurationSec int) error {
	if maxDurationSec < MinShotDurationSec {
		maxDurationSec = MaxShotDurationSec
	}
	if defaultDurationSec < MinShotDurationSec {
		defaultDurationSec = domainmedia.DefaultVideoDurationSec
	}
	if defaultDurationSec > maxDurationSec {
		defaultDurationSec = maxDurationSec
	}
	s.NormalizeWithDefaultDuration(defaultDurationSec)
	if s.EpisodeID == "" {
		return apperr.Validation("episode_id is required").
			WithMsg("errors.studio.episode_id_required", nil)
	}
	if s.Seq < 0 {
		return apperr.Validation("shot seq must be >= 0").
			WithMsg("errors.studio.shot_seq_invalid", nil)
	}
	if s.DurationSec < MinShotDurationSec || s.DurationSec > maxDurationSec {
		return apperr.Validation(fmt.Sprintf("shot duration_sec must be between %d and %d", MinShotDurationSec, maxDurationSec)).
			WithMsg("errors.studio.shot_duration_invalid", map[string]any{"min": MinShotDurationSec, "max": maxDurationSec})
	}
	if err := ValidateShotMode(s.ShotMode); err != nil {
		return err
	}
	return ValidateShotStatus(s.Status)
}

// NormalizeCodeTag ensures an @Tag identifier is well-formed (e.g. "@Hero").
func NormalizeCodeTag(tag, fallbackName string) string {
	raw := strings.TrimSpace(tag)
	if raw == "" {
		raw = strings.TrimSpace(fallbackName)
	}
	raw = strings.TrimPrefix(raw, "@")
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('@')
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(asciiFold(r))
		case r == '_' || r == '-':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "@" || out == "" {
		return ""
	}
	return out
}

func cleanTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
		norm := NormalizeCodeTag(t, "")
		if norm == "" || seen[strings.ToLower(norm)] {
			continue
		}
		seen[strings.ToLower(norm)] = true
		out = append(out, norm)
	}
	return out
}

func isTagBoundaryRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

func replaceExactTagInText(text, oldTag, newTag string) (string, bool) {
	if text == "" || oldTag == "" || strings.EqualFold(oldTag, newTag) {
		return text, false
	}
	lowerText := strings.ToLower(text)
	lowerOld := strings.ToLower(oldTag)
	if !strings.Contains(lowerText, lowerOld) {
		return text, false
	}
	var b strings.Builder
	changed := false
	i := 0
	for i < len(text) {
		idx := strings.Index(strings.ToLower(text[i:]), lowerOld)
		if idx < 0 {
			b.WriteString(text[i:])
			break
		}
		absIdx := i + idx
		endIdx := absIdx + len(oldTag)
		// Check boundaries in original string.
		prevOK := true
		if absIdx > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:absIdx])
			prevOK = !isTagBoundaryRune(r)
		}
		nextOK := true
		if endIdx < len(text) {
			r, _ := utf8.DecodeRuneInString(text[endIdx:])
			nextOK = !isTagBoundaryRune(r)
		}
		if prevOK && nextOK {
			b.WriteString(text[i:absIdx])
			b.WriteString(newTag)
			changed = true
			i = endIdx
		} else {
			b.WriteString(text[i : absIdx+1])
			i = absIdx + 1
		}
	}
	if !changed {
		return text, false
	}
	return b.String(), true
}

// RewriteAssetTagReferences deterministically rewrites @OldTag -> @NewTag and character Speaker names
// across an optional NormalizedScript and slice of Shots without performing any I/O.
func RewriteAssetTagReferences(oldTag, newTag, oldSpeaker, newSpeaker string, script *NormalizedScript, shots []Shot) (scriptModified bool, modifiedShotIDs []string) {
	normOld := NormalizeCodeTag(oldTag, "")
	normNew := NormalizeCodeTag(newTag, "")
	trimOldSpk := strings.TrimSpace(oldSpeaker)
	trimNewSpk := strings.TrimSpace(newSpeaker)

	rewriteTags := func(tags []string) ([]string, bool) {
		if normOld == "" || normNew == "" || len(tags) == 0 {
			return tags, false
		}
		changed := false
		out := make([]string, len(tags))
		for i, t := range tags {
			nt := NormalizeCodeTag(t, "")
			if strings.EqualFold(nt, normOld) {
				out[i] = normNew
				if !strings.EqualFold(nt, normNew) || t != normNew {
					changed = true
				}
			} else {
				out[i] = t
			}
		}
		cleaned := cleanTags(out)
		if !changed && len(cleaned) != len(tags) {
			changed = true
		}
		return cleaned, changed
	}

	if script != nil {
		if trimOldSpk != "" && trimNewSpk != "" && trimOldSpk != trimNewSpk {
			seenChars := make(map[string]bool, len(script.Characters))
			nextChars := make([]string, 0, len(script.Characters))
			for _, ch := range script.Characters {
				replaced := ch
				if strings.EqualFold(strings.TrimSpace(ch), trimOldSpk) {
					replaced = trimNewSpk
					if ch != trimNewSpk {
						scriptModified = true
					}
				}
				key := strings.ToLower(strings.TrimSpace(replaced))
				if key != "" && !seenChars[key] {
					seenChars[key] = true
					nextChars = append(nextChars, replaced)
				} else if key != "" {
					scriptModified = true
				}
			}
			script.Characters = nextChars
		}
		for si := range script.Scenes {
			for bi := range script.Scenes[si].Beats {
				bt := &script.Scenes[si].Beats[bi]
				if nextTags, ok := rewriteTags(bt.AssetTags); ok {
					bt.AssetTags = nextTags
					scriptModified = true
				}
				if normOld != "" && normNew != "" {
					if v, ok := replaceExactTagInText(bt.Description, normOld, normNew); ok {
						bt.Description = v
						scriptModified = true
					}
					if v, ok := replaceExactTagInText(bt.VisualHint, normOld, normNew); ok {
						bt.VisualHint = v
						scriptModified = true
					}
				}
				if trimOldSpk != "" && trimNewSpk != "" && trimOldSpk != trimNewSpk {
					for di := range bt.Dialogues {
						if strings.EqualFold(strings.TrimSpace(bt.Dialogues[di].Speaker), trimOldSpk) && bt.Dialogues[di].Speaker != trimNewSpk {
							bt.Dialogues[di].Speaker = trimNewSpk
							scriptModified = true
						}
					}
				}
			}
		}
	}

	for i := range shots {
		sh := &shots[i]
		shotChanged := false
		if nextTags, ok := rewriteTags(sh.RefAssetTags); ok {
			sh.RefAssetTags = nextTags
			shotChanged = true
		}
		if normOld != "" && normNew != "" {
			for _, field := range []*string{&sh.Title, &sh.ImagePrompt, &sh.VideoPrompt, &sh.NarrationText} {
				if v, ok := replaceExactTagInText(*field, normOld, normNew); ok {
					*field = v
					shotChanged = true
				}
			}
		}
		if trimOldSpk != "" && trimNewSpk != "" && trimOldSpk != trimNewSpk {
			for di := range sh.Dialogues {
				if strings.EqualFold(strings.TrimSpace(sh.Dialogues[di].Speaker), trimOldSpk) && sh.Dialogues[di].Speaker != trimNewSpk {
					sh.Dialogues[di].Speaker = trimNewSpk
					shotChanged = true
				}
			}
		}
		if shotChanged {
			modifiedShotIDs = append(modifiedShotIDs, sh.ID)
		}
	}
	return scriptModified, modifiedShotIDs
}

// Slugify produces a lowercase kebab-case filesystem-safe slug from title.
func Slugify(title string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.TrimSpace(title) {
		folded := unicode.ToLower(asciiFold(r))
		if (folded >= 'a' && folded <= 'z') || (folded >= '0' && folded <= '9') {
			b.WriteRune(folded)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "project"
	}
	if len(slug) > MaxSlugLength {
		slug = strings.TrimRight(slug[:MaxSlugLength], "-")
	}
	if slug == "" {
		return "project"
	}
	return slug
}

func asciiFold(r rune) rune {
	switch r {
	case 'đ':
		return 'd'
	case 'Đ':
		return 'D'
	}
	switch {
	case strings.ContainsRune("àáảãạăằắẳẵặâầấẩẫậ", r):
		return 'a'
	case strings.ContainsRune("ÀÁẢÃẠĂẰẮẲẴẶÂẦẤẨẪẬ", r):
		return 'A'
	case strings.ContainsRune("èéẻẽẹêềếểễệ", r):
		return 'e'
	case strings.ContainsRune("ÈÉẺẼẸÊỀẾỂỄỆ", r):
		return 'E'
	case strings.ContainsRune("ìíỉĩị", r):
		return 'i'
	case strings.ContainsRune("ÌÍỈĨỊ", r):
		return 'I'
	case strings.ContainsRune("òóỏõọôồốổỗộơờớởỡợ", r):
		return 'o'
	case strings.ContainsRune("ÒÓỎÕỌÔỒỐỔỖỘƠỜỚỞỠỢ", r):
		return 'O'
	case strings.ContainsRune("ùúủũụưừứửữự", r):
		return 'u'
	case strings.ContainsRune("ÙÚỦŨỤƯỪỨỬỮỰ", r):
		return 'U'
	case strings.ContainsRune("ỳýỷỹỵ", r):
		return 'y'
	case strings.ContainsRune("ỲÝỶỸỴ", r):
		return 'Y'
	default:
		return r
	}
}

// ErrProjectNotFound returns a typed not-found error for a project.
func ErrProjectNotFound(id string) error {
	return apperr.New(apperr.CodeNotFound, "Studio project not found").
		WithDetails(map[string]any{"id": id}).
		WithMsg("errors.studio.project_not_found", nil)
}

// ErrEpisodeNotFound returns a typed not-found error for an episode.
func ErrEpisodeNotFound(id string) error {
	return apperr.New(apperr.CodeNotFound, "Studio episode not found").
		WithDetails(map[string]any{"id": id}).
		WithMsg("errors.studio.episode_not_found", nil)
}

// ErrAssetNotFound returns a typed not-found error for a studio asset.
func ErrAssetNotFound(id string) error {
	return apperr.New(apperr.CodeNotFound, "Studio asset not found").
		WithDetails(map[string]any{"id": id}).
		WithMsg("errors.studio.asset_not_found", nil)
}

// ErrShotNotFound returns a typed not-found error for a studio shot.
func ErrShotNotFound(id string) error {
	return apperr.New(apperr.CodeNotFound, "Studio shot not found").
		WithDetails(map[string]any{"id": id}).
		WithMsg("errors.studio.shot_not_found", nil)
}

// Repository persists studio projects, episodes, asset bibles, and shot boards.
type Repository interface {
	ListProjects(ctx context.Context) ([]Project, error)
	GetProject(ctx context.Context, idOrSlug string) (Project, error)
	CreateProject(ctx context.Context, p Project) (Project, error)
	UpdateProject(ctx context.Context, p Project) (Project, error)
	DeleteProject(ctx context.Context, id string) error

	ListEpisodes(ctx context.Context, projectID string) ([]Episode, error)
	GetEpisode(ctx context.Context, id string) (Episode, error)
	CreateEpisode(ctx context.Context, ep Episode) (Episode, error)
	UpdateEpisode(ctx context.Context, ep Episode) (Episode, error)

	ListAssets(ctx context.Context, projectID string) ([]AssetEntry, error)
	GetAsset(ctx context.Context, id string) (AssetEntry, error)
	UpsertAsset(ctx context.Context, a AssetEntry) (AssetEntry, error)
	ReplaceAssets(ctx context.Context, projectID string, assets []AssetEntry) ([]AssetEntry, error)
	DeleteAsset(ctx context.Context, id string) error

	ListShots(ctx context.Context, episodeID string) ([]Shot, error)
	GetShot(ctx context.Context, id string) (Shot, error)
	UpsertShot(ctx context.Context, s Shot) (Shot, error)
	ReplaceShots(ctx context.Context, episodeID string, shots []Shot) ([]Shot, error)
	ReorderShots(ctx context.Context, episodeID string, shotIDs []string) ([]Shot, error)

	Phase2Repository
	Phase3Repository
}
