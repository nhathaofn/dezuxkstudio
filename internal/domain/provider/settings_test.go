package provider

import (
	"reflect"
	"slices"
	"testing"
)

func TestParseSettings(t *testing.T) {
	set, clear, err := ParseSettings(map[string]string{SettingImageMaxWorkers: " 4 ", SettingRequestGapMs: "0", SettingVideoRPM: ""})
	if err != nil || set[SettingImageMaxWorkers] != 4 || len(set) != 2 {
		t.Fatalf("%v %v", set, err)
	}
	if _, ok := set[SettingRequestGapMs]; !ok {
		t.Fatal("explicit 0 must be stored")
	}
	if !reflect.DeepEqual(clear, []string{SettingVideoRPM}) {
		t.Fatal(clear)
	}
	bad := []map[string]string{
		{"bogus": "1"},
		{SettingImageMaxWorkers: "0"},  // below min
		{SettingImageMaxWorkers: "65"}, // above max
		{SettingImageRPM: "-1"},
		{SettingImageRPM: "10001"},
		{SettingRequestGapMs: "abc"},
		{SettingRequestGapMs: "1.5"},
	}
	for _, b := range bad {
		if _, _, err := ParseSettings(b); err == nil {
			t.Errorf("%v must fail", b)
		}
	}
	set, clear, err = ParseSettings(nil)
	if err != nil || len(set) != 0 || len(clear) != 0 {
		t.Fatal("nil patch")
	}
}

func TestSettingSpecs(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range SettingSpecs() {
		if seen[s.Key] || s.Min > s.Max {
			t.Fatalf("bad spec %+v", s)
		}
		seen[s.Key] = true
		if s.Capability != "" && !slices.Contains(Capabilities(), s.Capability) {
			t.Fatalf("bad capability %+v", s)
		}
	}
	if _, ok := SettingSpecFor("nope"); ok {
		t.Fatal("unknown")
	}
	if s, ok := SettingSpecFor(SettingRequestGapMs); !ok || s.Capability != "" {
		t.Fatal(s)
	}
}

func TestEffectiveLimits(t *testing.T) {
	def := LimitDefaults{MaxWorkers: 3, RPM: 20, RequestGapMs: 50}
	meta := map[Capability]int{CapabilityVideo: 1, CapabilityImage: 7}

	l := EffectiveLimits(nil, def, nil)
	if l.MaxWorkers[CapabilityImage] != 3 || l.MaxWorkers[CapabilityTTS] != 3 || l.RPM[CapabilityVideo] != 20 || l.RequestGapMs != 50 {
		t.Fatalf("%+v", l)
	}
	// meta beats config for workers, not for RPM
	l = EffectiveLimits(nil, def, meta)
	if l.MaxWorkers[CapabilityVideo] != 1 || l.MaxWorkers[CapabilityImage] != 7 || l.MaxWorkers[CapabilityTTS] != 3 {
		t.Fatalf("%+v", l)
	}
	if l.RPM[CapabilityVideo] != 20 || l.RPM[CapabilityImage] != 20 {
		t.Fatalf("RPM must ignore meta concurrency: %+v", l.RPM)
	}
	// stored beats meta, including an explicit 0
	l = EffectiveLimits(Settings{SettingVideoMaxWorkers: 9, SettingImageRPM: 0, SettingRequestGapMs: 0}, def, meta)
	if l.MaxWorkers[CapabilityVideo] != 9 || l.RPM[CapabilityImage] != 0 || l.RequestGapMs != 0 {
		t.Fatalf("%+v", l)
	}
}

func TestMetaValidateDefaultConcurrency(t *testing.T) {
	base := Meta{ID: "x", DisplayName: "X", Capabilities: []Capability{CapabilityLLM, CapabilityVideo},
		Probe: Probe{Kind: ProbeOpenAIModels}}
	ok := base
	ok.DefaultConcurrency = map[Capability]int{CapabilityVideo: 1}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, dc := range map[string]map[Capability]int{
		"llm":         {CapabilityLLM: 1},
		"unsupported": {CapabilityTTS: 1},
		"zero":        {CapabilityVideo: 0},
	} {
		m := base
		m.DefaultConcurrency = dc
		if m.Validate() == nil {
			t.Errorf("%s must fail", name)
		}
	}
}
