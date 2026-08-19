package loadtest

import (
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// fixedClock timestamps every generated object. A wall-clock read would make
// the population non-deterministic, and two runs seeded minutes apart would
// not be comparable measurements.
var fixedClock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Census is an explicit per-kind object count, keyed by Kind. It replaces the
// pods-and-apps model when the population needs to match a real cluster's
// shape rather than a uniform one: kind ratios, not pod count, are what drive
// topology edge-matching and informer memory.
type Census map[string]int

// LargeMultiTenantEKS is the census of a ~250-node, 432-namespace EKS cluster
// running Crossplane, Kyverno, Argo CD, Traefik and VictoriaMetrics — a shape
// where workload kinds outnumber pods and Events dominate everything.
//
// The ratios are what matter and they are deliberately un-round: ReplicaSets
// run 2.2x Deployments (revision history), Deployments run 0.68x Pods (most
// workloads are 1-2 replicas), and Events outnumber every other kind combined
// by 3x.
var LargeMultiTenantEKS = Census{
	"Event":                   435554,
	"ReplicaSet":              43033,
	"Pod":                     29455,
	"Secret":                  26668,
	"Service":                 23547,
	"Deployment":              19924,
	"Job":                     19861,
	"Ingress":                 16977,
	"CronJob":                 4291,
	"ConfigMap":               3894,
	"NetworkPolicy":           1480,
	"PersistentVolume":        816,
	"PersistentVolumeClaim":   787,
	"ServiceAccount":          545,
	"Namespace":               432,
	"StatefulSet":             388,
	"HorizontalPodAutoscaler": 385,
	"Node":                    248,
	"ClusterRole":             216,
	"ClusterRoleBinding":      168,
	"RoleBinding":             66,
	"Role":                    60,
	"PodDisruptionBudget":     12,
	"DaemonSet":               9,
	"StorageClass":            5,
	"IngressClass":            3,
}

// Profiles maps a -profile flag value to its census.
var Profiles = map[string]Census{
	"large-eks": LargeMultiTenantEKS,
}

func (c Census) get(kind string) int {
	if n := c[kind]; n > 0 {
		return n
	}
	return 0
}

// Total reports how many objects the census materializes.
func (c Census) Total() int {
	total := 0
	for _, n := range c {
		total += n
	}
	return total
}

// censusLayout precomputes the divisors every builder indexes against. Each
// kind's object i attaches to app i%apps, so a kind with fewer objects than
// apps covers a prefix of them and a kind with more wraps — which is how the
// real ratios (2.2 ReplicaSets per Deployment, 0.2 ConfigMaps) reproduce
// without special-casing each kind.
type censusLayout struct {
	apps       int
	namespaces int
	nodes      int
	// configMaps bounds which apps mount one. Real clusters hold far fewer
	// ConfigMaps than workloads, and a pod template naming one that does not
	// exist yields no edge — so the shortfall has to be modelled as "most
	// workloads mount nothing", not "every workload mounts a dangling ref".
	configMaps int
	// cronJobs owns the Job population. In a cluster with this many CronJobs
	// almost every Job is one of their runs, and an owned Job collapses under
	// its parent in topology instead of standing alone — so leaving Jobs as
	// orphans inflates the graph.
	cronJobs int
	// serviceAccounts bounds which namespaces have an identity to run under.
	// Workloads share one per namespace (see serviceAccountName), so this only
	// bites on a census with fewer ServiceAccounts than namespaces — there the
	// remainder must name nothing rather than a name nothing materializes.
	serviceAccounts int
}

func newCensusLayout(c Census) censusLayout {
	return censusLayout{
		apps:       max(c.get("Deployment"), 1),
		namespaces: max(c.get("Namespace"), 1),
		nodes:      max(c.get("Node"), 1),
		configMaps: c.get("ConfigMap"),
		cronJobs:   c.get("CronJob"),

		serviceAccounts: c.get("ServiceAccount"),
	}
}

func (l censusLayout) cronJobIndex(i int) int   { return i % max(l.cronJobs, 1) }
func (l censusLayout) cronJobName(i int) string { return fmt.Sprintf("cron-%05d", l.cronJobIndex(i)) }
func (l censusLayout) cronJobUID(i int) types.UID {
	return types.UID(fmt.Sprintf("loadtest-cron-%05d", l.cronJobIndex(i)))
}

// cronJobNS keys off the CronJob index, not the Job index: an owned Job must
// share its owner's namespace or the ownership edge never resolves.
func (l censusLayout) cronJobNS(i int) string {
	if l.cronJobs == 0 {
		return l.ns(i)
	}
	return fmt.Sprintf("loadtest-%03d", l.cronJobIndex(i)%l.namespaces)
}

func (l censusLayout) hasConfigMap(app int) bool { return app%l.apps < l.configMaps }

// serviceAccountName is the identity an app's pod template runs under. Apps
// sharing a namespace share it, which is the real shape: a cluster holds far
// fewer ServiceAccounts than workloads because most workloads run under their
// namespace's default. Indexing it by namespace is also what makes it resolve —
// censusServiceAccount places ServiceAccount i in app i's namespace, so a name
// picked on any other axis lands in the wrong namespace, and topology's
// (namespace, name) join then drops the edge as silently as a name nothing
// materializes. Empty when the census is too small to cover the namespace.
func (l censusLayout) serviceAccountName(app int) string {
	index := l.nsIndex(app)
	if index >= l.serviceAccounts {
		return ""
	}
	return fmt.Sprintf("sa-%04d", index)
}

// Replica distribution. A census fixes totals, not shape, and shape is what
// the graph is built from: topology renders each pod individually up to 5 per
// workload and collapses anything above that into one group node. Spreading
// replicas evenly therefore never groups and inflates the node count, while a
// real cluster is a long tail — a handful of large services, mostly
// singletons, and some workloads scaled to zero.
//
// The tail size is calibrated against the reported graph, not derived from it:
// a diagnostics snapshot carries per-kind totals but not per-workload replica
// counts, so this is the one parameter fitted rather than observed.
const (
	censusTailWorkloads = 225
	censusTailReplicas  = 50
)

func (l censusLayout) app(i int) int { return i % l.apps }

// podApp maps a pod index to its workload under the long-tail distribution:
// the first censusTailWorkloads apps take censusTailReplicas pods each, and
// every remaining pod is the sole replica of its own app.
func (l censusLayout) podApp(i int) int {
	tail := censusTailWorkloads * censusTailReplicas
	if i < tail {
		return i / censusTailReplicas
	}
	return (censusTailWorkloads + (i - tail)) % l.apps
}

func (l censusLayout) replicasForApp(app int) int32 {
	if app < censusTailWorkloads {
		return censusTailReplicas
	}
	return 1
}

func (l censusLayout) podName(i int) string {
	return fmt.Sprintf("app-%05d-%06d", l.podApp(i), i)
}

func (l censusLayout) podNS(i int) string {
	return fmt.Sprintf("loadtest-%03d", l.podApp(i)%l.namespaces)
}
func (l censusLayout) nsIndex(i int) int { return l.app(i) % l.namespaces }
func (l censusLayout) ns(i int) string   { return fmt.Sprintf("loadtest-%03d", l.nsIndex(i)) }
func (l censusLayout) node(i int) string { return fmt.Sprintf("loadtest-node-%03d", i%l.nodes) }

func (l censusLayout) appName(i int) string { return fmt.Sprintf("app-%05d", l.app(i)) }
func (l censusLayout) labels(i int) map[string]string {
	return map[string]string{"app": l.appName(i), "loadtest": "true"}
}
func (l censusLayout) deployUID(i int) types.UID {
	return types.UID(fmt.Sprintf("loadtest-deploy-%05d", l.app(i)))
}
func (l censusLayout) rsName(i int) string { return fmt.Sprintf("app-%05d-rs-%d", l.app(i), i/l.apps) }
func (l censusLayout) rsUID(i int) types.UID {
	return types.UID(fmt.Sprintf("loadtest-rs-%05d-%d", l.app(i), i/l.apps))
}

// CensusObjects materializes the census. Objects are emitted kind by kind in
// descending count order so the largest LISTs are contiguous in the tracker,
// matching how an apiserver would page them.
func CensusObjects(c Census, image string) []runtime.Object {
	if image == "" {
		image = DefaultImage
	}
	l := newCensusLayout(c)
	objs := make([]runtime.Object, 0, c.Total())

	for i := 0; i < c.get("Namespace"); i++ {
		objs = append(objs, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("loadtest-%03d", i), Labels: map[string]string{"loadtest": "true"}},
			Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		})
	}
	for i := 0; i < c.get("Node"); i++ {
		objs = append(objs, censusNode(i))
	}
	for i := 0; i < c.get("Deployment"); i++ {
		objs = append(objs, censusDeployment(l, i, image))
	}
	for i := 0; i < c.get("ReplicaSet"); i++ {
		objs = append(objs, censusReplicaSet(l, i, image))
	}
	for i := 0; i < c.get("Pod"); i++ {
		objs = append(objs, censusPod(l, i, image))
	}
	for i := 0; i < c.get("Service"); i++ {
		objs = append(objs, censusService(l, i))
	}
	for i := 0; i < c.get("Secret"); i++ {
		objs = append(objs, censusSecret(l, i))
	}
	for i := 0; i < c.get("ConfigMap"); i++ {
		objs = append(objs, censusConfigMap(l, i))
	}
	for i := 0; i < c.get("Event"); i++ {
		objs = append(objs, censusEvent(l, i, c.get("Pod")))
	}
	for i := 0; i < c.get("Job"); i++ {
		objs = append(objs, censusJob(l, i, image))
	}
	for i := 0; i < c.get("CronJob"); i++ {
		objs = append(objs, censusCronJob(l, i, image))
	}
	for i := 0; i < c.get("Ingress"); i++ {
		objs = append(objs, censusIngress(l, i))
	}
	for i := 0; i < c.get("StatefulSet"); i++ {
		objs = append(objs, censusStatefulSet(l, i, image))
	}
	for i := 0; i < c.get("DaemonSet"); i++ {
		objs = append(objs, censusDaemonSet(l, i, image))
	}
	for i := 0; i < c.get("HorizontalPodAutoscaler"); i++ {
		objs = append(objs, censusHPA(l, i))
	}
	for i := 0; i < c.get("PersistentVolumeClaim"); i++ {
		objs = append(objs, censusPVC(l, i))
	}
	for i := 0; i < c.get("PersistentVolume"); i++ {
		objs = append(objs, censusPV(i))
	}
	for i := 0; i < c.get("NetworkPolicy"); i++ {
		objs = append(objs, censusNetworkPolicy(l, i))
	}
	for i := 0; i < c.get("ServiceAccount"); i++ {
		objs = append(objs, censusServiceAccount(l, i))
	}
	for i := 0; i < c.get("Role"); i++ {
		objs = append(objs, censusRole(l, i))
	}
	for i := 0; i < c.get("RoleBinding"); i++ {
		objs = append(objs, censusRoleBinding(l, i))
	}
	for i := 0; i < c.get("ClusterRole"); i++ {
		objs = append(objs, censusClusterRole(i))
	}
	for i := 0; i < c.get("ClusterRoleBinding"); i++ {
		objs = append(objs, censusClusterRoleBinding(i))
	}
	for i := 0; i < c.get("PodDisruptionBudget"); i++ {
		objs = append(objs, censusPDB(l, i))
	}
	for i := 0; i < c.get("StorageClass"); i++ {
		objs = append(objs, censusStorageClass(i))
	}
	for i := 0; i < c.get("IngressClass"); i++ {
		objs = append(objs, censusIngressClass(i))
	}
	return objs
}

