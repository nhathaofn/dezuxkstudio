package modules

import (
	"context"

	"app/internal/contracts"
	"app/internal/engine"
	"app/internal/realtime"
	"app/internal/service/workflow"
)

// Workflow exposes workflow:* actions over realtime WebSocket.
type Workflow struct {
	Service *workflow.Service
	// FileGuard, when set, is attached to every node run so nodes refuse paths the client may not touch
	// (headless server mode). Nil means the client is trusted (desktop).
	FileGuard engine.FileGuard
}

func (Workflow) Name() string { return "workflow" }

func (m Workflow) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionWorkflowSpecs,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.WorkflowSpecsResult, error) {
			specs := m.Service.Specs(ctx)
			return contracts.WorkflowSpecsResult{Specs: specs}, nil
		})

	realtime.Handle(r, contracts.ActionWorkflowRunNode,
		func(ctx context.Context, _ *realtime.Request, p contracts.WorkflowRunNodePayload) (contracts.WorkflowRunNodeResult, error) {
			out, err := m.Service.RunNode(engine.WithFileGuard(ctx, m.FileGuard), p.Type, p.Input)
			if err != nil {
				return contracts.WorkflowRunNodeResult{}, err
			}
			return contracts.WorkflowRunNodeResult{Output: out}, nil
		})

	realtime.Handle(r, contracts.ActionWorkflowRunGraph,
		func(ctx context.Context, _ *realtime.Request, p contracts.WorkflowRunGraphPayload) (contracts.WorkflowRunGraphResult, error) {
			res, err := m.Service.RunGraph(engine.WithFileGuard(ctx, m.FileGuard), p.Graph)
			if err != nil {
				return contracts.WorkflowRunGraphResult{}, err
			}
			return contracts.WorkflowRunGraphResult{Result: res}, nil
		})
}
