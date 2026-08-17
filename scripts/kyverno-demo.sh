#!/usr/bin/env bash
# Bootstrap a kind cluster pre-populated with a curated set of Kyverno
# scenarios for visual-testing the policy UI. Idempotent — can be re-run to
# refresh state or apply fixture updates without recreating the cluster.
#
# Subcommands:
#   up            Create cluster (if missing), install Kyverno, apply fixtures.
#   down          Delete the kind cluster.
#   reset         down + up.
#   status        Inventory the policies, reports and report API groups.
#   openreports   Switch report output to openreports.io, leaving the
#                 wgpolicyk8s.io CRDs served but EMPTY. This is the
#                 report-family selection case: a naive "first served wins"
#                 picks the empty API and shows zero findings on a cluster
#                 full of them.
#   modern-only   Remove the legacy kyverno.io policy CRDs to reproduce the
#                 Kyverno 1.20 API surface. READ THE WARNING IT PRINTS.
#   queue         Queue a burst of UpdateRequests so the page has something on
#                 it. They are garbage-collected seconds after completing, so
#                 they cannot be a standing fixture. `queue clean` removes the
#                 probe and the 250 NetworkPolicies it generates.
#   help          Show this message.
#
# Prerequisites:
#   - kind         https://kind.sigs.k8s.io/
#   - kubectl
#   - helm
#
# Set CLUSTER_NAME=foo to use a different cluster (default: radar-kyverno-demo).
#
# See scripts/kyverno-demo/README.md for the coverage matrix and the
# 1.20-simulation gotcha.

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-radar-kyverno-demo}"
KUBECTL_CTX="kind-${CLUSTER_NAME}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURES_DIR="${SCRIPT_DIR}/kyverno-demo"

# Pinned so the demo behaves consistently. Chart 3.8.2 ships Kyverno 1.18.2,
# which is the first release where the modern policies.kyverno.io CEL family
# is stable AND the legacy family still exists — i.e. the migration state the
# integration is built for. Bump deliberately; several fixtures depend on
# 1.18-era spec shapes.
KYVERNO_CHART_VERSION="${KYVERNO_CHART_VERSION:-3.8.2}"

# Pretty colors for status output. Quietly turn off in non-interactive env.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_BLUE='\033[34m'; C_GREEN='\033[32m'; C_YELLOW='\033[33m'; C_RED='\033[31m'; C_DIM='\033[2m'; C_RESET='\033[0m'
else
  C_BLUE=''; C_GREEN=''; C_YELLOW=''; C_RED=''; C_DIM=''; C_RESET=''
fi

step()    { printf "${C_BLUE}==> %s${C_RESET}\n" "$1"; }
ok()      { printf "${C_GREEN}    ✓ %s${C_RESET}\n" "$1"; }
warn()    { printf "${C_YELLOW}    ! %s${C_RESET}\n" "$1"; }
fail()    { printf "${C_RED}    ✗ %s${C_RESET}\n" "$1"; exit 1; }
note()    { printf "${C_DIM}    %s${C_RESET}\n" "$1"; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required but not installed"
}

k() { kubectl --context "${KUBECTL_CTX}" "$@"; }

cluster_exists() {
  kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"
}

# --- Cluster lifecycle -----------------------------------------------------

cmd_up() {
  require_cmd kind
  require_cmd kubectl
  require_cmd helm

  if cluster_exists; then
    step "Cluster ${CLUSTER_NAME} already exists — reusing"
  else
    step "Creating kind cluster ${CLUSTER_NAME}"
    kind create cluster --name "${CLUSTER_NAME}" >/dev/null
    ok "Cluster created"
  fi
  k cluster-info >/dev/null || fail "kind context not reachable"

  install_kyverno
  apply_fixtures
  wait_for_reports
  cmd_status

  printf "\n"
  step "Run Radar against it"
  note "kubectl config use-context ${KUBECTL_CTX}"
  note "./scripts/visual-test-start.sh"
}

