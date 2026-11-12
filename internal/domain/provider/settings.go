package provider

import (
	"fmt"
	"strconv"
	"strings"

	"app/internal/apperr"
)

// Setting keys of a provider's runtime limits. They apply to built-in and custom providers alike and are
// stored per provider (owner id) in one table; unset keys fall back to the app config defaults.
const (
	SettingImageMaxWorkers = "image_max_workers" // image requests running at once
	SettingVideoMaxWorkers = "video_max_workers"
	SettingTTSMaxWorkers   = "tts_max_workers"
	SettingImageRPM        = "image_rpm" // image requests per minute (0 = unlimited)
	SettingVideoRPM        = "video_rpm"
	SettingRequestGapMs    = "request_gap_ms" // minimum pause between two requests (0 = none)
)

// SettingSpec declares one provider setting.
type SettingSpec struct {
	Key string
	// Capability the setting applies to ("" = every capability); the UI hides settings for capabilities
	// the provider does not offer.
	Capability Capability
	Min, Max   int
}

// SettingSpecs is the single list of provider settings.
func SettingSpecs() []SettingSpec {
	return []SettingSpec{
		{Key: SettingImageMaxWorkers, Capability: CapabilityImage, Min: 1, Max: 64},
		{Key: SettingVideoMaxWorkers, Capability: CapabilityVideo, Min: 1, Max: 64},
		{Key: SettingTTSMaxWorkers, Capability: CapabilityTTS, Min: 1, Max: 64},
		{Key: SettingImageRPM, Capability: CapabilityImage, Min: 0, Max: 10000},
		{Key: SettingVideoRPM, Capability: CapabilityVideo, Min: 0, Max: 10000},
		{Key: SettingRequestGapMs, Min: 0, Max: 600000},
	}
}

// SettingSpecFor looks a setting up.
func SettingSpecFor(key string) (SettingSpec, bool) {
	for _, s := range SettingSpecs() {
		if s.Key == key {
			return s, true
		}
	}
	return SettingSpec{}, false
}

// Settings maps setting keys to values; a missing key means "use the default".
type Settings map[string]int

// ParseSettings validates a raw patch: keys must be declared, values integers within range.
// An empty string value clears the setting (back to the default).
func ParseSettings(raw map[string]string) (set Settings, clear []string, err error) {
	set = Settings{}
	for k, v := range raw {
		spec, ok := SettingSpecFor(k)
		if !ok {
			return nil, nil, apperr.New(apperr.CodeValidation, "Unknown setting "+k).WithDetails(map[string]any{"field": k})
		}
		v = strings.TrimSpace(v)
		if v == "" {
			clear = append(clear, k)
			continue
		}
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < spec.Min || n > spec.Max {
			return nil, nil, apperr.New(apperr.CodeValidation, fmt.Sprintf("%s must be an integer between %d and %d", k, spec.Min, spec.Max)).
				WithDetails(map[string]any{"field": k, "min": spec.Min, "max": spec.Max})
		}
		set[k] = n
	}
	return set, clear, nil
}

// Limits are the effective runtime limits of a provider (stored settings over defaults).
type Limits struct {
	MaxWorkers   map[Capability]int
	RPM          map[Capability]int // 0 = unlimited
	RequestGapMs int
}

// LimitDefaults are the app-wide defaults (from config.Config) used for unset settings.
type LimitDefaults struct {
	MaxWorkers   int
	RPM          int
	RequestGapMs int
}

// EffectiveLimits merges stored settings over defaults; built-in providers may declare their own
// default concurrency per capability (Meta.DefaultConcurrency) between the two.
func EffectiveLimits(s Settings, def LimitDefaults, metaDefaults map[Capability]int) Limits {
	pick := func(key string, c Capability, fallback int) int {
		if v, ok := s[key]; ok {
			return v
		}
		if v, ok := metaDefaults[c]; ok && key != SettingImageRPM && key != SettingVideoRPM {
			return v
		}
		return fallback
	}
	l := Limits{MaxWorkers: map[Capability]int{}, RPM: map[Capability]int{}}
	l.MaxWorkers[CapabilityImage] = pick(SettingImageMaxWorkers, CapabilityImage, def.MaxWorkers)
	l.MaxWorkers[CapabilityVideo] = pick(SettingVideoMaxWorkers, CapabilityVideo, def.MaxWorkers)
	l.MaxWorkers[CapabilityTTS] = pick(SettingTTSMaxWorkers, CapabilityTTS, def.MaxWorkers)
	l.RPM[CapabilityImage] = pick(SettingImageRPM, CapabilityImage, def.RPM)
	l.RPM[CapabilityVideo] = pick(SettingVideoRPM, CapabilityVideo, def.RPM)
	l.RequestGapMs = pick(SettingRequestGapMs, "", def.RequestGapMs)
	return l
}
