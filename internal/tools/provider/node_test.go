package provider

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	domain "app/internal/domain/provider"
	"app/internal/engine"
)

type fakeSvc struct {
	setupReq    domain.SetupRequest
	setupCalls  int
	testID      string
	activateID  string
	deleteID    string
	deleteCalls int
	saved       []engine.Option
	credsOnly   []bool

	discoverID    string
	defaultCalls  int
	defaultPurp   domain.Purpose
	defaultRef    string
	settingsID    string
	settingsRaw   map[string]string
	settingsCalls int
	modelPurpose  domain.Purpose
}

func (f *fakeSvc) OwnerOptions(context.Context) ([]engine.Option, error) {
	return []engine.Option{{Value: "openai", Label: "OpenAI"}}, nil
}
func (f *fakeSvc) ModelOptions(_ context.Context, p domain.Purpose) ([]engine.Option, error) {
	f.modelPurpose = p
	return []engine.Option{{Value: "openai/gpt-4o", Label: "OpenAI"}}, nil
}
func (f *fakeSvc) DiscoverModelsFor(_ context.Context, id string) (any, error) {
	f.discoverID = id
	return map[string]any{"ok": true}, nil
}
func (f *fakeSvc) SetDefaultModel(_ context.Context, p domain.Purpose, ref string) (any, error) {
	f.defaultCalls++
	f.defaultPurp, f.defaultRef = p, ref
	return map[string]any{"ok": true}, nil
}
func (f *fakeSvc) UpdateSettings(_ context.Context, id string, raw map[string]string) (domain.Settings, error) {
	f.settingsCalls++
	f.settingsID, f.settingsRaw = id, raw
	return domain.Settings{"image_max_workers": 2}, nil
}

func (f *fakeSvc) Overview(context.Context) (any, error) { return map[string]any{"ok": 1}, nil }
func (f *fakeSvc) Setup(_ context.Context, r domain.SetupRequest) (any, error) {
	f.setupCalls++
	f.setupReq = r
	return map[string]any{"saved": true}, nil
}
func (f *fakeSvc) TestByID(_ context.Context, id string) (any, error) {
	f.testID = id
	return map[string]any{"ok": true}, nil
}
func (f *fakeSvc) ActivateCredential(_ context.Context, id string) error {
	f.activateID = id
	return nil
}
func (f *fakeSvc) DeleteByID(_ context.Context, id string) error {
	f.deleteCalls++
	f.deleteID = id
	return nil
}
func (f *fakeSvc) Describe(_ context.Context, id string) (string, error) { return "name-" + id, nil }
func (f *fakeSvc) SavedOptions(_ context.Context, only bool) ([]engine.Option, error) {
	f.credsOnly = append(f.credsOnly, only)
	return f.saved, nil
}

func nodeOf(t *testing.T, svc Service, typ string) engine.Node {
	t.Helper()
	for _, n := range Nodes(svc) {
		if n.Spec().Type == typ {
			return n
		}
	}
	t.Fatalf("node %s not found", typ)
	return nil
}

func run(t *testing.T, svc Service, typ string, in map[string]any) (any, error) {
	t.Helper()
	b, _ := json.Marshal(in)
	return nodeOf(t, svc, typ).Run(context.Background(), b)
}

func TestSpecsValid(t *testing.T) {
	nodes := Nodes(&fakeSvc{})
	if len(nodes) != 8 {
		t.Fatalf("nodes = %d, want 8", len(nodes))
	}
	for _, n := range nodes {
		if err := engine.ValidateSpec(n.Spec()); err != nil {
			t.Errorf("%s: %v", n.Spec().Type, err)
		}
	}
}

