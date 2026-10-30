package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
	if err == nil || !strings.Contains(err.Error(), "vòng lặp") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}