func censusNode(i int) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("loadtest-node-%03d", i), Labels: map[string]string{"loadtest": "true"}},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Capacity: corev1.ResourceList{
				corev1.ResourcePods:   resource.MustParse("110"),
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourcePods:   resource.MustParse("110"),
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
		},
	}
}

func censusPodSpec(l censusLayout, i int, image string) corev1.PodSpec {
	spec := corev1.PodSpec{
		NodeName:           l.node(i),
		ServiceAccountName: l.serviceAccountName(l.app(i)),
		Containers: []corev1.Container{{
			Name:  "app",
			Image: image,
			EnvFrom: []corev1.EnvFromSource{{
				SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: censusSecretName(l, l.app(i))}},
			}},
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("16Mi"),
			}},
		}},
	}
	if l.hasConfigMap(l.app(i)) {
		spec.Volumes = []corev1.Volume{{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: fmt.Sprintf("app-%05d-config", l.app(i))}},
			},
		}}
	}
	return spec
}

func censusPodTemplate(l censusLayout, i int, image string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: l.labels(i)},
		Spec:       censusPodSpec(l, i, image),
	}
}

func censusDeployment(l censusLayout, i int, image string) *appsv1.Deployment {
	r := l.replicasForApp(l.app(i))
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: l.appName(i), Namespace: l.ns(i), UID: l.deployUID(i), Labels: l.labels(i),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &r,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": l.appName(i)}},
			Template: censusPodTemplate(l, i, image),
		},
		Status: appsv1.DeploymentStatus{Replicas: r, ReadyReplicas: r, AvailableReplicas: r, UpdatedReplicas: r},
	}
}