install_kyverno() {
  step "Installing Kyverno ${KYVERNO_CHART_VERSION}"
  helm repo add kyverno https://kyverno.github.io/kyverno/ >/dev/null 2>&1 || true
  helm repo update kyverno >/dev/null 2>&1 || true

  # policyExceptions is off by default; the exception fixtures need it on.
  # Single replicas keep a laptop-sized kind cluster responsive.
  helm upgrade --install kyverno kyverno/kyverno \
    --kube-context "${KUBECTL_CTX}" \
    --version "${KYVERNO_CHART_VERSION}" \
    -n kyverno --create-namespace \
    --set admissionController.replicas=1 \
    --set backgroundController.replicas=1 \
    --set reportsController.replicas=1 \
    --set cleanupController.replicas=1 \
    --set features.policyExceptions.enabled=true \
    --set features.policyExceptions.namespace="" \
    --wait --timeout 10m >/dev/null
  ok "Kyverno installed (all four controllers)"

  wait_for_webhook
}

# `helm --wait` returns once the Deployments report ready, which is NOT the
# same as the admission webhook accepting connections — the Service endpoints
# and the webhook's TLS cert land a little later. Applying a policy in that
# window fails with:
#   failed calling webhook "validate-policy.kyverno.svc": connection refused
# Wait for endpoints, then confirm with a real policy round-trip, because
# endpoint readiness alone still races the cert.
wait_for_webhook() {
  step "Waiting for the policy-validating webhook to accept connections"

  local i
  for i in $(seq 1 60); do
    if [ -n "$(k get endpoints kyverno-svc -n kyverno -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null)" ]; then
      break
    fi
    sleep 2
  done

  # Round-trip a throwaway policy until it sticks. Server-side dry-run still
  # goes through the webhook, so this proves the real path without leaving
  # anything behind.
  for i in $(seq 1 60); do
    if k apply --dry-run=server -f - >/dev/null 2>&1 <<'EOF'
apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: kyverno-demo-webhook-probe
spec:
  matchConstraints:
    resourceRules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        operations: ["CREATE"]
        resources: ["configmaps"]
  validations:
    - expression: "true"
      message: "probe"
EOF
    then
      ok "Webhook ready (after ${i} attempt(s))"
      return 0
    fi
    sleep 2
  done
  fail "Kyverno webhook never became ready — check 'kubectl -n kyverno get pods'"
}

# 250 ConfigMaps so `audit-owner-label` has more subjects than the coverage view
# lists at once, exercising its fold and truncation paths. Generated rather than
# checked in: 250 near-identical manifests is noise in a fixtures directory, and
# the count is the only thing that matters.
#
# BULK_SUBJECTS=0 skips them for a faster bootstrap.
seed_bulk_subjects() {
  local n="${BULK_SUBJECTS:-250}"
  [ "${n}" = "0" ] && { note "BULK_SUBJECTS=0 - skipping bulk ConfigMaps"; return 0; }

  step "Seeding ${n} ConfigMaps for the bulk-subject policy"
  {
    local i=1
    while [ "${i}" -le "${n}" ]; do
      printf -- '---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: bulk-%03d\n  namespace: policy-demo\n  labels:\n    demo: bulk\ndata:\n  note: "subject %d of %d for audit-owner-label"\n' "${i}" "${i}" "${n}"
      i=$((i + 1))
    done
  } | k apply -f - >/dev/null
  ok "${n} ConfigMaps created (labelled demo=bulk, deliberately missing owner)"
}

apply_fixtures() {
  step "Applying Kyverno demo fixtures"
  [ -d "${FIXTURES_DIR}" ] || fail "Fixtures dir not found: ${FIXTURES_DIR}"

  # Apply in number order: the namespace and its label must exist before the
  # policies that select on it, and the aggregated ClusterRole must exist
  # before the ClusterCleanupPolicy whose admission check depends on it.
  while IFS= read -r f; do
    if ! grep -q '^[[:space:]]*[^#[:space:]-]' "$f"; then
      note "skipping $(basename "$f") (placeholder / comments only)"
      continue
    fi
    note "applying $(basename "$f")"
    # Retry: the webhook can still blip under load even after the readiness
    # gate above, and a half-applied fixture set is worse than a slow one.
    # audit-require-labels warns on demo-web by design, so stderr noise here
    # is expected and not a failure.
    local attempt
    for attempt in 1 2 3 4 5; do
      if k apply -f "$f" >/dev/null 2>&1; then
        break
      fi
      if [ "$attempt" = 5 ]; then
        # Surface the real error on the final try rather than swallowing it.
        k apply -f "$f" >/dev/null || fail "could not apply $(basename "$f")"
      fi
      sleep 3
    done
  done < <(find "${FIXTURES_DIR}" -maxdepth 1 -type f -name '*.yaml' | sort)
seed_bulk_subjects
  trigger_generate_rule

  ok "Fixtures applied"
}

