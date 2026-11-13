package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"app/internal/apperr"
	"app/internal/progress"
	"app/internal/safego"
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

type scheduledJob struct {
	id    string
	p     plannedNode
	input json.RawMessage
}

type finishedJob struct {
	id  string
	run NodeRun
	out map[string]json.RawMessage
}

// RunGraph checks a graph and runs independent branches concurrently in dependency order.
// Progress goes to the context's progress sink (see internal/progress), so every caller — the
// workflow screen, an agent approval, an MCP tool — gets it without passing a callback.
func (r *Registry) RunGraph(ctx context.Context, g Graph) (GraphResult, error) {
	plan, order, err := r.plan(g)
	if err != nil {
		return GraphResult{}, err
	}
	total := len(order)
	res := GraphResult{
		Order: make([]string, 0, total),
		Nodes: make(map[string]NodeRun, total),
	}
	outputs := make(map[string]map[string]json.RawMessage, total)

	remainingEdges := make(map[string]int, total)
	dependents := make(map[string][]string, total)
	for _, id := range order {
		p := plan[id]
		for _, edges := range p.inputs {
			for _, e := range edges {
				remainingEdges[id]++
				dependents[e.From.Node] = append(dependents[e.From.Node], id)
			}
		}
	}

	workers := min(total, max(4, runtime.NumCPU()))
	jobs := make(chan scheduledJob, total)
	results := make(chan finishedJob, total)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		safego.Go("engine.graph.worker", func() {
			defer wg.Done()
			for job := range jobs {
				results <- r.executeScheduledJob(ctx, g.RequestID, job)
			}
		})
	}

	completed := 0
	ready := make([]string, 0, total)
	for _, id := range order {
		if remainingEdges[id] == 0 {
			ready = append(ready, id)
		}
	}

	// Single coordinator owns outputs, res, and ready queue, eliminating cross-goroutine map races.
	drainReady := func() {
		for len(ready) > 0 {
			id := ready[0]
			ready = ready[1:]
			p := plan[id]

			finishEarly := func(run NodeRun) {
				res.Nodes[id] = run
				res.Order = append(res.Order, id)
				completed++
				reportGraphProgress(ctx, completed, total, id, "Completed node "+id, "workflow.progress.completed")
				for _, dep := range dependents[id] {
					remainingEdges[dep]--
					if remainingEdges[dep] == 0 {
						ready = append(ready, dep)
					}
				}
			}

			if ctx.Err() != nil {
				finishEarly(NodeRun{Status: RunCancelled, Error: "Cancelled before execution."})
				continue
			}
			if blocked := r.blockedBy(p, res.Nodes); blocked != "" {
				finishEarly(NodeRun{Status: RunSkipped, Error: fmt.Sprintf("Skipped because upstream node %s has no result.", blocked)})
				continue
			}
			input, err := assemble(p, outputs)
			if err != nil {
				finishEarly(NodeRun{Status: RunFailed, Error: err.Error(), Code: apperr.CodeValidation})
				continue
			}
			progress.Report(ctx, progress.Update{
				Percent: float64(completed) / float64(total) * 100.0,
				Message: fmt.Sprintf("Running node %s (%s)", id, p.GraphNode.Type),
				Key:     "workflow.progress.running",
				Params:  map[string]string{"node": id, "type": p.GraphNode.Type},
			})
			jobs <- scheduledJob{id: id, p: p, input: input}
		}
	}

	drainReady()
	for completed < total {
		fin := <-results
		res.Nodes[fin.id] = fin.run
		res.Order = append(res.Order, fin.id)
		if fin.out != nil {
			outputs[fin.id] = fin.out
		}
		completed++
		reportGraphProgress(ctx, completed, total, fin.id, "Completed node "+fin.id, "workflow.progress.completed")
		for _, dep := range dependents[fin.id] {
			remainingEdges[dep]--
			if remainingEdges[dep] == 0 {
				ready = append(ready, dep)
			}
		}
		drainReady()
	}

	close(jobs)
	wg.Wait()
	return res, nil
}

func (r *Registry) executeScheduledJob(ctx context.Context, requestID string, job scheduledJob) (fin finishedJob) {
	fin.id = job.id
	defer func() {
		if rec := recover(); rec != nil {
			fin.run = NodeRun{Status: RunFailed, Error: fmt.Sprintf("node %s panicked: %v", job.id, rec), Code: apperr.CodeInternal}
			fin.out = nil
		}
	}()
	nodeCtx := ctx
	if requestID != "" {
		nodeCtx = WithRunKey(ctx, requestID+"/"+job.id)
	}
	fin.run, fin.out = runNode(nodeCtx, job.p, job.input)
	return fin
}