func censusReplicaSet(l censusLayout, i int, image string) *appsv1.ReplicaSet {
	// Only the current revision (the first wrap) carries replicas; the rest are
	// the scaled-to-zero history a Deployment accumulates, which is why real
	// clusters hold far more ReplicaSets than Deployments.
	r := int32(0)
	if i < l.apps {
		r = l.replicasForApp(l.app(i))
	}
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: l.rsName(i), Namespace: l.ns(i), UID: l.rsUID(i), Labels: l.labels(i),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: l.appName(i),
				UID: l.deployUID(i), Controller: boolPtr(true),
			}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &r,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": l.appName(i)}},
			Template: censusPodTemplate(l, i, image),
		},
		Status: appsv1.ReplicaSetStatus{Replicas: r, ReadyReplicas: r, AvailableReplicas: r},
	}
}

func censusPod(l censusLayout, i int, image string) *corev1.Pod {
	app := l.podApp(i)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: l.podName(i), Namespace: l.podNS(i), Labels: l.labels(app),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: l.rsName(app),
				UID: l.rsUID(app), Controller: boolPtr(true),
			}},
		},
		Spec: censusPodSpec(l, app, image),
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", Ready: true, Started: boolPtr(true),
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}

func censusService(l censusLayout, i int) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("app-%05d-svc-%d", l.app(i), i/l.apps), Namespace: l.ns(i), Labels: l.labels(i),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": l.appName(i)},
			Ports:    []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt(8080)}},
		},
	}
}