func TestSetupParamsDeriveFromRegistry(t *testing.T) {
	spec := nodeOf(t, &fakeSvc{}, NodeSetup).Spec()
	byName := map[string][]engine.Param{}
	for _, p := range spec.Params {
		byName[p.Name] = append(byName[p.Name], p)
	}
	prov := byName["provider"]
	if len(prov) != 1 || prov[0].Kind != engine.ParamSelect {
		t.Fatalf("provider param = %+v", prov)
	}
	want := []string{Auto}
	for _, m := range domain.Registry() {
		want = append(want, m.ID)
	}
	if !slices.Equal(prov[0].Options, want) {
		t.Errorf("provider options = %v, want %v", prov[0].Options, want)
	}
	for _, m := range domain.Registry() {
		for _, f := range m.Fields {
			if len(byName[f.Key]) != 1 {
				t.Errorf("field %q appears %d times, want 1", f.Key, len(byName[f.Key]))
				continue
			}
			if byName[f.Key][0].Secret != f.Secret {
				t.Errorf("field %q Secret = %v, registry says %v", f.Key, byName[f.Key][0].Secret, f.Secret)
			}
		}
	}
	for _, k := range []string{"api_key", "access_key", "secret_key", "credentials_file"} {
		ps := byName[k]
		if len(ps) != 1 || !ps[0].Secret {
			t.Errorf("%s must be a Secret param: %+v", k, ps)
		}
	}
	for _, k := range []string{"gcs_bucket", "wan3_base_url"} {
		ps := byName[k]
		if len(ps) != 1 || ps[0].Secret {
			t.Errorf("%s must exist and not be Secret: %+v", k, ps)
		}
	}
	for _, p := range spec.Params {
		if p.Secret && p.Kind != engine.ParamText {
			t.Errorf("secret param %s not text", p.Name)
		}
	}
}

