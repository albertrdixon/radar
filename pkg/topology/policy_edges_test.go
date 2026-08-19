package topology

import (
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Policies select workloads inside their own namespace. The builder resolves that through the
// by-namespace workload index rather than by walking every workload in the
// cluster and discarding the ones that don't match — the walk costs
// policies x cluster workloads, which is what BenchmarkRelationshipCacheBuild
// measures at the field census.
//
// Both readings produce the same graph, so the index can only be verified by
// what it emits: same-namespace matches, and nothing from anywhere else.
// CiliumNetworkPolicy resolves through the same index; it is not covered here
// because it arrives through the dynamic provider.

// policyWorkloadFixture puts perNamespace copies of a Deployment/StatefulSet/
// DaemonSet trio in every namespace, all carrying the labels the tests'
// policy selector matches. Uniform labels are the point: a build that resolves
// a selector outside the policy's namespace finds matches there, so the mistake
// shows up as edges rather than as silence.
func policyWorkloadFixture(namespaces []string, perNamespace int) *mockProvider {
	m := &mockProvider{}
	podLabels := map[string]string{"app": "web"}
	template := corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: podLabels}}

	for _, ns := range namespaces {
		for i := range perNamespace {
			suffix := ""
			if i > 0 {
				suffix = fmt.Sprintf("-%d", i)
			}
			m.deployments = append(m.deployments, &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "web" + suffix, Namespace: ns},
				Spec:       appsv1.DeploymentSpec{Template: template},
			})
			m.statefulSets = append(m.statefulSets, &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: "web-sts" + suffix, Namespace: ns},
				Spec:       appsv1.StatefulSetSpec{Template: template},
			})
			m.daemonSets = append(m.daemonSets, &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: "web-ds" + suffix, Namespace: ns},
				Spec:       appsv1.DaemonSetSpec{Template: template},
			})
		}
	}
	return m
}

func edgeTargetsFrom(topo *Topology, sourcePrefix string) []string {
	var targets []string
	for _, e := range topo.Edges {
		if strings.HasPrefix(e.Source, sourcePrefix) {
			targets = append(targets, e.Target)
		}
	}
	return targets
}

// TestPolicyEdgesStayInsideTheirNamespace is the correctness half. Every
// namespace holds a workload trio with labels the policy selector matches, so a
// build that resolved the selector against the wrong namespace's workloads
// would show up as extra edges — a "protected by" claim about a policy that
// does not apply, which is worse than no claim at all.
func TestPolicyEdgesStayInsideTheirNamespace(t *testing.T) {
	provider := policyWorkloadFixture([]string{"team-a", "team-b", "team-c"}, 1)
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}

	provider.networkPolicies = []*networkingv1.NetworkPolicy{{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-web", Namespace: "team-b"},
		Spec:       networkingv1.NetworkPolicySpec{PodSelector: *selector},
	}}
	provider.pdbs = []*policyv1.PodDisruptionBudget{{
		ObjectMeta: metav1.ObjectMeta{Name: "web-pdb", Namespace: "team-b"},
		Spec:       policyv1.PodDisruptionBudgetSpec{Selector: selector},
	}}

	topo, err := NewBuilder(provider).Build(relationshipCacheOptions())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	for _, tc := range []struct {
		name   string
		source string
	}{
		{"NetworkPolicy", "networkpolicy/team-b/allow-web"},
		{"PodDisruptionBudget", "poddisruptionbudget/team-b/web-pdb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targets := edgeTargetsFrom(topo, tc.source)
			want := map[string]bool{
				"deployment/team-b/web":      false,
				"statefulset/team-b/web-sts": false,
				"daemonset/team-b/web-ds":    false,
			}
			for _, target := range targets {
				seen, expected := want[target]
				if !expected {
					t.Errorf("%s produced edge to %q — the selector was resolved outside its namespace", tc.source, target)
					continue
				}
				if seen {
					t.Errorf("%s produced a duplicate edge to %q", tc.source, target)
				}
				want[target] = true
			}
			for target, seen := range want {
				if !seen {
					t.Errorf("%s is missing its edge to %q", tc.source, target)
				}
			}
		})
	}
}