// censusSecret carries ManagedFields naming a data owner. The informer
// transform parses exactly this to recover Secret write times, so a Secret
// without it makes that path free — and invisible to a measurement.
// censusSecretName is the Secret a workload's pod template consumes: the
// first one generated for its app. Pod specs and Secret objects must agree on
// it or the ConfigMap/Secret-to-workload edges never form, and the graph under
// measurement is missing a whole edge class.
func censusSecretName(l censusLayout, app int) string {
	return fmt.Sprintf("app-%05d-secret-0", app%l.apps)
}

func censusSecret(l censusLayout, i int) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("app-%05d-secret-%d", l.app(i), i/l.apps), Namespace: l.ns(i), Labels: l.labels(i),
			ManagedFields: []metav1.ManagedFieldsEntry{{
				Manager:    "helm",
				Operation:  metav1.ManagedFieldsOperationUpdate,
				APIVersion: "v1",
				Time:       &metav1.Time{Time: fixedClock},
				FieldsType: "FieldsV1",
				FieldsV1:   &metav1.FieldsV1{Raw: []byte(`{"f:data":{".":{},"f:token":{}},"f:type":{}}`)},
			}},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"token": []byte("c3ludGhldGljLWxvYWR0ZXN0LXNlY3JldC12YWx1ZQ==")},
	}
}

func censusConfigMap(l censusLayout, i int) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("app-%05d-config", l.app(i)), Namespace: l.ns(i), Labels: l.labels(i),
		},
		Data: map[string]string{"app.conf": fmt.Sprintf("name=%s\n", l.appName(i))},
	}
}

func censusEvent(l censusLayout, i, pods int) *corev1.Event {
	reasons := []string{"Scheduled", "Pulled", "Created", "Started", "BackOff", "Killing", "Unhealthy", "FailedMount"}
	target := i
	if pods > 0 {
		target = i % pods
	}
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s.%08x", l.podName(target), i), Namespace: l.podNS(target),
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Namespace: l.podNS(target), Name: l.podName(target),
			APIVersion: "v1", UID: types.UID(fmt.Sprintf("loadtest-pod-%06d", target)),
		},
		Reason:         reasons[i%len(reasons)],
		Message:        fmt.Sprintf("synthetic event %d for load testing the timeline pipeline", i),
		Type:           corev1.EventTypeNormal,
		Count:          int32(1 + i%5),
		Source:         corev1.EventSource{Component: "kubelet", Host: l.node(target)},
		FirstTimestamp: metav1.Time{Time: fixedClock},
		LastTimestamp:  metav1.Time{Time: fixedClock},
	}
}

func censusJob(l censusLayout, i int, image string) *batchv1.Job {
	one := int32(1)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%08d", l.cronJobName(i), i), Namespace: l.cronJobNS(i), Labels: l.labels(i),
		},
		Spec:   batchv1.JobSpec{Completions: &one, Parallelism: &one, Template: censusPodTemplate(l, i, image)},
		Status: batchv1.JobStatus{Succeeded: 1},
	}
	if l.cronJobs > 0 {
		job.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "batch/v1", Kind: "CronJob", Name: l.cronJobName(i),
			UID: l.cronJobUID(i), Controller: boolPtr(true),
		}}
	}
	return job
}

