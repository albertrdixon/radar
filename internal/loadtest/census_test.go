package loadtest

import (
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// maxCensusDrift is how far a materialized kind may fall from its declared
// count. It is zero: the census is the measurement's control variable, so any
// drift makes a comparison against the real cluster unsound.
const maxCensusDrift = 0.0

// unitTestCensusScale keeps this suite off the reported census. Every kind
// materializes exactly the count it is given at any scale, so the property is
// unchanged, whereas the reported census materializes 628k objects and about a
// gigabyte of resident memory per run. Scale floors every declared kind at 1,
// so scaling down drops no kind from the check.
const unitTestCensusScale = 0.01

func kindOf(obj runtime.Object) string {
	t := fmt.Sprintf("%T", obj)
	if i := strings.LastIndex(t, "."); i >= 0 {
		return t[i+1:]
	}
	return t
}

func countByKind(objs []runtime.Object) map[string]int {
	got := map[string]int{}
	for _, o := range objs {
		got[kindOf(o)]++
	}
	return got
}

func TestCensusObjectsMatchDeclaredCounts(t *testing.T) {
	census := LargeMultiTenantEKS.Scale(unitTestCensusScale)
	objs := CensusObjects(census, "")
	got := countByKind(objs)

	if len(objs) != census.Total() {
		t.Errorf("materialized %d objects, census declares %d", len(objs), census.Total())
	}

	for kind, want := range census {
		have := got[kind]
		drift := float64(have-want) / float64(want) * 100
		if drift < 0 {
			drift = -drift
		}
		if drift > maxCensusDrift {
			t.Errorf("%s: got %d, census declares %d (%.2f%% drift)", kind, have, want, drift)
		}
	}

	for kind, have := range got {
		if _, declared := census[kind]; !declared {
			t.Errorf("%s: materialized %d objects but the census declares none", kind, have)
		}
	}
}

// The ownership chain is what topology walks; a census with the right counts
// but dangling owner references would measure the wrong graph.
func TestCensusPreservesOwnershipChain(t *testing.T) {
	c := Census{"Namespace": 2, "Node": 2, "Deployment": 3, "ReplicaSet": 7, "Pod": 11}
	objs := CensusObjects(c, "")

	deployUIDs := map[types.UID]bool{}
	rsUIDs := map[types.UID]bool{}
	rsNames := map[string]bool{}
	for _, o := range objs {
		m := o.(metav1.Object)
		switch kindOf(o) {
		case "Deployment":
			deployUIDs[m.GetUID()] = true
		case "ReplicaSet":
			rsUIDs[m.GetUID()] = true
			rsNames[m.GetName()] = true
		}
	}

	for _, o := range objs {
		m := o.(metav1.Object)
		refs := m.GetOwnerReferences()
		if len(refs) == 0 {
			continue
		}
		ref := refs[0]
		switch kindOf(o) {
		case "ReplicaSet":
			if !deployUIDs[ref.UID] {
				t.Errorf("ReplicaSet %s owned by unknown Deployment %s", m.GetName(), ref.UID)
			}
		case "Pod":
			if !rsUIDs[ref.UID] {
				t.Errorf("Pod %s owned by unknown ReplicaSet %s (%s)", m.GetName(), ref.UID, ref.Name)
			}
			if !rsNames[ref.Name] {
				t.Errorf("Pod %s names a ReplicaSet %q that was never materialized", m.GetName(), ref.Name)
			}
		}
	}
}

// An owned Job in a different namespace from its CronJob is an unresolvable
// reference, and topology would render it as an orphan — inflating the graph
// exactly where a real cluster collapses it.
func TestCensusJobsResolveToOwningCronJobInSameNamespace(t *testing.T) {
	objs := CensusObjects(Census{
		"Namespace": 7, "Node": 2, "Deployment": 9, "CronJob": 4, "Job": 13,
	}, "")

	cronJobs := map[types.UID]string{}
	for _, o := range objs {
		if cj, ok := o.(*batchv1.CronJob); ok {
			cronJobs[cj.UID] = cj.Namespace + "/" + cj.Name
		}
	}
	if len(cronJobs) != 4 {
		t.Fatalf("materialized %d CronJobs, want 4", len(cronJobs))
	}

	jobs := 0
	for _, o := range objs {
		job, ok := o.(*batchv1.Job)
		if !ok {
			continue
		}
		jobs++
		if len(job.OwnerReferences) == 0 {
			t.Errorf("Job %s/%s has no owning CronJob", job.Namespace, job.Name)
			continue
		}
		ref := job.OwnerReferences[0]
		owner, known := cronJobs[ref.UID]
		if !known {
			t.Errorf("Job %s/%s owned by unknown CronJob %s", job.Namespace, job.Name, ref.UID)
			continue
		}
		if want := job.Namespace + "/" + ref.Name; owner != want {
			t.Errorf("Job %s/%s names CronJob %s but that CronJob lives at %s", job.Namespace, job.Name, want, owner)
		}
	}
	if jobs != 13 {
		t.Fatalf("materialized %d Jobs, want 13", jobs)
	}
}

// Secret write-time recovery parses ManagedFields inside the informer
// transform. A census whose Secrets carry none makes that path free, and a
// measurement taken against it would understate startup cost.
func TestCensusSecretsCarryDataOwnerManagedFields(t *testing.T) {
	objs := CensusObjects(Census{"Namespace": 1, "Deployment": 1, "Secret": 3}, "")

	found := 0
	for _, o := range objs {
		s, ok := o.(*corev1.Secret)
		if !ok {
			continue
		}
		found++
		if len(s.ManagedFields) == 0 {
			t.Fatalf("Secret %s carries no ManagedFields", s.Name)
		}
		e := s.ManagedFields[0]
		if e.Time == nil || e.FieldsV1 == nil {
			t.Fatalf("Secret %s ManagedFields entry lacks Time or FieldsV1", s.Name)
		}
		if !strings.Contains(string(e.FieldsV1.Raw), `"f:data"`) {
			t.Errorf("Secret %s ManagedFields does not claim ownership of data", s.Name)
		}
		if e.Operation != metav1.ManagedFieldsOperationUpdate && e.Operation != metav1.ManagedFieldsOperationApply {
			t.Errorf("Secret %s ManagedFields operation %q is neither Update nor Apply", s.Name, e.Operation)
		}
	}
	if found != 3 {
		t.Fatalf("materialized %d Secrets, want 3", found)
	}
}

// Events are the largest kind in a real cluster and the reason its heap is
// what it is; they must resolve to pods that exist.
func TestCensusEventsReferenceMaterializedPods(t *testing.T) {
	objs := CensusObjects(Census{"Namespace": 2, "Node": 1, "Deployment": 2, "Pod": 5, "Event": 12}, "")

	pods := map[string]bool{}
	for _, o := range objs {
		if p, ok := o.(*corev1.Pod); ok {
			pods[p.Namespace+"/"+p.Name] = true
		}
	}
	events := 0
	for _, o := range objs {
		e, ok := o.(*corev1.Event)
		if !ok {
			continue
		}
		events++
		key := e.InvolvedObject.Namespace + "/" + e.InvolvedObject.Name
		if !pods[key] {
			t.Errorf("Event %s references pod %s which was never materialized", e.Name, key)
		}
	}
	if events != 12 {
		t.Fatalf("materialized %d Events, want 12", events)
	}
}

// A pod template naming a ConfigMap or Secret that the census never
// materializes silently deletes an entire edge class from the graph, which is
// the exact thing these measurements compare against.
func TestCensusWorkloadConfigRefsResolve(t *testing.T) {
	// ConfigMaps are deliberately scarcer than Deployments, mirroring the real
	// ratio: most workloads must mount nothing rather than a dangling name.
	objs := CensusObjects(Census{
		"Namespace": 4, "Node": 2, "Deployment": 20, "Pod": 40, "Secret": 25, "ConfigMap": 5,
	}, "")

	secrets := map[string]bool{}
	configMaps := map[string]bool{}
	for _, o := range objs {
		switch v := o.(type) {
		case *corev1.Secret:
			secrets[v.Namespace+"/"+v.Name] = true
		case *corev1.ConfigMap:
			configMaps[v.Namespace+"/"+v.Name] = true
		}
	}

	checked := 0
	for _, o := range objs {
		p, ok := o.(*corev1.Pod)
		if !ok {
			continue
		}
		checked++
		for _, c := range p.Spec.Containers {
			for _, ef := range c.EnvFrom {
				if ef.SecretRef == nil {
					continue
				}
				if key := p.Namespace + "/" + ef.SecretRef.Name; !secrets[key] {
					t.Errorf("pod %s/%s consumes Secret %q which was never materialized", p.Namespace, p.Name, key)
				}
			}
		}
		for _, v := range p.Spec.Volumes {
			if v.ConfigMap == nil {
				continue
			}
			if key := p.Namespace + "/" + v.ConfigMap.Name; !configMaps[key] {
				t.Errorf("pod %s/%s mounts ConfigMap %q which was never materialized", p.Namespace, p.Name, key)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no pods materialized")
	}
}

// Workload identity is joined by (namespace, name) like the config refs above,
// so a name that resolves in the wrong namespace erases the ServiceAccount edge
// class just as completely as a name nothing materializes.
func TestCensusWorkloadServiceAccountRefsResolve(t *testing.T) {
	tests := []struct {
		name        string
		census      Census
		wantUnnamed bool
	}{
		// Enough ServiceAccounts for every namespace: nothing runs unnamed.
		{
			name:   "every namespace covered",
			census: Census{"Namespace": 4, "Node": 2, "Deployment": 20, "Pod": 40, "ServiceAccount": 6},
		},
		// The real census has far more workloads than ServiceAccounts, and
		// enough of them to wrap any fixed-modulus mapping.
		{
			name:   "more workloads than ServiceAccounts",
			census: Census{"Namespace": 7, "Node": 2, "Deployment": 600, "Pod": 40, "ServiceAccount": 9},
		},
		// Fewer ServiceAccounts than namespaces: the uncovered ones must name
		// nothing rather than a name that exists in someone else's namespace.
		{
			name:        "fewer ServiceAccounts than namespaces",
			census:      Census{"Namespace": 4, "Node": 2, "Deployment": 20, "Pod": 40, "ServiceAccount": 2},
			wantUnnamed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := CensusObjects(tt.census, "")

			serviceAccounts := map[string]bool{}
			for _, o := range objs {
				if sa, ok := o.(*corev1.ServiceAccount); ok {
					serviceAccounts[sa.Namespace+"/"+sa.Name] = true
				}
			}

			// Both shapes topology reads an identity from: the workload's pod
			// template and the running Pod.
			type identity struct{ kind, id, namespace, serviceAccount string }
			var identities []identity
			for _, o := range objs {
				switch v := o.(type) {
				case *appsv1.Deployment:
					identities = append(identities, identity{"Deployment", v.Name, v.Namespace, v.Spec.Template.Spec.ServiceAccountName})
				case *corev1.Pod:
					identities = append(identities, identity{"Pod", v.Name, v.Namespace, v.Spec.ServiceAccountName})
				}
			}

			named, unnamed := 0, 0
			for _, got := range identities {
				if got.serviceAccount == "" {
					unnamed++
					continue
				}
				named++
				if key := got.namespace + "/" + got.serviceAccount; !serviceAccounts[key] {
					t.Errorf("%s %s/%s runs as ServiceAccount %q, which was never materialized in that namespace",
						got.kind, got.namespace, got.id, key)
				}
			}

			if named == 0 {
				t.Fatal("nothing names a ServiceAccount; the edge class is absent from the measured graph")
			}
			if got := unnamed > 0; got != tt.wantUnnamed {
				t.Errorf("workloads running unnamed = %v (%d of %d), want %v",
					got, unnamed, len(identities), tt.wantUnnamed)
			}
		})
	}
}

func TestCensusIsDeterministic(t *testing.T) {
	first := CensusObjects(Census{"Namespace": 2, "Deployment": 3, "Pod": 7, "Event": 5}, "")
	second := CensusObjects(Census{"Namespace": 2, "Deployment": 3, "Pod": 7, "Event": 5}, "")

	if len(first) != len(second) {
		t.Fatalf("object counts differ across runs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		a, b := first[i].(metav1.Object), second[i].(metav1.Object)
		if a.GetName() != b.GetName() || a.GetNamespace() != b.GetNamespace() {
			t.Fatalf("object %d differs across runs: %s/%s vs %s/%s",
				i, a.GetNamespace(), a.GetName(), b.GetNamespace(), b.GetName())
		}
	}
}
