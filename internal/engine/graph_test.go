package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestGraphParallelDiamondAndTiming(t *testing.T) {
	reg := NewRegistry()
	var concurrent, maxConcurrent atomic.Int32
	trackParallel := func(typeName string) Node {
		return Typed(Spec{
			Type:    typeName,
			Inputs:  []Port{{Name: "text", Type: PortText}},
			Outputs: []Port{{Name: "images", Type: PortImage, Multiple: true}},
		}, func(ctx context.Context, in textsIn) (imagesOut, error) {
			cur := concurrent.Add(1)
			for {
				prev := maxConcurrent.Load()
				if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
					break
				}
			}
			defer concurrent.Add(-1)
			select {
			case <-time.After(60 * time.Millisecond):
			case <-ctx.Done():
				return imagesOut{}, ctx.Err()
			}
			return imagesOut{Images: []MediaRef{RefFor("/media/"+in.Text[0]+".png", MediaImage)}}, nil
		})
	}
	_ = reg.Register(
		Typed(Spec{
			Type:    "root",
			Inputs:  []Port{{Name: "seed", Type: PortText}},
			Outputs: []Port{{Name: "text", Type: PortText}},
		}, func(_ context.Context, in map[string]string) (map[string]string, error) {
			return map[string]string{"text": in["seed"]}, nil
		}),
		trackParallel("branch"),
		Typed(Spec{
			Type:    "join",
			Inputs:  []Port{{Name: "media", Type: PortImage, Multiple: true}},
			Outputs: []Port{{Name: "count", Type: PortText}},
		}, func(_ context.Context, in mediaIn) (countOut, error) {
			return countOut{Count: len(in.Media)}, nil
		}),
	)

	g := Graph{
		Nodes: []GraphNode{
			{ID: "r", Type: "root", Params: json.RawMessage(`{"seed":"x"}`)},
			{ID: "b1", Type: "branch"},
			{ID: "b2", Type: "branch"},
			{ID: "j", Type: "join"},
		},
		Edges: []Edge{
			{From: Endpoint{Node: "r", Port: "text"}, To: Endpoint{Node: "b1", Port: "text"}},
			{From: Endpoint{Node: "r", Port: "text"}, To: Endpoint{Node: "b2", Port: "text"}},
			{From: Endpoint{Node: "b1", Port: "images"}, To: Endpoint{Node: "j", Port: "media"}},
			{From: Endpoint{Node: "b2", Port: "images"}, To: Endpoint{Node: "j", Port: "media"}},
		},
	}

	res, err := reg.RunGraph(context.Background(), g)
	if err != nil {
		t.Fatalf("RunGraph: %v", err)
	}
	if !res.Done() {
		t.Fatalf("expected all nodes done, got %+v", res)
	}
	if maxConcurrent.Load() < 2 {
		t.Fatalf("expected independent branches b1/b2 to execute in parallel, maxConcurrent=%d", maxConcurrent.Load())
	}
	if len(res.Order) != 4 || res.Order[0] != "r" || res.Order[3] != "j" {
		t.Fatalf("unexpected topological completion order: %v", res.Order)
	}
	if !strings.Contains(string(res.Nodes["j"].Output), `"count":2`) {
		t.Fatalf("join output = %s, want count:2", res.Nodes["j"].Output)
	}
}

func TestGraphUpstreamFailureSkipsAndCancelPropagates(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(
		Typed(Spec{
			Type:    "fail",
			Outputs: []Port{{Name: "out", Type: PortText}},
		}, func(context.Context, map[string]any) (map[string]string, error) {
			return nil, errors.New("boom")
		}),
		Typed(Spec{
			Type:    "slow",
			Outputs: []Port{{Name: "out", Type: PortText}},
		}, func(ctx context.Context, _ map[string]any) (map[string]string, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		Typed(Spec{
			Type:   "sink",
			Inputs: []Port{{Name: "in", Type: PortText}},
		}, func(context.Context, map[string]any) (map[string]string, error) {
			return map[string]string{}, nil
		}),
	)

	// Branch 1 fails -> downstream skipped
	res, err := reg.RunGraph(context.Background(), Graph{
		Nodes: []GraphNode{{ID: "f", Type: "fail"}, {ID: "s", Type: "sink"}},
		Edges: []Edge{{From: Endpoint{Node: "f", Port: "out"}, To: Endpoint{Node: "s", Port: "in"}}},
	})
	if err != nil || res.Nodes["f"].Status != RunFailed || res.Nodes["s"].Status != RunSkipped {
		t.Fatalf("unexpected failure propagation: err=%v nodes=%+v", err, res.Nodes)
	}

	// Cancel propagates to running node and downstream unblocked node
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	res, err = reg.RunGraph(ctx, Graph{
		Nodes: []GraphNode{{ID: "w", Type: "slow"}, {ID: "d", Type: "sink"}},
		Edges: []Edge{{From: Endpoint{Node: "w", Port: "out"}, To: Endpoint{Node: "d", Port: "in"}}},
	})
	if err != nil || res.Nodes["w"].Status != RunCancelled || res.Nodes["d"].Status != RunCancelled {
		t.Fatalf("unexpected cancellation propagation: err=%v nodes=%+v", err, res.Nodes)
	}
}

func TestAssemblePreservesSinglePortTextArrayAndOneLevelNesting(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(
		Typed(Spec{
			Type:    "emitArray",
			Outputs: []Port{{Name: "tags", Type: PortText, Multiple: true}},
		}, func(context.Context, map[string]any) (map[string]any, error) {
			return map[string]any{"tags": []string{"alpha", "beta"}}, nil
		}),
		Typed(Spec{
			Type:    "acceptSingleTextArray",
			Inputs:  []Port{{Name: "text", Type: PortText, Multiple: false}},
			Outputs: []Port{{Name: "joined", Type: PortText}},
		}, func(_ context.Context, in textsIn) (map[string]string, error) {
			return map[string]string{"joined": strings.Join(in.Text, "|")}, nil
		}),
	)

	res, err := reg.RunGraph(context.Background(), Graph{
		Nodes: []GraphNode{
			{ID: "src", Type: "emitArray"},
			{ID: "dst", Type: "acceptSingleTextArray"},
		},
		Edges: []Edge{
			{From: Endpoint{Node: "src", Port: "tags"}, To: Endpoint{Node: "dst", Port: "text"}},
		},
	})
	if err != nil || !res.Done() {
		t.Fatalf("expected single PortText input to accept JSON array without error: err=%v res=%+v", err, res)
	}
	if !strings.Contains(string(res.Nodes["dst"].Output), `"alpha|beta"`) {
		t.Fatalf("dst output = %s", res.Nodes["dst"].Output)
	}
}
