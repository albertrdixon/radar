package server

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// The resource list views block themselves client-side once the row count
// crosses their guard (web/src/components/resources/ResourcesView.tsx):
// 25k for kinds served raw (Events, EndpointSlices), 50k for the kinds the
// server slims with ?include=summary (Pods, ReplicaSets). The raw guard
// exists because full objects are heavy: a production-shaped pod serializes
// to several KB, ~95% of it in subtrees the table never reads (env, volumes,
// probes, tolerations), so a guard-scale raw response is hundreds of MB
// decoded — gzip helps the wire, not the JSON.parse or the tab's heap.
//
// These tests are the executable form of that sizing story, at both guards:
// the raw test FAILS THE DAY ITS ANSWER FLIPS — if a guard-scale raw list
// ever fits the browser budget, the guard (and the test) must be revisited,
// not left to rot — and the summary test pins the payload contract that made
// raising the Pods/ReplicaSets guard safe in the first place.
const (
	// Mirrors LARGE_RESOURCE_LIST_LIMIT in web/src/components/resources/ResourcesView.tsx.
	largeResourceListGuardLimit = 25000
	// Mirrors SUMMARY_LIST_LIMIT there — the guard for summary-served kinds.
	summaryListGuardLimit = 50000

	// A single JSON list response beyond this is browser-hostile: main-thread
	// parse takes seconds and the resulting object graph multiplies in heap.
	browserPayloadBudgetBytes = 50 << 20

	// Per-row ceiling for summary rows, measured against a deliberately
	// pessimistic fixture (~2.4KB: every container restarted so lastState is
	// populated throughout, GPU requests, checksum annotations). Typical rows
	// are ~half this; at the 50k guard the decoded payload lands ~60–115MB
	// depending on crash-richness. A row growing past the ceiling means a
	// heavy subtree leaked back into the keep-list.
	summaryRowBudgetBytes = 3072
)

