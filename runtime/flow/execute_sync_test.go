package flow

import (
	"context"
	"testing"
)

type ctxProbe struct{ seen *any }

func (ctxProbe) Name() string { return "probe" }
func (p ctxProbe) Execute(ctx context.Context, _ *FlowNode, _ *ExecutionContext) (*NodeResult, error) {
	*p.seen = ctx.Value(probeKey{})
	return &NodeResult{Output: map[string]any{"ok": true}}, nil
}

type probeKey struct{}

// ExecuteSync must hand the caller's context (values, cancellation) to nodes.
func TestExecuteSyncPropagatesParentContext(t *testing.T) {
	var seen any
	e := NewEngine(Config{})
	e.RegisterNode("probe", ctxProbe{seen: &seen})
	f := &Flow{Name: "p", Status: FlowStatusActive, Nodes: []FlowNode{{ID: "t", Type: NodeTypeTrigger}, {ID: "p", Type: "probe"}},
		Edges: []FlowEdge{{ID: "e", Source: "t", Target: "p"}}}
	ctx := context.WithValue(context.Background(), probeKey{}, "run-42")
	res, err := e.ExecuteSync(ctx, f, nil, nil)
	if err != nil || res.Status != "completed" || seen != "run-42" {
		t.Fatalf("status=%v err=%v seen=%v", res.Status, err, seen)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ = e.ExecuteSync(cancelled, f, nil, nil)
	if res.Status == "completed" {
		t.Fatalf("cancelled parent still completed")
	}
}
