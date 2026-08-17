package policyreports

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func reportWithResults(namespace string, scope map[string]any, results ...map[string]any) *unstructured.Unstructured {
	raw := make([]any, 0, len(results))
	for _, r := range results {
		raw = append(raw, r)
	}
	obj := map[string]any{
		"apiVersion": "wgpolicyk8s.io/v1alpha2",
		"kind":       "PolicyReport",
		"metadata":   map[string]any{"name": "rep", "namespace": namespace},
		"results":    raw,
	}
	if scope != nil {
		obj["scope"] = scope
	}
	u := &unstructured.Unstructured{Object: obj}
	u.SetNamespace(namespace)
	return u
}

func podScope(name string) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "name": name}
}

func TestOutcomesForPolicyGroupsSubjects(t *testing.T) {
	idx := BuildIndex([]*unstructured.Unstructured{
		reportWithResults("team-a", podScope("web"),
			map[string]any{"policy": "require-limits", "rule": "check", "result": "fail", "source": "kyverno"},
		),
		reportWithResults("team-b", podScope("api"),
			map[string]any{"policy": "require-limits", "rule": "check", "result": "pass", "source": "kyverno"},
		),
	})

	got := idx.OutcomesForPolicy("require-limits")
	if len(got) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(got))
	}
	// Sorted by namespace, so team-a precedes team-b.
	if got[0].Subject.Namespace != "team-a" || got[0].Finding.Result != "fail" {
		t.Errorf("first outcome = %+v, want team-a/fail", got[0])
	}
	if got[1].Subject.Namespace != "team-b" || got[1].Finding.Result != "pass" {
		t.Errorf("second outcome = %+v, want team-b/pass", got[1])
	}
}

// A namespaced Kyverno Policy reports as "namespace/name". Storing it under a
// bare name would make the qualified lookup miss, which is a silent empty state
// on a policy that is working.
func TestOutcomesForPolicyKeepsQualifiedName(t *testing.T) {
	idx := BuildIndex([]*unstructured.Unstructured{
		reportWithResults("demo", podScope("web"),
			map[string]any{"policy": "demo/ns-require-resources", "result": "fail", "source": "KyvernoValidatingPolicy"},
		),
	})

	if got := idx.OutcomesForPolicy("ns-require-resources"); len(got) != 0 {
		t.Errorf("bare name matched a qualified policy: %+v", got)
	}
	if got := idx.OutcomesForPolicy("demo/ns-require-resources"); len(got) != 1 {
		t.Fatalf("qualified lookup returned %d outcomes, want 1", len(got))
	}
}

// Falco writes its detection source into `policy` and leaves no policy object
// behind; an entry with no policy name at all must not create a bucket keyed on
// the empty string, which would collect unrelated findings together.
func TestOutcomesForPolicySkipsUnnamedPolicies(t *testing.T) {
	idx := BuildIndex([]*unstructured.Unstructured{
		reportWithResults("demo", podScope("web"),
			map[string]any{"policy": "", "result": "fail"},
		),
	})
	if got := idx.OutcomesForPolicy(""); got != nil {
		t.Errorf("empty policy name produced outcomes: %+v", got)
	}
}

func TestOutcomesForPolicyReturnsDefensiveCopy(t *testing.T) {
	idx := BuildIndex([]*unstructured.Unstructured{
		reportWithResults("demo", podScope("web"),
			map[string]any{"policy": "p", "result": "fail"},
		),
	})
	first := idx.OutcomesForPolicy("p")
	first[0].Subject.Name = "mutated"
	second := idx.OutcomesForPolicy("p")
	if second[0].Subject.Name != "web" {
		t.Errorf("caller mutation leaked into the index: %q", second[0].Subject.Name)
	}
}

func TestOutcomesForPolicyNilIndex(t *testing.T) {
	var idx *Index
	if got := idx.OutcomesForPolicy("p"); got != nil {
		t.Errorf("nil index returned %+v", got)
	}
}