func reportGraphProgress(ctx context.Context, completed, total int, id, msg, key string) {
	if total <= 0 {
		return
	}
	progress.Report(ctx, progress.Update{
		Percent: float64(completed) / float64(total) * 100.0,
		Message: msg,
		Key:     key,
		Params:  map[string]string{"node": id},
	})
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
		return NodeRun{Status: RunFailed, Error: fmt.Sprintf("Node %s did not produce expected port outputs: %v", p.Type, err), Code: apperr.CodeInternal}, nil
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
			// Only flatten a 1-level JSON array for multi-value ports or media lists (where multi-output
			// nodes always emit arrays); preserve JSON arrays verbatim for single PortText inputs.
			if in.Multiple || in.Type != PortText {
				trimmed := bytes.TrimSpace(raw)
				if len(trimmed) > 0 && trimmed[0] == '[' {
					var list []json.RawMessage
					if json.Unmarshal(trimmed, &list) == nil {
						values = append(values, list...)
						continue
					}
				}
			}
			values = append(values, raw)
		}
		if err := checkMedia(in, values); err != nil {
			return nil, err
		}
		switch {
		case in.Multiple:
			input[port] = rawArray(values)
		case len(values) == 1:
			input[port] = values[0]
		case len(values) == 0 && in.Optional:
		case len(values) == 0:
			return nil, fmt.Errorf("Port %s of node %s received no value.", port, p.ID)
		default:
			return nil, fmt.Errorf("Port %s of node %s expects a single value but received %d.", port, p.ID, len(values))
		}
	}
	return json.Marshal(input)
}

func rawArray(values []json.RawMessage) json.RawMessage {
	if len(values) == 0 {
		return json.RawMessage("[]")
	}
	total := 2 + len(values) - 1
	for _, v := range values {
		total += len(v)
	}
	buf := make([]byte, 0, total)
	buf = append(buf, '[')
	for i, v := range values {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, v...)
	}
	buf = append(buf, ']')
	return json.RawMessage(buf)
}

func checkMedia(in Port, values []json.RawMessage) error {
	if in.Type == PortText {
		return nil
	}
	for _, v := range values {
		var ref MediaRef
		if err := json.Unmarshal(v, &ref); err != nil || ref.LocalURL == "" {
			return fmt.Errorf("Port %s requires a media file.", in.Name)
		}
		if !ref.Accepts(in.Type) {
			return fmt.Errorf("Port %s does not accept %s file (%s).", in.Name, ref.Kind, ref.LocalURL)
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
		return nil, nil, Invalid("Graph does not contain any nodes.")
	}
	plan := make(map[string]plannedNode, len(g.Nodes))
	ids := make([]string, 0, len(g.Nodes))
	for _, gn := range g.Nodes {
		if gn.ID == "" {
			return nil, nil, Invalid("A graph node is missing an identifier (ID).")
		}
		if _, dup := plan[gn.ID]; dup {
			return nil, nil, Invalid(fmt.Sprintf("Duplicate node ID %q.", gn.ID))
		}
		n, ok := r.node(gn.Type)
		if !ok {
			return nil, nil, Invalid(fmt.Sprintf("Unknown node type %q.", gn.Type))
		}
		params := map[string]json.RawMessage{}
		if len(gn.Params) > 0 && string(gn.Params) != "null" {
			if err := json.Unmarshal(gn.Params, &params); err != nil {
				return nil, nil, Invalid(fmt.Sprintf("Invalid parameters for node %s: %v", gn.ID, err))
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
			return nil, nil, Invalid(fmt.Sprintf("Edge %s -> %s references a non-existent node.", e.From.Node, e.To.Node))
		}
		out, ok := findPort(from.node.Spec().Outputs, e.From.Port)
		if !ok {
			return nil, nil, Invalid(fmt.Sprintf("Node %s does not have output port %q.", e.From.Node, e.From.Port))
		}
		in, ok := findPort(to.node.Spec().Inputs, e.To.Port)
		if !ok {
			return nil, nil, Invalid(fmt.Sprintf("Node %s does not have input port %q.", e.To.Node, e.To.Port))
		}
		if !Compatible(out.Type, in.Type) {
			return nil, nil, Invalid(fmt.Sprintf("Cannot connect incompatible ports %s.%s (%s) -> %s.%s (%s).", e.From.Node, out.Name, out.Type, e.To.Node, in.Name, in.Type))
		}
		if !in.Multiple && len(to.inputs[in.Name]) > 0 {
			return nil, nil, Invalid(fmt.Sprintf("Port %s.%s only accepts a single incoming connection.", e.To.Node, in.Name))
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
				return nil, nil, Invalid(fmt.Sprintf("Required input port %s of node %s has no value.", in.Name, id))
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
		return nil, nil, Invalid("Cycle detected in graph (cycle detected).")
	}
	return plan, order, nil
}
