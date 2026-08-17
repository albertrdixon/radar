# Kyverno Demo Cluster

Bootstraps a `kind` cluster with Kyverno 1.18.2 and a curated set of policy
fixtures covering the scenarios Radar's policy UI needs to render correctly.
Use it for visual-testing changes to the Kyverno renderers, the PolicyReport
pipeline, or the enforcement-posture computation — and for future work on
attributing an admission denial to the policy and rule that caused it, which
needs live admission and real policies rather than hand-patched states.

## Quick start

```bash
# Prerequisites: kind, kubectl, helm
./scripts/kyverno-demo.sh up        # ~4 minutes on first run
./scripts/kyverno-demo.sh status    # inventory policies, reports and sources

# Run Radar against it
kubectl config use-context kind-radar-kyverno-demo
./scripts/visual-test-start.sh

# When done
./scripts/kyverno-demo.sh down
```

## ⚠️ Read this before running `modern-only`

`./scripts/kyverno-demo.sh modern-only` removes the legacy `kyverno.io` policy
CRDs to reproduce the **Kyverno 1.20 API surface**, which is what the detection
gate exists for.

**Kyverno 1.18.2's admission controller crashloops in that state.** It
sanity-checks for `clusterpolicies.kyverno.io` and `policies.kyverno.io` at
startup and exits when they're absent:

```
sanity checks failed  error="failed to check CRD clusterpolicies.kyverno.io is
installed: customresourcedefinitions.apiextensions.k8s.io
\"clusterpolicies.kyverno.io\" not found"
```

This is **upstream behaviour, not a Radar bug and not a broken cluster.** The
reports controller stays healthy and the existing PolicyReports survive, which
is exactly what the detection gate needs to be tested against. Kyverno 1.20
presumably drops the check along with the CRDs; until it ships, this is the
closest faithful simulation available.

Run `./scripts/kyverno-demo.sh reset` to get a working cluster back.

## What's in the cluster

### Enforcement posture — the cases that motivated computing it

Radar computes an **effective** posture rather than echoing
`spec.validationActions`, because three fields interact. These four policies
are why:

| Resource | Renders | Why it matters |
|---|---|---|
| `require-run-as-nonroot` | `Deny` (red) | Deny + admission on. Genuinely blocks — the control case. |
| `audit-require-labels` | `Audit + Warn` (amber) | Deliberate posture; declared and effective agree. Warns live on `demo-web`. |
| `background-only-no-latest-tag` | `Background only` (**orange**) | **Declares `Deny`, blocks nothing** — `admission.enabled: false`. The discrepancy tier. |
| `implicit-deny-configmap-labels` | `Deny` (red) | **No `validationActions` at all**, which Kyverno treats as Deny. Undocumented upstream. |

Tone marks the *discrepancy*, not the label: `Background only` is orange on a
Deny-declaring policy and neutral on an Audit-declaring one, because skipping
admission loses nothing an audit policy promised.

Verify the two surprising ones by hand:

```bash
# Declares Deny, admission disabled → NOT blocked
kubectl run probe --image=nginx:latest -n default

# No validationActions at all → IS blocked
kubectl create configmap probe --from-literal=a=b -n policy-demo
# admission webhook "vpol.validate.kyverno.svc-fail" denied the request
```

The legacy family has the same problem in its own vocabulary, and
`spec.validationFailureAction` is not the answer to it. Three policies in
`policy-posture` isolate the ways that field misleads, all matching the one
labelled ConfigMap in that namespace:

| Resource | Renders | Why it matters |
|---|---|---|
| `posture-mixed-actions` | `Audit` | **Declares `Enforce` and rejects nothing.** Its validating rule overrides the action down to Audit and its only other rule mutates — a mutation failure is not a rejection, so the policy-wide `Enforce` governs nothing at all. |
| `posture-rules-disagree` | `Enforce`, with **no** consequence sentence | Two validating rules, opposite actions. The sentence is one claim about one failing count and cannot say which rule a failure came from, so it is withheld rather than guessed: `posture-subject` fails only the auditing rule and is in no danger. |
| `posture-unresolvable-context` | `Audit`, section open, **red** shield | Every result is `error` — a context entry names a ConfigMap that does not exist, so the rule never reaches a verdict. Nothing is checked and nothing is blocked, which is the state most easily mistaken for compliance. |

