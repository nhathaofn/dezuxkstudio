// Package engine provides DezuxkStudio's modular node execution runtime.
// It supports standalone step execution and directed acyclic graph (DAG) pipelines.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
)

// PortType is the kind of value travelling between nodes.
type PortType string

const (
	PortText  PortType = "text"
	PortImage PortType = "image"
	PortVideo PortType = "video"
	PortMedia PortType = "media" // Accepts image or video
)

// AllPortTypes is bound to the frontend as a TypeScript enum.
var AllPortTypes = []struct {
	Value  PortType
	TSName string
}{{PortText, "Text"}, {PortImage, "Image"}, {PortVideo, "Video"}, {PortMedia, "Media"}}

// Port is one input or output of a node.
type Port struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     PortType `json:"type"`
	Multiple bool     `json:"multiple"`
	Optional bool     `json:"optional"`
}

// ParamKind is how a node setting is chosen.
type ParamKind string

const (
	ParamSelect  ParamKind = "select"
	ParamInteger ParamKind = "integer"
	ParamText    ParamKind = "text"
	ParamToggle  ParamKind = "toggle"
)

// AllParamKinds is bound to the frontend as a TypeScript enum.
var AllParamKinds = []struct {
	Value  ParamKind
	TSName string
}{{ParamSelect, "Select"}, {ParamInteger, "Integer"}, {ParamText, "Text"}, {ParamToggle, "Toggle"}}

// Param is a node setting.
type Param struct {
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Kind        ParamKind `json:"kind"`
	OptionsFrom string    `json:"optionsFrom,omitempty"`
	Min         int       `json:"min,omitempty"`
	Max         int       `json:"max,omitempty"`
}

// Spec describes a node type: identity, ports and settings.
type Spec struct {
	Type        string  `json:"type"`
	Provider    string  `json:"provider"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Inputs      []Port  `json:"inputs"`
	Outputs     []Port  `json:"outputs"`
	Params      []Param `json:"params"`
}

// Node runs one unit of work.
type Node interface {
	Spec() Spec
	Run(ctx context.Context, input json.RawMessage) (any, error)
}

// Typed adapts a function with a typed request and result into a Node.
func Typed[In, Out any](spec Spec, run func(context.Context, In) (Out, error)) Node {
	return typedNode[In, Out]{spec: spec, run: run}
}

type typedNode[In, Out any] struct {
	spec Spec
	run  func(context.Context, In) (Out, error)
}

func (n typedNode[In, Out]) Spec() Spec { return n.spec }

func (n typedNode[In, Out]) Run(ctx context.Context, input json.RawMessage) (any, error) {
	var in In
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, Invalid(fmt.Sprintf("Dữ liệu đầu vào của node %s không hợp lệ: %v", n.spec.Type, err))
		}
	}
	return n.run(ctx, in)
}
