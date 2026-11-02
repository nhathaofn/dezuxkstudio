package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"app/internal/apperr"
)

type textsIn struct {
	Text Texts `json:"text"`
}

type imagesOut struct {
	Images []MediaRef `json:"images"`
}

type mediaIn struct {
	Media []MediaRef `json:"media"`
}

type countOut struct {
	Count int `json:"count"`
}

func makeImages(t string, calls *[]string) Node {
	return Typed(Spec{
		Type:    t,
		Inputs:  []Port{{Name: "text", Type: PortText, Multiple: true}},
		Outputs: []Port{{Name: "images", Type: PortImage, Multiple: true}},
	}, func(_ context.Context, in textsIn) (imagesOut, error) {
		*calls = append(*calls, t)
		out := imagesOut{}
		for _, s := range in.Text.NonEmpty() {
			out.Images = append(out.Images, RefFor("/media/"+s+".png", MediaImage))
		}
		return out, nil
	})
}

func countMedia(t string, port PortType, calls *[]string) Node {
	return Typed(Spec{
		Type:    t,
		Inputs:  []Port{{Name: "media", Type: port, Multiple: true}},
		Outputs: []Port{{Name: "count", Type: PortText}},
	}, func(_ context.Context, in mediaIn) (countOut, error) {
		*calls = append(*calls, t)
		return countOut{Count: len(in.Media)}, nil
	})
}

func TestCompatible(t *testing.T) {
	cases := []struct {
		from, to PortType
		want     bool
	}{
		{PortImage, PortImage, true},
		{PortImage, PortMedia, true},
		{PortVideo, PortMedia, true},
		{PortMedia, PortImage, false},
		{PortImage, PortVideo, false},
		{PortText, PortImage, false},
	}
	for _, c := range cases {
		if got := Compatible(c.from, c.to); got != c.want {
			t.Errorf("Compatible(%v, %v) = %v; want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestGraphRunOrderAndCycleDetection(t *testing.T) {
	calls := []string{}
	reg := NewRegistry()
	_ = reg.Register(
		makeImages("gen", &calls),
		countMedia("count", PortImage, &calls),
	)

	// Valid linear pipeline: gen -> count
	validG := Graph{
		Nodes: []GraphNode{
			{ID: "n2", Type: "count"},
			{ID: "n1", Type: "gen", Params: json.RawMessage(`{"text": "cat"}`)},
		},
		Edges: []Edge{
			{From: Endpoint{Node: "n1", Port: "images"}, To: Endpoint{Node: "n2", Port: "media"}},
		},
	}
	res, err := reg.RunGraph(context.Background(), validG)
	if err != nil {
		t.Fatalf("unexpected graph error: %v", err)
	}
	if !res.Done() {
		t.Fatalf("graph not done: %+v", res)
	}
	if len(res.Order) != 2 || res.Order[0] != "n1" || res.Order[1] != "n2" {
		t.Fatalf("unexpected order: %v", res.Order)
	}

	// Cycle detection with compatible types
	regCycle := NewRegistry()
	_ = regCycle.Register(
		Typed(Spec{
			Type:    "relay",
			Inputs:  []Port{{Name: "in", Type: PortImage}},
			Outputs: []Port{{Name: "out", Type: PortImage}},
		}, func(_ context.Context, in map[string]any) (map[string]any, error) {
			return in, nil
		}),
	)
	cycleValidTypes := Graph{
		Nodes: []GraphNode{
			{ID: "a", Type: "relay"},
			{ID: "b", Type: "relay"},
		},
		Edges: []Edge{
			{From: Endpoint{Node: "a", Port: "out"}, To: Endpoint{Node: "b", Port: "in"}},
			{From: Endpoint{Node: "b", Port: "out"}, To: Endpoint{Node: "a", Port: "in"}},
		},
	}
	_, err = regCycle.RunGraph(context.Background(), cycleValidTypes)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}

func TestGraphValidationErrorsReturnCodeValidation(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(
		Typed(Spec{
			Type:    "nodeA",
			Inputs:  []Port{{Name: "in", Type: PortImage, Optional: true}},
			Outputs: []Port{{Name: "out", Type: PortImage}},
		}, func(_ context.Context, in map[string]any) (map[string]any, error) {
			return in, nil
		}),
	)

	// 1. Cycle detection
	cycleGraph := Graph{
		Nodes: []GraphNode{
			{ID: "n1", Type: "nodeA"},
			{ID: "n2", Type: "nodeA"},
		},
		Edges: []Edge{
			{From: Endpoint{Node: "n1", Port: "out"}, To: Endpoint{Node: "n2", Port: "in"}},
			{From: Endpoint{Node: "n2", Port: "out"}, To: Endpoint{Node: "n1", Port: "in"}},
		},
	}
	_, err := reg.RunGraph(context.Background(), cycleGraph)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected CodeValidation for cycle error, got: %v", CodeOf(err))
	}
	if CodeOf(err) != apperr.CodeValidation {
		t.Fatalf("expected CodeOf to return CodeValidation, got: %v", CodeOf(err))
	}

	// 2. Unknown node type
	unknownGraph := Graph{
		Nodes: []GraphNode{
			{ID: "n1", Type: "unknown_type"},
		},
	}
	_, err = reg.RunGraph(context.Background(), unknownGraph)
	if err == nil {
		t.Fatal("expected unknown node type error")
	}
	if !apperr.IsCode(err, apperr.CodeValidation) || CodeOf(err) != apperr.CodeValidation {
		t.Fatalf("expected CodeValidation for unknown node, got: %v", CodeOf(err))
	}

	// 3. Missing / non-existent port
	missingPortGraph := Graph{
		Nodes: []GraphNode{
			{ID: "n1", Type: "nodeA"},
			{ID: "n2", Type: "nodeA"},
		},
		Edges: []Edge{
			{From: Endpoint{Node: "n1", Port: "non_existent_port"}, To: Endpoint{Node: "n2", Port: "in"}},
		},
	}
	_, err = reg.RunGraph(context.Background(), missingPortGraph)
	if err == nil {
		t.Fatal("expected missing port error")
	}
	if !apperr.IsCode(err, apperr.CodeValidation) || CodeOf(err) != apperr.CodeValidation {
		t.Fatalf("expected CodeValidation for missing port, got: %v", CodeOf(err))
	}
}