```bash
# Overridden to Audit by its own rule → NOT blocked, despite Enforce on the spec
kubectl create configmap probe -n policy-posture --from-literal=a=b \
  --dry-run=client -o yaml | kubectl label -f - --local -o yaml posture=subject a=present | kubectl apply -f -

# The rule that really does reject, in the policy next to it
kubectl create configmap probe2 -n policy-posture --from-literal=a=b \
  --dry-run=client -o yaml | kubectl label -f - --local -o yaml posture=subject | kubectl apply -f -
# admission webhook "validate.kyverno.svc-fail" denied the request
```

### The rest of the modern family

| Resource | Kind | What it exercises |
|---|---|---|
| `ns-require-resources` | NamespacedValidatingPolicy | Namespaced twin sharing its cluster-scoped renderer |
| `verify-signed-images` | ImageValidatingPolicy | Supply chain: cosign keyless attestors, notary certs, SBOM/SLSA attestations |
| `add-default-labels` | MutatingPolicy | `Mutates on admission` |
| `backfill-cost-center-existing` | MutatingPolicy | **`mutateExisting` with admission off** — used to read "Inactive" while rewriting the cluster |
| `generate-default-netpol` | GeneratingPolicy | Generation lifecycle: `generateExisting` / `synchronize` / `orphanDownstreamOnPolicyDelete` |
| `cleanup-completed-jobs` | DeletingPolicy | Six-hourly cron → renders `Never run` for a while |
| `probe-every-minute` | DeletingPolicy | Every-minute cron → flips to `43s ago` within ~2 min |
| `ns-cleanup-old-pods` | NamespacedDeletingPolicy | Namespaced twin |

The two DeletingPolicies exist as a pair on purpose: within two minutes of
bootstrap the table shows both `Never run` and a recent timestamp, which is
what the Last Run column exists to surface. No hand-patching required.

### Legacy family and the dual-API collision

| Resource | Kind | What it exercises |
|---|---|---|
| `legacy-disallow-latest-tag` | `kyverno.io` ClusterPolicy | Deprecated family still rendering; `Enforce`/`Audit` vocabulary |
| `legacy-generate-companion` | `kyverno.io` ClusterPolicy | A **generate** rule on the legacy kind. The family lives in the rule block, not the kind, so anything reading the kind alone calls its `pass` result "passing" — the validate vocabulary, on a rule that validates nothing. Trigger: a ConfigMap labelled `needs-companion=true` in `policy-demo`; target: a NetworkPolicy named after it. |
| `legacy-verify-image-signature` | `kyverno.io` ClusterPolicy | A **verifyImages** rule, where enforcement posture is not where it looks. Kyverno defaults `spec.validationFailureAction` to `Audit` when absent and writes the rule count under the all-lowercase key `verifyimages`, so a policy that rejects unsigned images reads as audit-only with no rules at all to anything trusting the obvious spellings. The action that governs it is `verifyImages[].failureAction`, and a rule-level action overrides the spec-level one rather than deferring to it. Matches only `docker.io/busybox*`, which no demo workload runs, so it blocks nothing during bootstrap — to see the rejection, create a busybox pod in `policy-demo` that satisfies `require-run-as-nonroot`. |
| `legacy-cleanup-completed-pods` | `kyverno.io` ClusterCleanupPolicy | Deprecated-but-deployed; needs the aggregated cleanup ClusterRole |
| `modern-exempt-monitoring` | `policies.kyverno.io` PolicyException | `policyRefs` + CEL `matchConditions` |
| `legacy-exempt-latest-tag` | `kyverno.io/v2` PolicyException | `exceptions[].policyName` + `ruleNames` + any/all match |

