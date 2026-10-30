package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"app/internal/apperr"
)

// Graph is a set of node instances wired output port -> input port.
type Graph struct {
	RequestID string      `json:"requestId,omitempty"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []Edge      `json:"edges"`
}

// GraphNode is one instance of a node type.
type GraphNode struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Endpoint is one port of one instance.
type Endpoint struct {
	Node string `json:"node"`
	Port string `json:"port"`
}

// Edge hands an output port's value to an input port.
type Edge struct {
	From Endpoint `json:"from"`
	To   Endpoint `json:"to"`
}

// RunStatus is how one instance ended.
type RunStatus string

const (
	RunDone      RunStatus = "done"
	RunFailed    RunStatus = "failed"
	RunPending   RunStatus = "pending"
	RunSkipped   RunStatus = "skipped"
	RunCancelled RunStatus = "cancelled"
)

// NodeRun is one instance's outcome.
type NodeRun struct {
	Status RunStatus       `json:"status"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   apperr.Code     `json:"code,omitempty"`
	JobIDs []string        `json:"jobIds,omitempty"`
}

// GraphResult holds every instance's outcome, in run order.
type GraphResult struct {
	Order []string           `json:"order"`
	Nodes map[string]NodeRun `json:"nodes"`
}

// Done reports whether every instance finished.
func (r GraphResult) Done() bool {
	for _, run := range r.Nodes {
		if run.Status != RunDone {
			return false
		}
	}
	return true
}

type plannedNode struct {
	GraphNode
	node   Node
	params map[string]json.RawMessage
	inputs map[string][]Edge
}

// RunGraph checks a graph and runs it one node at a time in dependency order.
func (r *Registry) RunGraph(ctx context.Context, g Graph) (GraphResult, error) {
	plan, order, err := r.plan(g)
	if err != nil {
		return GraphResult{}, err
	}
	res := GraphResult{Order: order, Nodes: make(map[string]NodeRun, len(order))}
	outputs := make(map[string]map[string]json.RawMessage, len(order))
	for _, id := range order {
		p := plan[id]
		if ctx.Err() != nil {
			res.Nodes[id] = NodeRun{Status: RunCancelled, Error: "Đã dừng trước khi chạy."}
			continue
		}
		if blocked := r.blockedBy(p, res.Nodes); blocked != "" {
			res.Nodes[id] = NodeRun{Status: RunSkipped, Error: fmt.Sprintf("Bỏ qua vì node %s chưa có kết quả.", blocked)}
			continue
		}
		input, err := assemble(p, outputs)
		if err != nil {
			res.Nodes[id] = NodeRun{Status: RunFailed, Error: err.Error(), Code: apperr.CodeValidation}
			continue
		}
		nodeCtx := ctx
		if g.RequestID != "" {
			nodeCtx = WithRunKey(ctx, g.RequestID+"/"+id)
		}
		run, out := runNode(nodeCtx, p, input)
		res.Nodes[id] = run
		if out != nil {
			outputs[id] = out
		}
	}
	return res, nil
}

func (r *Registry) blockedBy(p plannedNode, done map[string]NodeRun) string {
	for _, edges := range p.inputs {
		for _, e := range edges {
			if done[e.From.Node].Status != RunDone {
				return e.From.Node
			}
		}
	}
	return ""
}

func runNode(ctx context.Context, p plannedNode, input json.RawMessage) (NodeRun, map[string]json.RawMessage) {
	out, err := p.node.Run(ctx, input)
	if err != nil {
		var pending *PendingError
		switch {
		case errors.As(err, &pending):
			return NodeRun{Status: RunPending, Error: pending.Message, Code: CodeOf(err), JobIDs: pending.JobIDs}, nil
		case ctx.Err() != nil:
			return NodeRun{Status: RunCancelled, Error: err.Error()}, nil
		}
		return NodeRun{Status: RunFailed, Error: err.Error(), Code: CodeOf(err)}, nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return NodeRun{Status: RunFailed, Error: err.Error(), Code: apperr.CodeInternal}, nil
	}
	ports := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &ports); err != nil {
		return NodeRun{Status: RunFailed, Error: fmt.Sprintf("Node %s không trả kết quả theo đúng cổng: %v", p.Type, err), Code: apperr.CodeInternal}, nil
	}
	return NodeRun{Status: RunDone, Output: raw}, ports
}

func assemble(p plannedNode, outputs map[string]map[string]json.RawMessage) (json.RawMessage, error) {
	input := make(map[string]json.RawMessage, len(p.params)+len(p.inputs))
	for k, v := range p.params {
		input[k] = v
	}
	spec := p.node.Spec()
	for port, edges := range p.inputs {
		in, _ := findPort(spec.Inputs, port)
		var values []json.RawMessage
		for _, e := range edges {
			raw, ok := outputs[e.From.Node][e.From.Port]
			if !ok || string(raw) == "null" {
				continue
			}
			var list []json.RawMessage
			if json.Unmarshal(raw, &list) == nil {
				values = append(values, list...)
			} else {
				values = append(values, raw)
			}
		}
		if err := checkMedia(in, values); err != nil {
			return nil, err
		}
		switch {
		case in.Multiple:
			list, _ := json.Marshal(values)
			input[port] = list
		case len(values) == 1:
			input[port] = values[0]
		case len(values) == 0 && in.Optional:
		case len(values) == 0:
			return nil, fmt.Errorf("Cổng %s của node %s không nhận được giá trị nào.", port, p.ID)
		default:
			return nil, fmt.Errorf("Cổng %s của node %s chỉ nhận một giá trị nhưng có %d.", port, p.ID, len(values))
		}
	}
	return json.Marshal(input)
}

