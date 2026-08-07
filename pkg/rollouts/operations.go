package rollouts

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// Patch payloads, verbatim from kubectl-argo-rollouts.
const (
	abortPatch = `{"status":{"abort":true}}`
	retryPatch = `{"status":{"abort":false}}`

	unpausePatch = `{"spec":{"paused":false}}`

	clearPauseConditionsPatch                   = `{"status":{"pauseConditions":null}}`
	clearPauseConditionsAndControllerPausePatch = `{"status":{"pauseConditions":null, "controllerPause":false, "currentStepIndex":%d}}`
	clearPauseConditionsPatchWithStep           = `{"status":{"pauseConditions":null, "currentStepIndex":%d}}`

	promoteFullPatch = `{"status":{"promoteFull":true}}`
)

func result(operation, namespace, name, message string) OperationResult {
	return OperationResult{Message: message, Operation: operation, Namespace: namespace, Name: name}
}

// A paused Rollout needs spec.paused cleared alongside the status patch.
func unpauseSpecPatch(ro *unstructured.Unstructured) []byte {
	if !isPaused(ro) {
		return nil
	}
	return []byte(unpausePatch)
}

// Abort reverts traffic to the stable ReplicaSet without touching spec. The
// fastest way out of a bad rollout; Retry resumes it afterwards.
func Abort(ctx context.Context, client dynamic.Interface, namespace, name string) (OperationResult, error) {
	if _, err := get(ctx, client, namespace, name); err != nil {
		return OperationResult{}, err
	}
	if err := patchStatusThenSpec(ctx, client, namespace, name, []byte(abortPatch), nil); err != nil {
		return OperationResult{}, err
	}
	return result("abort", namespace, name,
		fmt.Sprintf("Rollout %s/%s aborted — traffic reverted to the stable version", namespace, name)), nil
}

// Retry clears an abort so the rollout resumes from its current step.
func Retry(ctx context.Context, client dynamic.Interface, namespace, name string) (OperationResult, error) {
	if _, err := get(ctx, client, namespace, name); err != nil {
		return OperationResult{}, err
	}
	if err := patchStatusThenSpec(ctx, client, namespace, name, []byte(retryPatch), nil); err != nil {
		return OperationResult{}, err
	}
	return result("retry", namespace, name, fmt.Sprintf("Rollout %s/%s retried", namespace, name)), nil
}

// Promote clears the current pause. The controller advances the step itself once
// pauseConditions clears; only an Inconclusive analysis needs it moved here.
func Promote(ctx context.Context, client dynamic.Interface, namespace, name string) (OperationResult, error) {
	ro, err := get(ctx, client, namespace, name)
	if err != nil {
		return OperationResult{}, err
	}

	var statusPatch []byte
	var stepIndex *int64
	next := nextStepIndex(ro)

	switch {
	case isInconclusive(ro) && pauseConditionCount(ro) > 0 && controllerPause(ro):
		stepIndex = &next
		statusPatch = []byte(fmt.Sprintf(clearPauseConditionsAndControllerPausePatch, next))
	case pauseConditionCount(ro) > 0:
		statusPatch = []byte(clearPauseConditionsPatch)
	case len(canarySteps(ro)) > 0:
		// Nothing is paused, so clearing pauseConditions would be a no-op: mid
		// analysis or experiment the step index is what has to move.
		stepIndex = &next
		statusPatch = []byte(fmt.Sprintf(clearPauseConditionsPatchWithStep, next))
	}

	if err := patchStatusThenSpec(ctx, client, namespace, name, statusPatch, unpauseSpecPatch(ro)); err != nil {
		return OperationResult{}, err
	}
	res := result("promote", namespace, name, fmt.Sprintf("Rollout %s/%s promoted", namespace, name))
	res.StepIndex = stepIndex
	return res, nil
}

// PromoteFull skips every remaining step, pause, and analysis, taking the
// canary straight to 100%. The emergency-hotfix path.
func PromoteFull(ctx context.Context, client dynamic.Interface, namespace, name string) (OperationResult, error) {
	ro, err := get(ctx, client, namespace, name)
	if err != nil {
		return OperationResult{}, err
	}

	// CLI parity: Argo omits the promoteFull patch when there is no new revision.
	var statusPatch []byte
	if !currentIsStable(ro) {
		statusPatch = []byte(promoteFullPatch)
	}

	if err := patchStatusThenSpec(ctx, client, namespace, name, statusPatch, unpauseSpecPatch(ro)); err != nil {
		return OperationResult{}, err
	}

	repatchUntilPromoted(ctx, client, namespace, name)

	return result("promote-full", namespace, name,
		fmt.Sprintf("Rollout %s/%s promoted to full — remaining steps, pauses, and analysis skipped", namespace, name)), nil
}