// realisticListPod mirrors what the informer cache actually holds for a
// production pod after field stripping (no managedFields, no last-applied):
// two app containers plus an init container, real env/probe/volume/toleration
// weight, and full statuses. Lean synthetic pods understate list payloads
// by an order of magnitude, which is exactly the mistake that made the raw
// list look affordable.
func realisticListPod(i int) *corev1.Pod {
	ns := fmt.Sprintf("team-%02d", i%40)
	name := fmt.Sprintf("checkout-api-6d9f7b9c4d-%05d", i)
	created := metav1.NewTime(time.Unix(1750000000+int64(i%86400), 0))
	started := metav1.NewTime(created.Add(7 * time.Second))
	envs := make([]corev1.EnvVar, 0, 14)
	for e := 0; e < 11; e++ {
		envs = append(envs, corev1.EnvVar{
			Name:  fmt.Sprintf("CHECKOUT_SETTING_%d", e),
			Value: fmt.Sprintf("https://internal.svc.cluster.local:84%02d/path/v2?feature=enabled", e),
		})
	}
	envs = append(envs,
		corev1.EnvVar{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "metadata.name"}}},
		corev1.EnvVar{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "checkout-db"}, Key: "password"}}},
		corev1.EnvVar{Name: "FEATURE_FLAGS", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "checkout-flags"}, Key: "flags"}}},
	)
	probe := func(path string, port int) *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(int32(port)), Scheme: corev1.URISchemeHTTP}},
			InitialDelaySeconds: 10, TimeoutSeconds: 3, PeriodSeconds: 15, SuccessThreshold: 1, FailureThreshold: 3,
		}
	}
	requests := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("512Mi")}
	limits := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")}
	mounts := []corev1.VolumeMount{
		{Name: "config", MountPath: "/etc/checkout", ReadOnly: true},
		{Name: "tls-certs", MountPath: "/etc/tls", ReadOnly: true},
		{Name: "cache", MountPath: "/var/cache/checkout"},
		{Name: "kube-api-access-x7k2m", MountPath: "/var/run/secrets/kubernetes.io/serviceaccount", ReadOnly: true},
	}
	container := func(cname, image string, port int) corev1.Container {
		return corev1.Container{
			Name:  cname,
			Image: image,
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: int32(port), Protocol: corev1.ProtocolTCP}},
			Env:   envs,
			EnvFrom: []corev1.EnvFromSource{
				{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "checkout-common"}}},
			},
			Resources:                corev1.ResourceRequirements{Requests: requests, Limits: limits},
			VolumeMounts:             mounts,
			LivenessProbe:            probe("/healthz", port),
			ReadinessProbe:           probe("/readyz", port),
			StartupProbe:             probe("/healthz", port),
			Lifecycle:                &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{Command: []string{"/bin/sh", "-c", "sleep 5"}}}},
			TerminationMessagePath:   "/dev/termination-log",
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: boolPtr(false),
				ReadOnlyRootFilesystem:   boolPtr(true),
				RunAsNonRoot:             boolPtr(true),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
		}
	}
	containerStatus := func(cname, image string, restarts int32) corev1.ContainerStatus {
		return corev1.ContainerStatus{
			Name:         cname,
			Ready:        true,
			Started:      boolPtr(true),
			RestartCount: restarts,
			Image:        image,
			ImageID:      "registry.example.com/shop/" + cname + "@sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			ContainerID:  fmt.Sprintf("containerd://%064d", i),
			State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "Error", ExitCode: 137, StartedAt: created, FinishedAt: started,
				ContainerID: fmt.Sprintf("containerd://%064d", i-1),
			}},
		}
	}
	cond := func(t corev1.PodConditionType) corev1.PodCondition {
		return corev1.PodCondition{Type: t, Status: corev1.ConditionTrue, LastTransitionTime: started}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         ns,
			UID:               types.UID(fmt.Sprintf("6a1d3f0e-4b2c-4d5e-8f9a-%012d", i)),
			ResourceVersion:   fmt.Sprintf("%d", 900000000+i),
			Generation:        1,
			CreationTimestamp: created,
			Labels: map[string]string{
				"app": "checkout-api", "app.kubernetes.io/name": "checkout-api",
				"app.kubernetes.io/instance": "checkout-api-prod", "app.kubernetes.io/version": "2.14.3",
				"pod-template-hash": "6d9f7b9c4d", "team": "payments",
			},
			Annotations: map[string]string{
				"checksum/config":                                "8f43b1c2a9d7e6f5checksum0a1b2c3d4e5f66778899aabbccddeeff00112233",
				"prometheus.io/scrape":                           "true",
				"prometheus.io/port":                             "9090",
				"cluster-autoscaler.kubernetes.io/safe-to-evict": "true",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "checkout-api-6d9f7b9c4d",
				UID: "0e2f4a6c-8b1d-4e3f-9a5c-7d0e1f2a3b4c", Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
			}},
		},
		Spec: corev1.PodSpec{
			NodeName:           fmt.Sprintf("ip-10-42-%d-%d.eu-west-1.compute.internal", i%64, i%250),
			ServiceAccountName: "checkout-api",
			NodeSelector:       map[string]string{"kubernetes.io/arch": "amd64", "node.kubernetes.io/lifecycle": "on-demand"},
			Priority:           int32Ptr(0),
			DNSPolicy:          corev1.DNSClusterFirst,
			RestartPolicy:      corev1.RestartPolicyAlways,
			SchedulerName:      "default-scheduler",
			Tolerations: []corev1.Toleration{
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute, TolerationSeconds: int64Ptr(300)},
				{Key: "node.kubernetes.io/unreachable", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute, TolerationSeconds: int64Ptr(300)},
				{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "payments", Effect: corev1.TaintEffectNoSchedule},
			},
			Affinity: &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout-api"}},
						TopologyKey:   "kubernetes.io/hostname",
					},
				}},
			}},
			Volumes: []corev1.Volume{
				{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "checkout-config"}, DefaultMode: int32Ptr(420)}}},
				{Name: "tls-certs", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "checkout-tls", DefaultMode: int32Ptr(420)}}},
				{Name: "cache", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{Name: "kube-api-access-x7k2m", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
					DefaultMode: int32Ptr(420),
					Sources: []corev1.VolumeProjection{
						{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{ExpirationSeconds: int64Ptr(3607), Path: "token"}},
						{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "kube-root-ca.crt"}, Items: []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}}}},
					},
				}}},
			},
			InitContainers: []corev1.Container{container("run-migrations", "registry.example.com/shop/checkout-migrate:2.14.3", 8080)},
			Containers: []corev1.Container{
				container("checkout-api", "registry.example.com/shop/checkout-api:2.14.3", 8080),
				container("envoy-sidecar", "registry.example.com/infra/envoy:1.29.4", 15000),
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				cond(corev1.PodScheduled), cond(corev1.PodInitialized),
				cond(corev1.ContainersReady), cond(corev1.PodReady),
			},
			HostIP:    fmt.Sprintf("10.42.%d.%d", i%64, i%250),
			HostIPs:   []corev1.HostIP{{IP: fmt.Sprintf("10.42.%d.%d", i%64, i%250)}},
			PodIP:     fmt.Sprintf("10.44.%d.%d", i%250, (i/250)%250),
			PodIPs:    []corev1.PodIP{{IP: fmt.Sprintf("10.44.%d.%d", i%250, (i/250)%250)}},
			StartTime: &started,
			QOSClass:  corev1.PodQOSBurstable,
			InitContainerStatuses: []corev1.ContainerStatus{{
				Name: "run-migrations", Ready: true, RestartCount: 0,
				Image:   "registry.example.com/shop/checkout-migrate:2.14.3",
				ImageID: "registry.example.com/shop/checkout-migrate@sha256:aa86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					Reason: "Completed", ExitCode: 0, StartedAt: created, FinishedAt: started,
				}},
			}},
			ContainerStatuses: []corev1.ContainerStatus{
				containerStatus("checkout-api", "registry.example.com/shop/checkout-api:2.14.3", int32(i%4)),
				containerStatus("envoy-sidecar", "registry.example.com/infra/envoy:1.29.4", 0),
			},
		},
	}
}