func censusCronJob(l censusLayout, i int, image string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: l.cronJobName(i), Namespace: l.cronJobNS(i), UID: l.cronJobUID(i), Labels: l.labels(i),
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "*/5 * * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{Template: censusPodTemplate(l, i, image)},
			},
		},
	}
}

func censusIngress(l censusLayout, i int) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	class := "alb"
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("app-%05d-ing-%d", l.app(i), i/l.apps), Namespace: l.ns(i), Labels: l.labels(i),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &class,
			Rules: []networkingv1.IngressRule{{
				Host: fmt.Sprintf("app-%05d.loadtest.example.com", l.app(i)),
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: fmt.Sprintf("app-%05d-svc-0", l.app(i)),
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
}

func censusStatefulSet(l censusLayout, i int, image string) *appsv1.StatefulSet {
	r := int32(2)
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("sts-%05d", i), Namespace: l.ns(i), Labels: l.labels(i),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &r,
			ServiceName: fmt.Sprintf("app-%05d-svc-0", l.app(i)),
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": l.appName(i)}},
			Template:    censusPodTemplate(l, i, image),
		},
		Status: appsv1.StatefulSetStatus{Replicas: r, ReadyReplicas: r},
	}
}

func censusDaemonSet(l censusLayout, i int, image string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("ds-%03d", i), Namespace: l.ns(i), Labels: l.labels(i),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": l.appName(i)}},
			Template: censusPodTemplate(l, i, image),
		},
		Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 248, NumberReady: 248},
	}
}

func censusHPA(l censusLayout, i int) *autoscalingv2.HorizontalPodAutoscaler {
	minR := int32(2)
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("app-%05d-hpa", l.app(i)), Namespace: l.ns(i), Labels: l.labels(i),
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1", Kind: "Deployment", Name: l.appName(i),
			},
			MinReplicas: &minR,
			MaxReplicas: 10,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 2, DesiredReplicas: 2},
	}
}

func censusPVC(l censusLayout, i int) *corev1.PersistentVolumeClaim {
	sc := "gp3"
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("pvc-%05d", i), Namespace: l.ns(i), Labels: l.labels(i),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &sc,
			VolumeName:       fmt.Sprintf("pv-%05d", i),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
}

func censusPV(i int) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pv-%05d", i), Labels: map[string]string{"loadtest": "true"}},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: "gp3",
			Capacity:         corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{Driver: "ebs.csi.aws.com", VolumeHandle: fmt.Sprintf("vol-%05d", i)},
			},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
}

func censusNetworkPolicy(l censusLayout, i int) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("netpol-%05d", i), Namespace: l.ns(i), Labels: l.labels(i),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": l.appName(i)}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"loadtest": "true"}},
				}},
			}},
		},
	}
}

func censusServiceAccount(l censusLayout, i int) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("sa-%04d", i), Namespace: l.ns(i), Labels: map[string]string{"loadtest": "true"},
		},
	}
}

func censusRole(l censusLayout, i int) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("role-%04d", i), Namespace: l.ns(i)},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""}, Resources: []string{"pods", "configmaps"}, Verbs: []string{"get", "list", "watch"},
		}},
	}
}

func censusRoleBinding(l censusLayout, i int) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rb-%04d", i), Namespace: l.ns(i)},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: fmt.Sprintf("role-%04d", i)},
		Subjects: []rbacv1.Subject{{
			Kind: "ServiceAccount", Name: fmt.Sprintf("sa-%04d", i), Namespace: l.ns(i),
		}},
	}
}

func censusClusterRole(i int) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("clusterrole-%04d", i)},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"},
		}},
	}
}

func censusClusterRoleBinding(i int) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("crb-%04d", i)},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: fmt.Sprintf("clusterrole-%04d", i)},
		Subjects: []rbacv1.Subject{{
			Kind: "ServiceAccount", Name: fmt.Sprintf("sa-%04d", i), Namespace: "loadtest-000",
		}},
	}
}

func censusPDB(l censusLayout, i int) *policyv1.PodDisruptionBudget {
	minAvail := intstr.FromInt(1)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pdb-%03d", i), Namespace: l.ns(i)},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvail,
			Selector:     &metav1.LabelSelector{MatchLabels: map[string]string{"app": l.appName(i)}},
		},
	}
}

func censusStorageClass(i int) *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: fmt.Sprintf("sc-%02d", i)},
		Provisioner: "ebs.csi.aws.com",
	}
}

func censusIngressClass(i int) *networkingv1.IngressClass {
	return &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("ingressclass-%02d", i)},
		Spec:       networkingv1.IngressClassSpec{Controller: "ingress.k8s.aws/alb"},
	}
}
