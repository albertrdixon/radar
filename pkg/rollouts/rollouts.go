// Package rollouts implements control-plane operations for Argo Rollouts
// (argoproj.io/v1alpha1 Rollout): abort, retry, promote, skip-step, and undo.
// Patch payloads and target subresources match `kubectl argo rollouts`; there is
// no dependency on the Argo Rollouts Go module or a Rollouts API server.
package rollouts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

var GVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "rollouts",
}

// A Rollout's revisions are its owned ReplicaSets, keyed by RevisionAnnotation.
var replicaSetGVR = schema.GroupVersionResource{
	Group:    "apps",
	Version:  "v1",
	Resource: "replicasets",
}

const (
	// The Rollouts analogue of deployment.kubernetes.io/revision.
	RevisionAnnotation = "rollout.argoproj.io/revision"

	// Must be stripped from any template written back into a Rollout's spec: the
	// controller derives the hash from template contents, so a stale hash makes
	// it compute a different one than the ReplicaSet it's matching against.
	PodTemplateHashLabel = "rollouts-pod-template-hash"

	RestartAtField = "restartAt"
)

var (
	ErrRevisionNotFound = errors.New("revision not found")

	// Undo target already matches the live template; the write would be a no-op.
	ErrTemplateUnchanged = errors.New("pod template already matches the requested revision")

	ErrWorkloadRefUnsupported = errors.New("unsupported workloadRef kind")
	ErrNoSteps                = errors.New("rollout has no canary steps")
	ErrAlreadyAtLastStep      = errors.New("rollout is already at its last step")
	ErrResourceTerminating    = errors.New("rollout is pending deletion")
)

type OperationResult struct {
	Message   string `json:"message"`
	Operation string `json:"operation"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Revision  int64  `json:"revision,omitempty"`
	StepIndex *int64 `json:"stepIndex,omitempty"`
}

type Strategy string

const (
	StrategyCanary    Strategy = "canary"
	StrategyBlueGreen Strategy = "blueGreen"
	StrategyUnknown   Strategy = "unknown"
)

func get(ctx context.Context, client dynamic.Interface, namespace, name string) (*unstructured.Unstructured, error) {
	ro, err := client.Resource(GVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("Rollout %s/%s not found: %w", namespace, name, err)
		}
		return nil, fmt.Errorf("failed to get Rollout %s/%s: %w", namespace, name, err)
	}
	if err := assertNotTerminating(ro, namespace, name); err != nil {
		return nil, err
	}
	return ro, nil
}

func assertNotTerminating(ro *unstructured.Unstructured, namespace, name string) error {
	if ro.GetDeletionTimestamp().IsZero() {
		return nil
	}
	suffix := ""
	if finalizers := ro.GetFinalizers(); len(finalizers) > 0 {
		suffix = fmt.Sprintf(" (finalizers: %s)", strings.Join(finalizers, ", "))
	}
	return fmt.Errorf("Rollout %s/%s is being deleted%s: %w", namespace, name, suffix, ErrResourceTerminating)
}

func StrategyOf(ro *unstructured.Unstructured) Strategy {
	if _, found, _ := unstructured.NestedMap(ro.Object, "spec", "strategy", "canary"); found {
		return StrategyCanary
	}
	if _, found, _ := unstructured.NestedMap(ro.Object, "spec", "strategy", "blueGreen"); found {
		return StrategyBlueGreen
	}
	return StrategyUnknown
}

func canarySteps(ro *unstructured.Unstructured) []any {
	steps, _, _ := unstructured.NestedSlice(ro.Object, "spec", "strategy", "canary", "steps")
	return steps
}

// Pointer field in the Rollout API — absent (not yet stepping) differs from 0.
func currentStepIndex(ro *unstructured.Unstructured) (int64, bool) {
	idx, found, err := unstructured.NestedInt64(ro.Object, "status", "currentStepIndex")
	if err != nil || !found {
		return 0, false
	}
	return idx, true
}

func isPaused(ro *unstructured.Unstructured) bool {
	paused, _, _ := unstructured.NestedBool(ro.Object, "spec", "paused")
	return paused
}

// currentIsStable reports that the rolling-out revision is already the stable one,
// i.e. there is nothing left to promote.
func currentIsStable(ro *unstructured.Unstructured) bool {
	current, _, _ := unstructured.NestedString(ro.Object, "status", "currentPodHash")
	stable, _, _ := unstructured.NestedString(ro.Object, "status", "stableRS")
	return current == stable
}

func pauseConditionCount(ro *unstructured.Unstructured) int {
	conds, _, _ := unstructured.NestedSlice(ro.Object, "status", "pauseConditions")
	return len(conds)
}

func controllerPause(ro *unstructured.Unstructured) bool {
	p, _, _ := unstructured.NestedBool(ro.Object, "status", "controllerPause")
	return p
}

// A canary sitting on an Inconclusive analysis run needs controllerPause cleared
// and the step advanced together, or the controller re-pauses on the same result.
func isInconclusive(ro *unstructured.Unstructured) bool {
	if StrategyOf(ro) != StrategyCanary {
		return false
	}
	phase, found, _ := unstructured.NestedString(ro.Object, "status", "canary", "currentStepAnalysisRunStatus", "status")
	return found && phase == "Inconclusive"
}

func patchStatusThenSpec(ctx context.Context, client dynamic.Interface, namespace, name string, statusPatch, specPatch []byte) error {
	ri := client.Resource(GVR).Namespace(namespace)

	if statusPatch != nil {
		if _, err := ri.Patch(ctx, name, types.MergePatchType, statusPatch, metav1.PatchOptions{}, "status"); err != nil {
			return fmt.Errorf("failed to patch Rollout status: %w", err)
		}
	}

	if specPatch != nil {
		if _, err := ri.Patch(ctx, name, types.MergePatchType, specPatch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch Rollout: %w", err)
		}
	}
	return nil
}
