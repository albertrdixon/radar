# Calico Demo Cluster

Bootstraps a `kind` cluster running real Calico with its aggregated API server,
plus the policy shapes Radar's Calico surfaces have to render correctly. Use it
for visual-testing the Calico renderers, the policy topology, and the dashboard's
network-policy coverage.

## Quick start

```bash
# Prerequisites: kind, kubectl
./scripts/calico-demo.sh up        # ~5 minutes on first run
./scripts/calico-demo.sh status    # inventory policies through both API groups

# Run Radar against it
kubectl config use-context kind-radar-calico-demo
./scripts/visual-test-start.sh

# When done
./scripts/calico-demo.sh down
```

## Two Calico behaviours worth knowing before you change anything

**Both API groups serve the same objects.** With the Calico API server installed,
`projectcalico.org/v3` (aggregated) and `crd.projectcalico.org/v1` (the CRDs
behind it) return the same stored policies. They are two views of one object, not
two objects. Anything that walks discovery and builds per (kind, group) will
produce a duplicate of every policy unless it deduplicates on kind, namespace and
name. The two groups are authorized independently, though, so a caller who can
list a policy under either group can genuinely read it — treat the groups as
alternatives when filtering by RBAC, not as separate identities.

The cluster is deliberately built with the `APIServer` CR applied. Without it only
`crd.projectcalico.org` is served, there is nothing to duplicate, and the
dual-group paths are never exercised.

**A staged deletion has no selector, and that does not mean "everything".** The
Calico API rejects any other spec field alongside `stagedAction: Delete`:

```
The StagedNetworkPolicy "db-lockdown" is invalid: StagedNetworkPolicySpec:
Invalid value: {}: Spec fields, except Tier, should all be zero-value if
stagedAction is Delete
```

An empty selector normally means "select every workload". On a staged deletion it
means the opposite: promoting it removes protection. `stagedAction` is one of
`Set`, `Delete`, `Learn`, `Ignore`, defaulting to `Set`; only `Set` and `Learn`
preview protection that would exist afterwards.

## What the fixtures cover

| Fixture | Why it is here |
|---|---|
| `core-web-ingress` (networking.k8s.io) | A core NetworkPolicy alongside Calico's identically-named kind. Catches anything that merges the two families. |
| `api-lockdown` | Equality selector, Allow and Deny rules, ports, an egress CIDR. |
| `sa-scoped` | `all()` endpoints narrowed by `serviceAccountSelector`. Evaluating only the endpoint selector over-covers the namespace. |
| `db-lockdown` | The only policy selecting `db`, so staging its deletion has something real to take away. |
| `prod-default-deny` | Set-membership selector plus `namespaceSelector`. `calico-demo-dev/dev-web` matches the endpoints but must be excluded by namespace. |
| `security.baseline` | Lives in a non-default tier, so it must be named `security.baseline`. Compound `&&` selector, `Pass` action. |
| `staged-web-tighten` | A staged `Set`: the one shape that really is a preview of protection. |
| `db-lockdown` (staged) | A staged `Delete` with the empty spec the API forces, named for the enforced policy it removes. Reading that as "all workloads" turns a removal into total coverage. |
| `security.baseline` (staged) | A staged deletion of the only policy covering `calico-demo-dev/dev-web`, so projected coverage must fall *below* today's. |
| `staged-global-audit` | Cluster-scoped staged `Set` with a `has()` selector. |
| `staged-k8s-agent` | `StagedKubernetesNetworkPolicy` carries the Kubernetes `podSelector` shape and raw pod labels, not Calico selector syntax and not Calico's automatic endpoint labels. |
| `tunnel-only-pool` | A disabled, tunnel-only IPPool next to the operator's default, so the pool list has two states to tell apart. |
| HostEndpoint | Named ports, expected IPs and profiles on a real node. |

## Coverage arithmetic

With the fixtures applied and no namespace filter, the dashboard should read
**10 of 14** workloads covered and **9 of 14** projected if the staged set were
promoted — a *decrease*, because the staged set removes more than it adds:

- `calico-demo/agent` is the one workload the staged set would newly cover
  (`staged-k8s-agent`).
- `calico-demo/db` loses its only cover (`db-lockdown` is staged for deletion).
- `calico-demo-dev/dev-web` loses its only cover (`security.baseline` is staged
  for deletion).

The other two staged policies select workloads that are already covered, so they
move nothing.

If the projected number ever comes out at or above the enforced one, something is
treating a staged deletion as protection.

## Limits

Single node, so nothing here exercises BGP peering, cross-node encapsulation, or
a HostEndpoint on a real uplink. Calico is genuinely enforcing policy, but the
fixtures are chosen for what Radar renders, not for testing Calico itself.