# Creates the ConfigMap that fires `legacy-generate-companion`, once the policy
# is actually intercepting ConfigMaps.
#
# Ordering is the whole point. The rule sets `skipBackgroundRequests: true`, so
# generation happens on admission and only on admission: a trigger created
# before Kyverno rebuilds its webhook is never looked at again. The failure is
# silent — a labelled ConfigMap, no NetworkPolicy, no event, no error.
trigger_generate_rule() {
  step "Triggering the legacy generate rule"

  k wait --for=condition=Ready clusterpolicy/legacy-generate-companion --timeout=120s >/dev/null 2>&1 \
    || warn "legacy-generate-companion did not report Ready — generation may not fire"

  # Deleted first so this is a CREATE, and the label is part of that CREATE —
  # labelling afterwards would rely on the rule also matching UPDATE.
  k -n policy-demo delete configmap needs-a-companion --ignore-not-found >/dev/null
  k apply -f - >/dev/null <<'YAML' || fail "could not create the generate trigger ConfigMap"
apiVersion: v1
kind: ConfigMap
metadata:
  name: needs-a-companion
  namespace: policy-demo
  labels:
    needs-companion: "true"
data:
  note: "triggers legacy-generate-companion, which generates needs-a-companion-companion"
YAML

  local i
  for i in $(seq 1 20); do
    if k -n policy-demo get networkpolicy needs-a-companion-companion >/dev/null 2>&1; then
      ok "needs-a-companion → generated needs-a-companion-companion"
      return 0
    fi
    sleep 3
  done
  warn "no NetworkPolicy was generated — the generate rule fired nothing"
}

wait_for_reports() {
  step "Waiting for the background scanner to produce PolicyReports"
  local n=0
  for _ in $(seq 1 30); do
    n=$(k get policyreports -A --no-headers 2>/dev/null | wc -l | tr -d ' ')
    [ "${n:-0}" -gt 5 ] && break
    sleep 10
  done
  if [ "${n:-0}" -gt 5 ]; then
    ok "${n} PolicyReports produced"
  else
    fail "only ${n:-0} PolicyReports after 5 minutes — check the reports controller"
  fi
}

cmd_down() {
  require_cmd kind
  if cluster_exists; then
    step "Deleting cluster ${CLUSTER_NAME}"
    kind delete cluster --name "${CLUSTER_NAME}" >/dev/null
    ok "Cluster deleted"
  else
    note "Cluster ${CLUSTER_NAME} does not exist"
  fi
}

cmd_reset() { cmd_down; cmd_up; }

# --- Scenario toggles ------------------------------------------------------

cmd_openreports() {
  require_cmd helm
  cluster_exists || fail "Cluster ${CLUSTER_NAME} does not exist — run 'up' first"

  step "Switching report output to openreports.io"
  helm upgrade kyverno kyverno/kyverno \
    --kube-context "${KUBECTL_CTX}" \
    --version "${KYVERNO_CHART_VERSION}" -n kyverno --reuse-values \
    --set openreports.enabled=true \
    --set openreports.installCrds=true \
    --no-hooks --wait --timeout 8m >/dev/null
  ok "openreports enabled"

  note "waiting for reports to migrate..."
  local wg=0 or=0 wg_policy=0 wg_cluster=0 or_policy=0 or_cluster=0
  for _ in $(seq 1 60); do
    wg_policy=$({ k get policyreports.wgpolicyk8s.io -A --no-headers 2>/dev/null || true; } | wc -l | tr -d ' ')
    wg_cluster=$({ k get clusterpolicyreports.wgpolicyk8s.io --no-headers 2>/dev/null || true; } | wc -l | tr -d ' ')
    or_policy=$({ k get reports.openreports.io -A --no-headers 2>/dev/null || true; } | wc -l | tr -d ' ')
    or_cluster=$({ k get clusterreports.openreports.io --no-headers 2>/dev/null || true; } | wc -l | tr -d ' ')
    wg=$((wg_policy + wg_cluster))
    or=$((or_policy + or_cluster))
    [ "${wg}" = "0" ] && [ "${or}" -gt 5 ] && break
    sleep 5
  done
  [ "${wg}" = "0" ] && [ "${or}" -gt 5 ] \
    || fail "report migration did not settle (wgpolicyk8s.io=${wg}, openreports.io=${or})"

  printf "\n"
  ok "wgpolicyk8s.io: ${wg} reports (CRDs still SERVED)"
  ok "openreports.io: ${or} reports"
  printf "\n"
  note "Both families are now served; only one holds data. Selection that"
  note "takes the first SERVED group picks the empty one and reports zero"
  note "findings. Radar probes each group for actual objects instead —"
  note "check the log for 'warming up N report CRDs from [openreports.io]'."
}

