package k8s

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func rolloutObj(spec, status map[string]any) *unstructured.Unstructured {
	if spec == nil {
		spec = map[string]any{}
	}
	if status == nil {
		status = map[string]any{}
	}
	return &unstructured.Unstructured{Object: map[string]any{"spec": spec, "status": status}}
}

func canarySteps(count int) []any {
	steps := make([]any, 0, count)
	for range count {
		steps = append(steps, map[string]any{"setWeight": int64(10)})
	}
	return steps
}

// None of these move status.phase, and an empty diff drops the update.
func TestDiffRolloutCoversMidCanaryTransitions(t *testing.T) {
	tests := []struct {
		name        string
		old         *unstructured.Unstructured
		new         *unstructured.Unstructured
		wantSummary string
	}{
		{
			name: "step advanced",
			old: rolloutObj(map[string]any{"strategy": map[string]any{"canary": map[string]any{"steps": canarySteps(4)}}},
				map[string]any{"phase": "Progressing", "currentStepIndex": int64(1)}),
			new: rolloutObj(map[string]any{"strategy": map[string]any{"canary": map[string]any{"steps": canarySteps(4)}}},
				map[string]any{"phase": "Progressing", "currentStepIndex": int64(2)}),
			wantSummary: "step 2/4",
		},
		{
			name: "canary weight shifted",
			old: rolloutObj(nil, map[string]any{"phase": "Progressing",
				"canary": map[string]any{"weights": map[string]any{"canary": map[string]any{"weight": int64(20)}}}}),
			new: rolloutObj(nil, map[string]any{"phase": "Progressing",
				"canary": map[string]any{"weights": map[string]any{"canary": map[string]any{"weight": int64(50)}}}}),
			wantSummary: "canary weight: 20%→50%",
		},
		{
			name: "manual pause entered",
			old:  rolloutObj(nil, map[string]any{"phase": "Progressing"}),
			new: rolloutObj(nil, map[string]any{"phase": "Progressing",
				"pauseConditions": []any{map[string]any{"reason": "CanaryPauseStep"}}}),
			wantSummary: "paused: CanaryPauseStep",
		},
		{
			name: "pause cleared by promote",
			old: rolloutObj(nil, map[string]any{"phase": "Progressing",
				"pauseConditions": []any{map[string]any{"reason": "CanaryPauseStep"}}}),
			new:         rolloutObj(nil, map[string]any{"phase": "Progressing"}),
			wantSummary: "pause cleared",
		},
		{
			name:        "aborted",
			old:         rolloutObj(nil, map[string]any{"phase": "Progressing"}),
			new:         rolloutObj(nil, map[string]any{"phase": "Progressing", "abort": true}),
			wantSummary: "aborted",
		},
		{
			name:        "retried",
			old:         rolloutObj(nil, map[string]any{"phase": "Degraded", "abort": true}),
			new:         rolloutObj(nil, map[string]any{"phase": "Degraded"}),
			wantSummary: "abort cleared",
		},
		{
			name:        "promoted to full",
			old:         rolloutObj(nil, map[string]any{"phase": "Progressing"}),
			new:         rolloutObj(nil, map[string]any{"phase": "Progressing", "promoteFull": true}),
			wantSummary: "promoted to full",
		},
		{
			name:        "controller paused",
			old:         rolloutObj(nil, map[string]any{"phase": "Progressing"}),
			new:         rolloutObj(nil, map[string]any{"phase": "Progressing", "controllerPause": true}),
			wantSummary: "controller paused",
		},
		{
			name:        "stable replicaset moved after promotion",
			old:         rolloutObj(nil, map[string]any{"phase": "Healthy", "stableRS": "abc123"}),
			new:         rolloutObj(nil, map[string]any{"phase": "Healthy", "stableRS": "def456"}),
			wantSummary: "stable rs: abc123→def456",
		},
		{
			name:        "blue-green active selector cut over",
			old:         rolloutObj(nil, map[string]any{"phase": "Progressing", "blueGreen": map[string]any{"activeSelector": "abc123"}}),
			new:         rolloutObj(nil, map[string]any{"phase": "Progressing", "blueGreen": map[string]any{"activeSelector": "def456"}}),
			wantSummary: "active: abc123→def456",
		},
		{
			name: "image rolled",
			old: rolloutObj(map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "web", "image": "web:v1"}}}}}, nil),
			new: rolloutObj(map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "web", "image": "web:v2"}}}}}, nil),
			wantSummary: "image: web:v2",
		},
		{
			name:        "restart requested",
			old:         rolloutObj(nil, nil),
			new:         rolloutObj(map[string]any{"restartAt": "2026-08-06T00:00:00Z"}, nil),
			wantSummary: "restart requested",
		},
		{
			name:        "scaled",
			old:         rolloutObj(map[string]any{"replicas": int64(3)}, nil),
			new:         rolloutObj(map[string]any{"replicas": int64(6)}, nil),
			wantSummary: "scaled: 3→6",
		},
		{
			name:        "updated replicas progressed",
			old:         rolloutObj(nil, map[string]any{"phase": "Progressing", "updatedReplicas": int64(1)}),
			new:         rolloutObj(nil, map[string]any{"phase": "Progressing", "updatedReplicas": int64(2)}),
			wantSummary: "updated: 1→2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes, summary := diffRollout(tt.old, tt.new)
			if len(changes) == 0 {
				t.Fatalf("no changes recorded — the timeline would drop this update")
			}
			joined := strings.Join(summary, "; ")
			if !strings.Contains(joined, tt.wantSummary) {
				t.Fatalf("summary = %q, want it to contain %q", joined, tt.wantSummary)
			}
		})
	}
}

func TestDiffRolloutIgnoresNoOpUpdate(t *testing.T) {
	status := map[string]any{"phase": "Healthy", "currentStepIndex": int64(4), "stableRS": "abc123"}
	changes, _ := diffRollout(rolloutObj(nil, status), rolloutObj(nil, status))
	if len(changes) != 0 {
		t.Fatalf("identical Rollouts produced %d changes: %+v", len(changes), changes)
	}
}

func TestDiffRolloutPauseReasonsAreOrderIndependent(t *testing.T) {
	reasons := func(order ...string) map[string]any {
		conditions := make([]any, 0, len(order))
		for _, reason := range order {
			conditions = append(conditions, map[string]any{"reason": reason})
		}
		return map[string]any{"pauseConditions": conditions}
	}
	changes, _ := diffRollout(
		rolloutObj(nil, reasons("BlueGreenPause", "CanaryPauseStep")),
		rolloutObj(nil, reasons("CanaryPauseStep", "BlueGreenPause")),
	)
	if len(changes) != 0 {
		t.Fatalf("reordered pause conditions reported as a change: %+v", changes)
	}
}

func TestRolloutIsRegisteredInDiffFunctions(t *testing.T) {
	if !KindHasDiffer("Rollout") {
		t.Fatal("Rollout is not registered in diffFunctions")
	}
}
