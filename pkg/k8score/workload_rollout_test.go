package k8score

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/skyhook-io/radar/pkg/rollouts"
)

func newFakeRolloutClient(t *testing.T, objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		rollouts.GVR: "RolloutList",
		{Group: "apps", Version: "v1", Resource: "replicasets"}: "ReplicaSetList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func rolloutObject(namespace, name, image string, mutate func(map[string]any)) *unstructured.Unstructured {
	ro := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Rollout",
		"metadata": map[string]any{
			"namespace": namespace,
			"name":      name,
			"uid":       "ro-uid",
		},
		"spec": map[string]any{
			"replicas": int64(3),
			"strategy": map[string]any{
				"canary": map[string]any{"steps": []any{map[string]any{"setWeight": int64(50)}}},
			},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": name}},
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": "app", "image": image}},
				},
			},
		},
		"status": map[string]any{},
	}}
	if mutate != nil {
		mutate(ro.Object)
	}
	return ro
}

func rolloutReplicaSet(namespace, name, revision, podHash, image string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "ReplicaSet",
		"metadata": map[string]any{
			"namespace":   namespace,
			"name":        name,
			"annotations": map[string]any{rollouts.RevisionAnnotation: revision},
			"labels":      map[string]any{rollouts.PodTemplateHashLabel: podHash},
			"ownerReferences": []any{map[string]any{
				"apiVersion": "argoproj.io/v1alpha1",
				"kind":       "Rollout",
				"name":       "web",
				"uid":        "ro-uid",
			}},
		},
		"spec": map[string]any{
			"replicas": int64(3),
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{
					"app":                         "web",
					rollouts.PodTemplateHashLabel: podHash,
				}},
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": "app", "image": image}},
				},
			},
		},
	}}
}

func lastPatchBody(t *testing.T, client *dynamicfake.FakeDynamicClient) map[string]any {
	t.Helper()
	actions := client.Actions()
	for i := len(actions) - 1; i >= 0; i-- {
		pa, ok := actions[i].(clienttesting.PatchAction)
		if !ok {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(pa.GetPatch(), &body); err != nil {
			t.Fatalf("patch not JSON: %v", err)
		}
		return body
	}
	t.Fatalf("no patch recorded; actions=%v", actions)
	return nil
}

func TestRestartWorkload_RolloutUsesRestartAt(t *testing.T) {
	client := newFakeRolloutClient(t, rolloutObject("prod", "web", "web:v1", nil))
	m := NewWorkloadManager(client, nil)

	if err := m.RestartWorkload(context.Background(), "Rollout", "prod", "web"); err != nil {
		t.Fatalf("RestartWorkload: %v", err)
	}

	spec, ok := lastPatchBody(t, client)["spec"].(map[string]any)
	if !ok {
		t.Fatal("patch has no spec")
	}
	if spec[rollouts.RestartAtField] == nil {
		t.Errorf("spec.%s not set: %v", rollouts.RestartAtField, spec)
	}
	if _, touched := spec["template"]; touched {
		t.Errorf("patched spec.template, which re-runs the whole canary: %v", spec)
	}
}

// The Rollout branch must not need discovery — pkg/rollouts carries its own GVR,
// so these verbs work before the CRD is cached.
func TestRolloutOperationsWorkWithoutDiscovery(t *testing.T) {
	client := newFakeRolloutClient(t,
		rolloutObject("prod", "web", "web:v2", func(o map[string]any) {
			o["status"] = map[string]any{"currentPodHash": "hash2", "stableRS": "hash1"}
		}),
		rolloutReplicaSet("prod", "web-1", "1", "hash1", "web:v1"),
		rolloutReplicaSet("prod", "web-2", "2", "hash2", "web:v2"),
	)
	m := NewWorkloadManager(client, nil)

	revisions, err := m.ListWorkloadRevisions(context.Background(), "rollouts", "prod", "web")
	if err != nil {
		t.Fatalf("ListWorkloadRevisions: %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("got %d revisions, want 2", len(revisions))
	}
	if revisions[0].Number != 2 {
		t.Errorf("not newest-first: %d", revisions[0].Number)
	}
	if !revisions[0].IsCurrent || revisions[0].IsStable {
		t.Errorf("rev 2 current=%v stable=%v, want current only", revisions[0].IsCurrent, revisions[0].IsStable)
	}
	if revisions[1].IsCurrent || !revisions[1].IsStable {
		t.Errorf("rev 1 current=%v stable=%v, want stable only", revisions[1].IsCurrent, revisions[1].IsStable)
	}

	if err := m.RollbackWorkload(context.Background(), "Rollout", "prod", "web", 1); err != nil {
		t.Fatalf("RollbackWorkload: %v", err)
	}
	patches := 0
	for _, action := range client.Actions() {
		if _, ok := action.(clienttesting.PatchAction); ok {
			patches++
		}
	}
	if patches == 0 {
		t.Fatal("rollback issued no patch")
	}
}

func TestRollbackWorkload_RolloutStripsPodTemplateHash(t *testing.T) {
	client := newFakeRolloutClient(t,
		rolloutObject("prod", "web", "web:v2", func(o map[string]any) {
			o["status"] = map[string]any{"currentPodHash": "hash2"}
		}),
		rolloutReplicaSet("prod", "web-1", "1", "hash1", "web:v1"),
		rolloutReplicaSet("prod", "web-2", "2", "hash2", "web:v2"),
	)
	m := NewWorkloadManager(client, nil)

	if err := m.RollbackWorkload(context.Background(), "Rollout", "prod", "web", 1); err != nil {
		t.Fatalf("RollbackWorkload: %v", err)
	}

	for _, action := range client.Actions() {
		pa, ok := action.(clienttesting.PatchAction)
		if !ok {
			continue
		}
		if strings.Contains(string(pa.GetPatch()), rollouts.PodTemplateHashLabel) {
			t.Fatalf("rollback patch leaks %s: %s", rollouts.PodTemplateHashLabel, pa.GetPatch())
		}
	}
}

func TestNormalizeWorkloadKind_Rollout(t *testing.T) {
	for _, in := range []string{"Rollout", "rollout", "rollouts"} {
		if got := NormalizeWorkloadKind(in); got != "rollouts" {
			t.Errorf("NormalizeWorkloadKind(%q) = %q, want rollouts", in, got)
		}
	}
}
