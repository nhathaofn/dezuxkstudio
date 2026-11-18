package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validSpec() Spec {
	return Spec{
		Type: "tool.x", Label: "X", Description: "Does X to images.",
		Inputs:  []Port{{Name: "image", Label: "Image", Type: PortImage}},
		Outputs: []Port{{Name: "image", Label: "Result", Type: PortImage}},
		Params: []Param{
			{Name: "inplace", Label: "Overwrite", Kind: ParamToggle, Default: "false", Destructive: true},
			{Name: "workers", Label: "Workers", Kind: ParamInteger, Min: 0, Max: 64, Default: "0"},
			{Name: "corner", Label: "Corner", Kind: ParamSelect, Options: []string{"a", "b"}, Default: "a"},
			{Name: "output_dir", Label: "Out", Kind: ParamText, Role: ParamRoleOutputDir},
		},
	}
}

func TestValidateSpecAcceptsACompleteSpec(t *testing.T) {
	if err := ValidateSpec(validSpec()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSpecRejectsWhatTheAgentCannotUse(t *testing.T) {
	cases := map[string]func(*Spec){
		"description":           func(s *Spec) { s.Description = "" },
		"select needs":          func(s *Spec) { s.Params[2].Options = nil },
		"not one of":            func(s *Spec) { s.Params[2].Default = "z" },
		"outside":               func(s *Spec) { s.Params[1].Default = "99" },
		"only toggles":          func(s *Spec) { s.Params[1].Destructive = true },
		"must default to false": func(s *Spec) { s.Params[0].Default = "true" },
		"output_dir role":       func(s *Spec) { s.Params[3].Kind = ParamInteger },
		"duplicates":            func(s *Spec) { s.Params[3].Name = "image" },
		"unknown type":          func(s *Spec) { s.Inputs[0].Type = "binary" },
	}
	for want, mutate := range cases {
		s := validSpec()
		s.Params = append([]Param{}, s.Params...)
		s.Inputs = append([]Port{}, s.Inputs...)
		mutate(&s)
		err := ValidateSpec(s)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestEngineSubstrateHardening(t *testing.T) {
	// nil ctx CheckFile returns nil safely
	if err := CheckFile(nil, "anything", true); err != nil { //nolint:staticcheck
		t.Fatalf("CheckFile(nil) got %v", err)
	}

	// RefFor ignores dots inside directory segments
	ref := RefFor(`D:\v1.0\img\clip`, MediaVideo)
	if ref.Kind != MediaVideo || ref.MimeType != "" {
		t.Fatalf("unexpected RefFor on extensionless dotted dir: %+v", ref)
	}

	// Registry.Run enforces Spec.Params bounds & options
	reg := NewRegistry()
	var ran bool
	n := Typed(validSpec(), func(_ context.Context, _ struct{}) (struct{}, error) {
		ran = true
		return struct{}{}, nil
	})
	if err := reg.Register(n); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Run(context.Background(), "tool.x", json.RawMessage(`{"workers":999}`)); err == nil {
		t.Fatal("expected out-of-range workers error")
	}
	if _, err := reg.Run(context.Background(), "tool.x", json.RawMessage(`{"corner":"invalid"}`)); err == nil {
		t.Fatal("expected invalid select option error")
	}
	if _, err := reg.Run(context.Background(), "tool.x", json.RawMessage(`{"workers":4,"corner":"b"}`)); err != nil || !ran {
		t.Fatalf("expected valid input to execute node, ran=%v err=%v", ran, err)
	}

	// RunGraph returns promptly on context cancel even if worker refuses to exit immediately
	release := make(chan struct{})
	defer close(release)
	slowReg := NewRegistry()
	_ = slowReg.Register(Typed(Spec{
		Type: "tool.slow", Label: "Slow", Description: "Blocks until released.",
	}, func(_ context.Context, _ struct{}) (struct{}, error) {
		<-release
		return struct{}{}, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := slowReg.RunGraph(ctx, Graph{
		Nodes: []GraphNode{{ID: "n1", Type: "tool.slow"}},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("RunGraph blocked on slow worker for %v", time.Since(start))
	}
}