cmd_modern_only() {
  require_cmd helm
  cluster_exists || fail "Cluster ${CLUSTER_NAME} does not exist — run 'up' first"

  printf "\n"
  warn "READ THIS BEFORE THE CLUSTER LOOKS BROKEN:"
  note "Removing the legacy CRDs reproduces the Kyverno 1.20 API surface"
  note "faithfully, but Kyverno 1.18.2's ADMISSION CONTROLLER CRASHLOOPS in"
  note "that state — it sanity-checks for clusterpolicies.kyverno.io and"
  note "policies.kyverno.io at startup and exits when they are absent."
  note ""
  note "That is upstream behaviour, not a Radar bug and not a broken cluster."
  note "The reports controller stays healthy and the existing PolicyReports"
  note "survive, which is what the detection gate is tested against."
  note ""
  note "Run 'reset' to get a working cluster back."
  printf "\n"

  step "Removing legacy kyverno.io policy CRDs"
  # --no-hooks: the chart's post-upgrade migration job fails once the CRDs it
  # migrates are gone. Do not use --wait: the admission Deployment is expected
  # to crashloop after the legacy CRDs disappear.
  helm upgrade kyverno kyverno/kyverno \
    --kube-context "${KUBECTL_CTX}" \
    --version "${KYVERNO_CHART_VERSION}" -n kyverno --reuse-values \
    --set crds.groups.kyverno.clusterpolicies=false \
    --set crds.groups.kyverno.policies=false \
    --no-hooks >/dev/null

  printf "\n"
  # Helm returns as soon as the API server accepts the deletion; the CRDs then
  # go through finalizers and garbage collection of their CRs. Asserting the
  # count immediately reports "still present" on a machine where that takes
  # more than a moment, and the command fails while the state it is creating is
  # in fact arriving. Wait for the state, then assert it.
  local waited=0
  while [ "${waited}" -lt 60 ]; do
    if [ "$({ k get crd clusterpolicies.kyverno.io policies.kyverno.io --no-headers 2>/dev/null || true; } | wc -l | tr -d ' ')" = "0" ]; then
      break
    fi
    sleep 2
    waited=$((waited + 2))
  done

  local legacy modern reports
  legacy=$({ k get crd clusterpolicies.kyverno.io policies.kyverno.io --no-headers 2>/dev/null || true; } | wc -l | tr -d ' ')
  modern=$(k get crd -o jsonpath='{range .items[?(@.spec.group=="policies.kyverno.io")]}{.metadata.name}{"\n"}{end}' 2>/dev/null | wc -l | tr -d ' ')
  reports=$(k get policyreports -A --no-headers 2>/dev/null | wc -l | tr -d ' ')
  [ "${legacy}" = "0" ] || fail "${legacy} legacy policy CRD(s) still present"
  [ "${modern}" -gt 0 ] || fail "modern policies.kyverno.io CRDs disappeared"
  [ "${reports}" -gt 0 ] || fail "PolicyReports did not survive legacy CRD removal"
  ok "legacy ClusterPolicy CRD present: ${legacy} (expect 0)"
  ok "policies.kyverno.io CRDs: ${modern}"
  ok "PolicyReports surviving: ${reports}"
  printf "\n"
  note "Detection keyed only on the legacy family would now report"
  note "'not_installed' and drop the entire report index. Check Radar's log"
  note "for 'Kyverno detected' plus 'Index initialized with N subjects'."
}

# --- Status ----------------------------------------------------------------