var (
	promoteFullSettleTimeout  = 15 * time.Second
	promoteFullSettleInterval = 500 * time.Millisecond
)

// Reconciling a new template, the controller writes status from a copy read before the
// promoteFull patch and silently drops it. Best effort: the rollback already landed.
func repatchUntilPromoted(ctx context.Context, client dynamic.Interface, namespace, name string) {
	deadline := time.Now().Add(promoteFullSettleTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(promoteFullSettleInterval):
		}

		ro, err := get(ctx, client, namespace, name)
		if err != nil {
			return
		}
		if currentIsStable(ro) {
			return
		}
		if promoteFullPending(ro) {
			continue
		}
		if err := patchStatusThenSpec(ctx, client, namespace, name, []byte(promoteFullPatch), unpauseSpecPatch(ro)); err != nil {
			return
		}
	}
}

func promoteFullPending(ro *unstructured.Unstructured) bool {
	pending, _, _ := unstructured.NestedBool(ro.Object, "status", "promoteFull")
	return pending
}

// SkipCurrentStep advances past exactly one canary step. Blue-green Rollouts and
// step-less canaries have nothing to skip.
func SkipCurrentStep(ctx context.Context, client dynamic.Interface, namespace, name string) (OperationResult, error) {
	ro, err := get(ctx, client, namespace, name)
	if err != nil {
		return OperationResult{}, err
	}

	if StrategyOf(ro) == StrategyBlueGreen {
		return OperationResult{}, fmt.Errorf("Rollout %s/%s uses the blueGreen strategy: %w", namespace, name, ErrNoSteps)
	}
	steps := canarySteps(ro)
	if len(steps) == 0 {
		return OperationResult{}, fmt.Errorf("Rollout %s/%s defines no canary steps: %w", namespace, name, ErrNoSteps)
	}
	if idx, ok := currentStepIndex(ro); ok && idx >= int64(len(steps)) {
		return OperationResult{}, fmt.Errorf("Rollout %s/%s is at step %d of %d: %w", namespace, name, idx, len(steps), ErrAlreadyAtLastStep)
	}

	next := nextStepIndex(ro)
	statusPatch := []byte(fmt.Sprintf(clearPauseConditionsPatchWithStep, next))

	if err := patchStatusThenSpec(ctx, client, namespace, name, statusPatch, unpauseSpecPatch(ro)); err != nil {
		return OperationResult{}, err
	}
	res := result("skip-step", namespace, name,
		fmt.Sprintf("Rollout %s/%s advanced to step %d of %d", namespace, name, next, len(steps)))
	res.StepIndex = &next
	return res, nil
}

// Capped at the step count. An unset index means stepping hasn't started, so the
// next step is 1.
func nextStepIndex(ro *unstructured.Unstructured) int64 {
	steps := int64(len(canarySteps(ro)))
	idx, ok := currentStepIndex(ro)
	if !ok {
		if steps == 0 {
			return 0
		}
		return 1
	}
	if idx+1 > steps {
		return steps
	}
	return idx + 1
}

// Restart recreates the Rollout's pods in place via spec.restartAt, without
// creating a new revision or re-running the canary steps.
func Restart(ctx context.Context, client dynamic.Interface, namespace, name string) (OperationResult, error) {
	if _, err := get(ctx, client, namespace, name); err != nil {
		return OperationResult{}, err
	}

	restartAt := time.Now().UTC().Format(time.RFC3339)
	patch := fmt.Sprintf(`{"spec":{%q:%q}}`, RestartAtField, restartAt)
	if _, err := client.Resource(GVR).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{},
	); err != nil {
		return OperationResult{}, fmt.Errorf("failed to restart Rollout: %w", err)
	}
	return result("restart", namespace, name,
		fmt.Sprintf("Rollout %s/%s restarting pods (restartAt %s)", namespace, name, restartAt)), nil
}