func checkMedia(in Port, values []json.RawMessage) error {
	if in.Type == PortText {
		return nil
	}
	for _, v := range values {
		var ref MediaRef
		if err := json.Unmarshal(v, &ref); err != nil || ref.LocalURL == "" {
			return fmt.Errorf("Cổng %s cần tệp media.", in.Name)
		}
		if !ref.Accepts(in.Type) {
			return fmt.Errorf("Cổng %s không nhận tệp %s (%s).", in.Name, ref.Kind, ref.LocalURL)
		}
	}
	return nil
}

func findPort(ports []Port, name string) (Port, bool) {
	for _, p := range ports {
		if p.Name == name {
			return p, true
		}
	}
	return Port{}, false
}

func (r *Registry) plan(g Graph) (map[string]plannedNode, []string, error) {
	if len(g.Nodes) == 0 {
		return nil, nil, Invalid("Đồ thị chưa có node nào.")
	}
	plan := make(map[string]plannedNode, len(g.Nodes))
	ids := make([]string, 0, len(g.Nodes))
	for _, gn := range g.Nodes {
		if gn.ID == "" {
			return nil, nil, Invalid("Có node chưa có mã định danh ID.")
		}
		if _, dup := plan[gn.ID]; dup {
			return nil, nil, Invalid(fmt.Sprintf("Mã node %q bị trùng.", gn.ID))
		}
		n, ok := r.node(gn.Type)
		if !ok {
			return nil, nil, Invalid(fmt.Sprintf("Không có node %q.", gn.Type))
		}
		params := map[string]json.RawMessage{}
		if len(gn.Params) > 0 && string(gn.Params) != "null" {
			if err := json.Unmarshal(gn.Params, &params); err != nil {
				return nil, nil, Invalid(fmt.Sprintf("Cài đặt của node %s không hợp lệ: %v", gn.ID, err))
			}
		}
		plan[gn.ID] = plannedNode{GraphNode: gn, node: n, params: params, inputs: map[string][]Edge{}}
		ids = append(ids, gn.ID)
	}
	next := map[string][]string{}
	indegree := map[string]int{}
	for _, e := range g.Edges {
		from, okFrom := plan[e.From.Node]
		to, okTo := plan[e.To.Node]
		if !okFrom || !okTo {
			return nil, nil, Invalid(fmt.Sprintf("Dây nối %s -> %s chỉ tới node không tồn tại.", e.From.Node, e.To.Node))
		}
		out, ok := findPort(from.node.Spec().Outputs, e.From.Port)
		if !ok {
			return nil, nil, Invalid(fmt.Sprintf("Node %s không có cổng ra %q.", e.From.Node, e.From.Port))
		}
		in, ok := findPort(to.node.Spec().Inputs, e.To.Port)
		if !ok {
			return nil, nil, Invalid(fmt.Sprintf("Node %s không có cổng vào %q.", e.To.Node, e.To.Port))
		}
		if !Compatible(out.Type, in.Type) {
			return nil, nil, Invalid(fmt.Sprintf("Không nối được %s.%s (%s) vào %s.%s (%s).", e.From.Node, out.Name, out.Type, e.To.Node, in.Name, in.Type))
		}
		if !in.Multiple && len(to.inputs[in.Name]) > 0 {
			return nil, nil, Invalid(fmt.Sprintf("Cổng %s.%s chỉ nhận một dây nối.", e.To.Node, in.Name))
		}
		to.inputs[in.Name] = append(to.inputs[in.Name], e)
		next[e.From.Node] = append(next[e.From.Node], e.To.Node)
		indegree[e.To.Node]++
	}
	for _, id := range ids {
		p := plan[id]
		for _, in := range p.node.Spec().Inputs {
			_, wired := p.inputs[in.Name]
			_, set := p.params[in.Name]
			if !in.Optional && !wired && !set {
				return nil, nil, Invalid(fmt.Sprintf("Cổng %s của node %s chưa có giá trị.", in.Name, id))
			}
		}
	}

	order := make([]string, 0, len(ids))
	ready := []string{}
	for _, id := range ids {
		if indegree[id] == 0 {
			ready = append(ready, id)
		}
	}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, n := range next[id] {
			if indegree[n]--; indegree[n] == 0 {
				ready = append(ready, n)
			}
		}
	}
	if len(order) != len(ids) {
		return nil, nil, Invalid("Đồ thị có vòng lặp (cycle detected).")
	}
	return plan, order, nil
}