func TestDeleteConfirmIsDestructiveToggle(t *testing.T) {
	var found bool
	for _, p := range nodeOf(t, &fakeSvc{}, NodeDelete).Spec().Params {
		if p.Name == "confirm" {
			found = true
			if p.Kind != engine.ParamToggle || !p.Destructive || p.Default != "false" {
				t.Errorf("confirm = %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("no confirm param")
	}
}

func TestSetupMapsInput(t *testing.T) {
	f := &fakeSvc{}
	_, err := run(t, f, NodeSetup, map[string]any{
		"provider": "auto", "protocol": "auto", "api_key": " sk-123 ", "base_url": "https://x.test/v1",
		"name": "mine", "access_key": "ak", "secret_key": "sk", "gcs_bucket": "bkt",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := f.setupReq
	if r.ProviderID != "" || r.DiscoveryFormat != "" || r.APIKey != "sk-123" || r.BaseURL != "https://x.test/v1" || r.Name != "mine" {
		t.Errorf("req = %+v", r)
	}
	if r.Secrets["access_key"] != "ak" || r.Secrets["secret_key"] != "sk" || len(r.Secrets) != 2 {
		t.Errorf("secrets = %v", r.Secrets)
	}
	if r.Values["gcs_bucket"] != "bkt" || len(r.Values) != 1 {
		t.Errorf("values = %v", r.Values)
	}

	f = &fakeSvc{}
	formats := domain.DiscoveryFormats()
	if _, err := run(t, f, NodeSetup, map[string]any{"provider": domain.IDOpenAI, "protocol": string(formats[0]), "api_key": "k"}); err != nil {
		t.Fatal(err)
	}
	if f.setupReq.ProviderID != domain.IDOpenAI || f.setupReq.DiscoveryFormat != formats[0] {
		t.Errorf("req = %+v", f.setupReq)
	}
}

func TestSetupRequiresKey(t *testing.T) {
	f := &fakeSvc{}
	if _, err := run(t, f, NodeSetup, map[string]any{"provider": "auto", "base_url": "https://x"}); err == nil {
		t.Fatal("expected validation error")
	}
	if f.setupCalls != 0 {
		t.Fatal("Setup must not be called")
	}
	// secrets alone are enough (Kling)
	if _, err := run(t, f, NodeSetup, map[string]any{"access_key": "a", "secret_key": "b"}); err != nil {
		t.Fatal(err)
	}
}

func TestIDRequired(t *testing.T) {
	f := &fakeSvc{}
	for _, typ := range []string{NodeTest, NodeActivate, NodeDelete} {
		if _, err := run(t, f, typ, map[string]any{"confirm": true}); err == nil {
			t.Errorf("%s: expected error without id", typ)
		}
	}
	if f.deleteCalls != 0 || f.testID != "" || f.activateID != "" {
		t.Fatal("service must not be called")
	}
	if _, err := run(t, f, NodeTest, map[string]any{"id": "c1"}); err != nil || f.testID != "c1" {
		t.Errorf("test: %v %q", err, f.testID)
	}
	out, err := run(t, f, NodeActivate, map[string]any{"id": "c2"})
	if err != nil || f.activateID != "c2" {
		t.Errorf("activate: %v %q", err, f.activateID)
	}
	if m, _ := out.(map[string]any); m["active"] != "name-c2" {
		t.Errorf("activate out = %v", out)
	}
}

func TestDeleteNeedsConfirm(t *testing.T) {
	f := &fakeSvc{}
	if _, err := run(t, f, NodeDelete, map[string]any{"id": "c1"}); err == nil {
		t.Fatal("expected error without confirm")
	}
	if _, err := run(t, f, NodeDelete, map[string]any{"id": "c1", "confirm": false}); err == nil {
		t.Fatal("expected error with confirm=false")
	}
	if f.deleteCalls != 0 {
		t.Fatal("DeleteByID must not be called")
	}
	if _, err := run(t, f, NodeDelete, map[string]any{"id": "c1", "confirm": true}); err != nil {
		t.Fatal(err)
	}
	if f.deleteCalls != 1 || f.deleteID != "c1" {
		t.Errorf("calls=%d id=%q", f.deleteCalls, f.deleteID)
	}
}

func TestOptionSources(t *testing.T) {
	f := &fakeSvc{saved: []engine.Option{{Value: "a", Label: "A"}}}
	src := OptionSources(f)
	for _, k := range []string{OptionsSaved, OptionsCredentials} {
		got, err := src[k](context.Background(), nil)
		if err != nil || len(got) != 1 || got[0].Value != "a" {
			t.Errorf("%s: %v %v", k, got, err)
		}
	}
	got, _ := OptionSources(&fakeSvc{})[OptionsCredentials](context.Background(), nil)
	_ = got
	g := &fakeSvc{}
	_, _ = OptionSources(g)[OptionsSaved](context.Background(), nil)
	_, _ = OptionSources(g)[OptionsCredentials](context.Background(), nil)
	if !slices.Equal(g.credsOnly, []bool{false, true}) {
		t.Errorf("credentialsOnly flags = %v", g.credsOnly)
	}
}

func TestNewNodeSpecs(t *testing.T) {
	f := &fakeSvc{}
	for _, typ := range []string{NodeDiscoverModels, NodeSetDefault, NodeSettings} {
		if err := engine.ValidateSpec(nodeOf(t, f, typ).Spec()); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	// every setting spec has an integer param with the same bounds
	params := map[string]engine.Param{}
	for _, p := range nodeOf(t, f, NodeSettings).Spec().Params {
		params[p.Name] = p
	}
	for _, sp := range domain.SettingSpecs() {
		p, ok := params[sp.Key]
		if !ok || p.Kind != engine.ParamInteger || p.Min != sp.Min || p.Max != sp.Max {
			t.Errorf("setting %s: %+v", sp.Key, p)
		}
	}
	if params["id"].OptionsFrom != OptionsOwners || params["reset"].Name == "" {
		t.Errorf("params = %+v", params)
	}
	// set_default offers every purpose
	for _, p := range nodeOf(t, f, NodeSetDefault).Spec().Params {
		if p.Name == "purpose" {
			var want []string
			for _, pu := range domain.Purposes() {
				want = append(want, string(pu))
			}
			if !slices.Equal(p.Options, want) {
				t.Errorf("purposes = %v", p.Options)
			}
		}
		if p.Name == "model" && p.OptionsFrom != OptionsModels {
			t.Errorf("model options = %q", p.OptionsFrom)
		}
	}
}

func TestDiscoverModelsNode(t *testing.T) {
	f := &fakeSvc{}
	if _, err := run(t, f, NodeDiscoverModels, map[string]any{}); err == nil {
		t.Fatal("id is required")
	}
	if _, err := run(t, f, NodeDiscoverModels, map[string]any{"id": " openai "}); err != nil || f.discoverID != "openai" {
		t.Fatalf("%v %q", err, f.discoverID)
	}
}

func TestSettingsNodeMapsOnlyGivenKeys(t *testing.T) {
	f := &fakeSvc{}
	out, err := run(t, f, NodeSettings, map[string]any{"id": "agnes", "video_max_workers": 3, "image_rpm": 0})
	if err != nil {
		t.Fatal(err)
	}
	if f.settingsID != "agnes" || len(f.settingsRaw) != 2 || f.settingsRaw["video_max_workers"] != "3" || f.settingsRaw["image_rpm"] != "0" {
		t.Fatalf("%q %v", f.settingsID, f.settingsRaw)
	}
	if m, _ := out.(map[string]any); m["ok"] != true || m["settings"] == nil {
		t.Fatalf("out = %v", out)
	}

	// reset list -> empty values, mixed with a set
	f = &fakeSvc{}
	if _, err := run(t, f, NodeSettings, map[string]any{"id": "x", "image_max_workers": 2, "reset": " video_rpm , request_gap_ms,, "}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"image_max_workers": "2", "video_rpm": "", "request_gap_ms": ""}
	if len(f.settingsRaw) != len(want) {
		t.Fatalf("%v", f.settingsRaw)
	}
	for k, v := range want {
		if got, ok := f.settingsRaw[k]; !ok || got != v {
			t.Errorf("%s = %q (%v)", k, got, ok)
		}
	}

	// nothing to change / no id: the service is not called
	f = &fakeSvc{}
	if _, err := run(t, f, NodeSettings, map[string]any{"id": "x"}); err == nil {
		t.Fatal("nothing given must fail")
	}
	if _, err := run(t, f, NodeSettings, map[string]any{"id": "x", "reset": " , "}); err == nil {
		t.Fatal("blank reset must fail")
	}
	if _, err := run(t, f, NodeSettings, map[string]any{"image_rpm": 1}); err == nil {
		t.Fatal("id is required")
	}
	if f.settingsCalls != 0 {
		t.Fatal("service must not be called")
	}
}

func TestSetDefaultNode(t *testing.T) {
	f := &fakeSvc{}
	if _, err := run(t, f, NodeSetDefault, map[string]any{"model": "openai/gpt-4o"}); err == nil {
		t.Fatal("purpose is required")
	}
	if f.defaultCalls != 0 {
		t.Fatal("service must not be called")
	}
	if _, err := run(t, f, NodeSetDefault, map[string]any{"purpose": "image", "model": " openai/gpt-image-1 "}); err != nil {
		t.Fatal(err)
	}
	if f.defaultPurp != domain.PurposeImage || f.defaultRef != "openai/gpt-image-1" {
		t.Fatalf("%q %q", f.defaultPurp, f.defaultRef)
	}
	// empty model = auto
	if _, err := run(t, f, NodeSetDefault, map[string]any{"purpose": "text"}); err != nil || f.defaultRef != "" || f.defaultPurp != domain.PurposeText {
		t.Fatalf("%v %q %q", err, f.defaultPurp, f.defaultRef)
	}
}

func TestOwnerAndModelOptionSources(t *testing.T) {
	f := &fakeSvc{}
	src := OptionSources(f)
	got, err := src[OptionsOwners](context.Background(), nil)
	if err != nil || len(got) != 1 || got[0].Value != "openai" {
		t.Fatalf("%v %v", got, err)
	}
	got, err = src[OptionsModels](context.Background(), map[string]string{"purpose": "video"})
	if err != nil || len(got) != 1 || f.modelPurpose != domain.PurposeVideo {
		t.Fatalf("%v %v %q", got, err, f.modelPurpose)
	}
	// no purpose selected yet: every purpose's models are offered
	if _, err := src[OptionsModels](context.Background(), nil); err != nil || f.modelPurpose != "" {
		t.Fatalf("%v %q", err, f.modelPurpose)
	}
}