func TestRawPodListPayloadStillJustifiesLargeListGuard(t *testing.T) {
	// 1/10 guard scale keeps the test fast and small; payload extrapolates
	// linearly because rows are homogeneous.
	const n = largeResourceListGuardLimit / 10
	pods := make([]*corev1.Pod, 0, n)
	for i := 0; i < n; i++ {
		pods = append(pods, realisticListPod(i))
	}

	rec := httptest.NewRecorder()
	start := time.Now()
	(&Server{}).writeJSON(rec, pods)
	encodeDuration := time.Since(start)

	gotBytes := rec.Body.Len()
	perRow := gotBytes / n
	atGuardScale := int64(perRow) * largeResourceListGuardLimit
	t.Logf("raw pod list: %d rows = %.1fMB (%d bytes/row), encoded in %v; extrapolated to %d rows = %.0fMB",
		n, float64(gotBytes)/(1<<20), perRow, encodeDuration, largeResourceListGuardLimit, float64(atGuardScale)/(1<<20))

	if atGuardScale < browserPayloadBudgetBytes {
		t.Fatalf("a guard-scale raw pod list is now %.0fMB (< %.0fMB budget): the LARGE_RESOURCE_LIST_LIMIT guard no longer protects anything — revisit or remove it (web/src/components/resources/ResourcesView.tsx)",
			float64(atGuardScale)/(1<<20), float64(browserPayloadBudgetBytes)/(1<<20))
	}
}

// The summary counterpart: the same pods through summarizeTypedList must stay
// several-fold smaller than raw, and each row must stay under its byte
// ceiling — that pair is what makes the 50k summary guard honest.
func TestSummaryPodListPayloadStaysWithinRowBudget(t *testing.T) {
	const n = summaryListGuardLimit / 20
	pods := make([]*corev1.Pod, 0, n)
	for i := 0; i < n; i++ {
		pods = append(pods, realisticListPod(i))
	}

	rawRec := httptest.NewRecorder()
	(&Server{}).writeJSON(rawRec, pods)
	rawBytes := rawRec.Body.Len()

	sumRec := httptest.NewRecorder()
	start := time.Now()
	(&Server{}).writeJSON(sumRec, summarizeTypedList("pods", pods))
	encodeDuration := time.Since(start)

	sumBytes := sumRec.Body.Len()
	perRow := sumBytes / n
	atGuardScale := int64(perRow) * summaryListGuardLimit
	t.Logf("summary pod list: %d rows = %.1fMB (%d bytes/row), encoded in %v; extrapolated to %d rows = %.0fMB (raw is %.1fx bigger)",
		n, float64(sumBytes)/(1<<20), perRow, encodeDuration, summaryListGuardLimit, float64(atGuardScale)/(1<<20), float64(rawBytes)/float64(sumBytes))

	if perRow > summaryRowBudgetBytes {
		t.Fatalf("summary pod rows grew to %d bytes (> %d budget) — a heavy subtree leaked back into the keep-list, or the 50k guard needs revisiting", perRow, summaryRowBudgetBytes)
	}
	if sumBytes*4 > rawBytes {
		t.Fatalf("summary is only %.1fx smaller than raw (want ≥4x) — the strip no longer pays for itself", float64(rawBytes)/float64(sumBytes))
	}
}

func int32Ptr(v int32) *int32 { return &v }
func int64Ptr(v int64) *int64 { return &v }