cmd_status() {
  cluster_exists || fail "Cluster ${CLUSTER_NAME} does not exist — run 'up' first"

  printf "\n"
  step "Kyverno controllers"
  k get pods -n kyverno --no-headers 2>/dev/null \
    | grep -v migrate \
    | awk '{printf "    %-52s %s %s\n", $1, $2, $3}' || note "none"

  printf "\n"
  step "Modern policies (policies.kyverno.io)"
  for kind in validatingpolicies namespacedvalidatingpolicies imagevalidatingpolicies \
              mutatingpolicies generatingpolicies deletingpolicies namespaceddeletingpolicies; do
    printf "    %-34s %s\n" "$kind" \
      "$(k get "${kind}.policies.kyverno.io" -A --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  done

  printf "\n"
  step "Legacy policies (kyverno.io)"
  for kind in clusterpolicies clustercleanuppolicies; do
    printf "    %-34s %s\n" "$kind" \
      "$(k get "${kind}.kyverno.io" --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  done
  printf "    %-34s %s (modern) / %s (legacy)\n" "policyexceptions" \
    "$(k get policyexceptions.policies.kyverno.io -A --no-headers 2>/dev/null | wc -l | tr -d ' ')" \
    "$(k get policyexceptions.kyverno.io -A --no-headers 2>/dev/null | wc -l | tr -d ' ')"

  printf "\n"
  step "Reports"
  printf "    %-34s %s\n" "wgpolicyk8s.io policyreports" \
    "$(k get policyreports.wgpolicyk8s.io -A --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  printf "    %-34s %s\n" "wgpolicyk8s.io clusterpolicyreports" \
    "$(k get clusterpolicyreports.wgpolicyk8s.io --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  printf "    %-34s %s\n" "openreports.io reports" \
    "$(k get reports.openreports.io -A --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  printf "    %-34s %s\n" "openreports.io clusterreports" \
    "$(k get clusterreports.openreports.io --no-headers 2>/dev/null | wc -l | tr -d ' ')"

  printf "\n"
  step "Distinct results[].source values (the engine-taxonomy case)"
  local report_resources="policyreports.wgpolicyk8s.io,clusterpolicyreports.wgpolicyk8s.io"
  if k get crd reports.openreports.io clusterreports.openreports.io >/dev/null 2>&1; then
    report_resources="${report_resources},reports.openreports.io,clusterreports.openreports.io"
  fi
  k get "${report_resources}" -A -o json 2>/dev/null \
    | jq -r '[.items[].results[]?.source] | group_by(.) | map({s:.[0],n:length}) | sort_by(-.n) | .[] | "    \(.n)\t\(.s)"' 2>/dev/null \
    || note "jq not installed — skipping"
  note "One engine, several producer strings. Filtering on the raw value"
  note "fragments Kyverno across as many buckets as it has policy types."
}

# Applies the UpdateRequest probe, or removes it.
#
# `spec.plugins`-style standing fixtures do not work for UpdateRequests: Kyverno
# garbage-collects them within seconds of Completed. This produces a burst on
# demand so the page has something on it — the generate request stays Pending
# longest and is the one worth opening.
cmd_queue() {
  local probe="${FIXTURES_DIR}/queue-probe.yaml"
  [ -f "${probe}" ] || fail "probe not found: ${probe}"

  if [ "${1:-}" = "clean" ]; then
    step "Removing the UpdateRequest probe and everything it created"
    k delete -f "${probe}" --ignore-not-found >/dev/null
    k -n policy-demo delete networkpolicy -l generated-by=probe-generate-existing --ignore-not-found >/dev/null
    k -n policy-demo label configmap -l demo=bulk probe-mutated- >/dev/null 2>&1 || true
    ok "probe removed"
    return 0
  fi

  step "Queueing UpdateRequests"
  k apply -f "${probe}" >/dev/null || fail "could not apply the probe"

  local n=0 i
  for i in $(seq 1 20); do
    n=$(k get updaterequests.kyverno.io -A --no-headers 2>/dev/null | wc -l | tr -d ' ')
    [ "${n:-0}" -gt 0 ] && break
    sleep 2
  done
  if [ "${n:-0}" = "0" ]; then
    warn "no UpdateRequests appeared — the background controller may not be running"
    return 0
  fi
  ok "${n} UpdateRequest(s) queued"
  note "They are deleted seconds after they complete. Open one now:"
  note "  kubectl get updaterequests.kyverno.io -A"
  note "Run '$0 queue clean' to remove the probe and the 250 NetworkPolicies it makes."
}

cmd_help() {
  sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

case "${1:-help}" in
  up)          cmd_up          ;;
  down)        cmd_down        ;;
  reset)       cmd_reset       ;;
  status)      cmd_status      ;;
  openreports) cmd_openreports ;;
  modern-only) cmd_modern_only ;;
  queue)       cmd_queue "${2:-}" ;;
  help|-h|--help) cmd_help     ;;
  *)
    printf "${C_RED}Unknown subcommand: %s${C_RESET}\n\n" "$1"
    cmd_help
    exit 1
    ;;
esac