func TestBaseRuleFoldsAutogen(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		autogen bool
	}{
		{"validate-image-tag", "validate-image-tag", false},
		{"autogen-validate-image-tag", "validate-image-tag", true},
		{"autogen-cronjob-validate-image-tag", "validate-image-tag", true},
		{"", "", false},
		// Not an autogen prefix — a user may legitimately name a rule this way.
		{"autogenerate-things", "autogenerate-things", false},
	}
	for _, c := range cases {
		base, autogen := BaseRule(c.in)
		if base != c.want || autogen != c.autogen {
			t.Errorf("BaseRule(%q) = (%q, %v), want (%q, %v)", c.in, base, autogen, c.want, c.autogen)
		}
	}
}

// Provenance must not weaken the cross-family dedup. A cluster mid-migration
// from wgpolicyk8s.io to openreports.io serves the same result in both, and
// counting it twice inflates every violation total.
func TestDedupSurvivesProvenanceAndUnionsGroups(t *testing.T) {
	wg := reportWithResults("demo", podScope("web"),
		map[string]any{"policy": "p", "rule": "r", "result": "fail", "source": "kyverno", "message": "m"},
	)
	openReports := reportWithResults("demo", podScope("web"),
		map[string]any{"policy": "p", "rule": "r", "result": "fail", "source": "kyverno", "message": "m"},
	)
	openReports.Object["apiVersion"] = "openreports.io/v1alpha1"

	idx := BuildIndex([]*unstructured.Unstructured{wg, openReports})

	if got := idx.FindingsFor("", "Pod", "demo", "web"); len(got) != 1 {
		t.Fatalf("identical finding in two families produced %d findings, want 1", len(got))
	}
	outcomes := idx.OutcomesForPolicy("p")
	if len(outcomes) != 1 {
		t.Fatalf("OutcomesForPolicy returned %d, want 1", len(outcomes))
	}
	if len(outcomes[0].Groups) != 2 {
		t.Fatalf("groups = %v, want both families unioned onto the survivor", outcomes[0].Groups)
	}
}

// A finding present in only one family must carry only that family, so a caller
// authorized on the other cannot be served it.
func TestOutcomeCarriesOnlyItsOwnFamily(t *testing.T) {
	only := reportWithResults("demo", podScope("api"),
		map[string]any{"policy": "p", "result": "fail", "source": "kyverno"},
	)
	only.Object["apiVersion"] = "openreports.io/v1alpha1"

	outcomes := BuildIndex([]*unstructured.Unstructured{only}).OutcomesForPolicy("p")
	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(outcomes))
	}
	if len(outcomes[0].Groups) != 1 || outcomes[0].Groups[0] != "openreports.io" {
		t.Errorf("groups = %v, want [openreports.io]", outcomes[0].Groups)
	}
}

// The per-resource view and the per-policy view read the same index and must
// answer the same way about who may see what. FindingsFor drops the provenance
// entirely, so a caller of that function CANNOT filter — which is how the two
// views came to disagree.
func TestSourcedFindingsForCarriesProvenance(t *testing.T) {
	wg := reportWithResults("demo", podScope("web"),
		map[string]any{"policy": "require-labels", "result": "fail", "source": "kyverno"},
	)
	or := reportWithResults("demo", podScope("web"),
		map[string]any{"policy": "require-probes", "result": "fail", "source": "kyverno"},
	)
	or.Object["apiVersion"] = "openreports.io/v1alpha1"

	idx := BuildIndex([]*unstructured.Unstructured{wg, or})
	got := idx.SourcedFindingsFor("", "Pod", "demo", "web")
	if len(got) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(got))
	}

	byPolicy := map[string][]string{}
	for _, o := range got {
		if o.Subject.Name != "web" || o.Subject.Namespace != "demo" {
			t.Errorf("subject = %+v, want demo/web", o.Subject)
		}
		if len(o.Groups) == 0 {
			t.Errorf("outcome %q carries no report family — it cannot be authorized", o.Finding.Policy)
		}
		byPolicy[o.Finding.Policy] = o.Groups
	}
	if g := byPolicy["require-labels"]; len(g) != 1 || g[0] != "wgpolicyk8s.io" {
		t.Errorf("require-labels groups = %v, want [wgpolicyk8s.io]", g)
	}
	if g := byPolicy["require-probes"]; len(g) != 1 || g[0] != "openreports.io" {
		t.Errorf("require-probes groups = %v, want [openreports.io]", g)
	}

	if idx.SourcedFindingsFor("", "Pod", "demo", "missing") != nil {
		t.Error("unknown subject should return nil")
	}
}
