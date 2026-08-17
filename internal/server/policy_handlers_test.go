package server

import (
	"testing"

	"github.com/skyhook-io/radar/internal/k8s"
)

// Report producers are inconsistent about case across report families, so the
// vocabulary is normalized before anything branches on it.
func TestNormalizePolicyResult(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"fail", "fail"}, {"Fail", "fail"}, {"FAIL", "fail"},
		{"pass", "pass"}, {"Pass", "pass"},
		{"warn", "warn"}, {"error", "error"}, {"skip", "skip"},
		{"something-new", "something-new"},
	} {
		if got := normalizePolicyResult(c.in); got != c.want {
			t.Errorf("normalizePolicyResult(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Worst first, so a workload with one error among many warnings does not bury
// it. An unknown result sorts last rather than jumping the queue.
func TestPolicyResultRankOrdersWorstFirst(t *testing.T) {
	order := []string{"error", "fail", "warn", "skip", "mystery"}
	for i := 1; i < len(order); i++ {
		if policyResultRank(order[i-1]) >= policyResultRank(order[i]) {
			t.Errorf("%q should rank before %q", order[i-1], order[i])
		}
	}
	// Case must not change the ordering.
	if policyResultRank("FAIL") != policyResultRank("fail") {
		t.Error("ranking is case-sensitive")
	}
}

// Counts include passes even though passes are not listed: the count is what
// proves a check actually ran, which is the difference between "compliant" and
// "never examined".
func TestPolicyCountsIncludePassesThatAreNotListed(t *testing.T) {
	var c PolicyResourceCounts
	for _, r := range []string{"pass", "Pass", "fail", "warn", "error", "skip", "unknown"} {
		c.count(r)
	}
	if c.Pass != 2 || c.Fail != 1 || c.Warn != 1 || c.Error != 1 || c.Skip != 1 {
		t.Errorf("counts = %+v", c)
	}
	if !isPolicyPassResult("PASS") || isPolicyPassResult("fail") {
		t.Error("pass detection is wrong")
	}
}

// The two families do not share a resource name. Authorizing against only
// wgpolicyk8s.io's `policyreports` asks about a resource that is not served on
// an openreports-only cluster — which is where Kyverno 1.15+ writes — so the
// answer says nothing about whether the caller can see their policy results.
func TestPolicyReportFamiliesCarryTheirOwnResourceNames(t *testing.T) {
	byGroup := map[string]string{}
	for _, f := range k8s.PolicyReportFamilies {
		byGroup[f.Group] = f.Namespaced
	}
	if got := byGroup["wgpolicyk8s.io"]; got != "policyreports" {
		t.Errorf("wgpolicyk8s.io resource = %q, want policyreports", got)
	}
	if got := byGroup["openreports.io"]; got != "reports" {
		t.Errorf("openreports.io resource = %q, want reports — it is NOT policyreports", got)
	}
	if len(k8s.PolicyReportFamilies) != 2 {
		t.Errorf("expected both families to be authorized against, got %d", len(k8s.PolicyReportFamilies))
	}
}

// A cluster-scoped finding was read from a cluster-scoped report, and permission
// on the namespaced resource says nothing about it: `list policyreports`
// cluster-wide is a different grant from `list clusterpolicyreports`. Asking the
// namespaced question about a cluster-scoped subject can only answer the wrong
// one — in either direction.
func TestPolicyReportResourceFollowsSubjectScope(t *testing.T) {
	for _, f := range k8s.PolicyReportFamilies {
		if got := f.ResourceAt("kube-system"); got != f.Namespaced {
			t.Errorf("%s namespaced resource = %q, want %q", f.Group, got, f.Namespaced)
		}
		if got := f.ResourceAt(""); got != f.ClusterScoped {
			t.Errorf("%s cluster-scoped resource = %q, want %q", f.Group, got, f.ClusterScoped)
		}
		if f.Namespaced == f.ClusterScoped {
			t.Errorf("%s uses one resource name for both scopes", f.Group)
		}
	}
}

// The listed subjects are only the non-passing ones, so a rule's missing rows
// have two possible causes and the client cannot distinguish them from
// HiddenByFilter alone: it counts passing subjects too. Without the split, a
// rule whose only failure sits outside the namespace filter looks capped, and
// the UI offers to load rows that no higher limit will ever return.
func TestHiddenNotableSeparatesTheFilterFromTheCap(t *testing.T) {
	var bucket PolicyCoverageRule
	for _, result := range []string{"pass", "pass", "fail", "warn", "error", "skip"} {
		bucket.HiddenByFilter++
		if !isPolicyPassResult(result) {
			bucket.HiddenNotable++
		}
	}
	if bucket.HiddenByFilter != 6 {
		t.Errorf("HiddenByFilter = %d, want 6", bucket.HiddenByFilter)
	}
	// Every non-passing result counts: warn, error and skip are listed as rows
	// exactly like fail, so leaving any of them out re-creates the same gap.
	if bucket.HiddenNotable != 4 {
		t.Errorf("HiddenNotable = %d, want 4", bucket.HiddenNotable)
	}
}