**Both API families are installed deliberately.** Family selection, the Kyverno
sidebar group collapsing two API groups, and the legacy-vs-modern renderer
split only have meaning with both present. `PolicyException` is the sharpest
case: same Kind, same plural, two groups, different spec shapes.

### The two working records

`UpdateRequest` and `EphemeralReport` are Kyverno's own bookkeeping, and neither
can be a standing fixture — both are deleted within seconds of finishing. What
they are worth is the state they hold while they exist: a generation that has
queued and never run, and a scan's findings before they reach a PolicyReport.

`./scripts/kyverno-demo.sh queue` produces a burst of UpdateRequests on demand;
`queue clean` removes the probe and the 250 NetworkPolicies it generates.
EphemeralReports need nothing — the background scanner regenerates them
continuously, so the list is never empty for long.

Three shapes make these easy to render blank, and all three were read off live
objects rather than the CRD schema:

| What | Reality |
|---|---|
| A **generate** UpdateRequest | `spec.resource` is `{}` and `spec.rule` is `""`. Every trigger is in `spec.ruleContext[].trigger`, and one request can hold hundreds. |
| A **mutate** UpdateRequest | The opposite: `spec.resource` and `spec.rule` are populated, and there is no `ruleContext` at all. One request per trigger. |
| An **EphemeralReport** | Findings live in `spec`, not `status`. `spec.owner` is present and **blank** — the subject is only in `metadata.ownerReferences` and the `audit.kyverno.io/resource.*` labels. Result timestamps are `{seconds, nanos}`, and `new Date()` on that is Invalid Date, not an error. |

### Reports and the engine taxonomy

The background scanner produces ~40 PolicyReports across the fixtures plus
kube-system's own workloads. `status` prints the distinct `results[].source`
values, which on a bootstrapped cluster looks like:

```
47  KyvernoValidatingPolicy
32  KyvernoMutatingPolicy
32  kyverno
 7  KyvernoGeneratingPolicy
```

**One engine, four producer strings.** `source` is a producer type, not an
engine — filtering on the raw value fragments Kyverno across as many buckets as
it has policy types, which is why the taxonomy normalizes it.

### The report-family selection case

```bash
./scripts/kyverno-demo.sh openreports
```

Enables `openreports.enabled`, which leaves the `wgpolicyk8s.io` CRDs **served
but empty** while every report lands in `openreports.io`. Selection that takes
the first *served* group picks the empty one and reports zero findings on a
cluster full of them — indistinguishable from a clean cluster. Radar probes
each group for actual objects instead; check the log for:

```
[policy-reports] warming up 2 report CRDs from [openreports.io] (probed 36 reports)
```

## Coverage this deliberately does not include

- **Enforce-blocked resources.** Kyverno writes reports for audit policies and
  background scans only, so a resource rejected at admission was never created
  and no report describes it. No fixture can produce one — it's a property of
  the PolicyReport data model.
- **A `Not Ready` policy.** Kyverno rejects uncompilable policies at admission,
  so a broken one can't be created. It also never populates
  `status.conditionStatus.ready` on DeletingPolicy.
- **A real Kyverno 1.20.** Unreleased; `modern-only` simulates the API surface.

## Subcommands

| Command | What it does |
|---|---|
| `up` | Create cluster if missing, install Kyverno, apply fixtures, wait for reports |
| `down` | Delete the cluster |
| `reset` | `down` + `up` |
| `status` | Inventory policies, reports, and distinct `source` values |
| `openreports` | Switch to `openreports.io`, leaving `wgpolicyk8s.io` served-but-empty |
| `modern-only` | Remove legacy CRDs to simulate 1.20 — **read the warning above** |

`make kyverno-demo`, `make kyverno-demo-down` and `make kyverno-demo-status`
wrap the common three.

Set `CLUSTER_NAME=foo` to use a different cluster name, or
`KYVERNO_CHART_VERSION=x.y.z` to pin a different chart. Several fixtures depend
on 1.18-era spec shapes, so bump the version deliberately.
