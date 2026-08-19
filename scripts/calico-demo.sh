#!/usr/bin/env bash
# Bootstrap a kind cluster running real Calico, pre-populated with the policy
# shapes Radar's Calico surfaces have to render correctly. Idempotent — re-run
# to apply fixture updates without recreating the cluster.
#
# Subcommands:
#   up      Create cluster (if missing), install Calico with its API server,
#           apply fixtures.
#   down    Delete the kind cluster.
#   status  Inventory what each fixture is doing, through both API groups.
#   help    Show this message.
#
# Prerequisites:
#   - kind         https://kind.sigs.k8s.io/
#   - kubectl
#
# Set CLUSTER_NAME=foo to use a different cluster (default: radar-calico-demo).
#
# See scripts/calico-demo/README.md for the coverage matrix and the two Calico
# behaviours that are easy to get wrong without a cluster to check against.

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-radar-calico-demo}"
KUBECTL_CTX="kind-${CLUSTER_NAME}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURES_DIR="${SCRIPT_DIR}/calico-demo"

# Pinned so the demo behaves consistently. Calico moved its operator CRDs into
# a separate manifest in v3.30, and the API server's namespace has changed
# across releases, so both are version-sensitive. Bump deliberately.
CALICO_VERSION="${CALICO_VERSION:-v3.32.1}"
CALICO_MANIFESTS="https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests"

log() { echo "[calico-demo] $*"; }

require() {
  command -v "$1" >/dev/null 2>&1 || { echo "error: $1 is required" >&2; exit 1; }
}

create_cluster() {
  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
    log "cluster $CLUSTER_NAME already exists"
    return
  fi
  log "creating kind cluster $CLUSTER_NAME with the default CNI disabled"
  # Calico is the CNI here, so kindnet must not be installed. The pod subnet is
  # Calico's own default, which keeps the operator's default IPPool honest.
  cat <<EOF | kind create cluster --name "$CLUSTER_NAME" --config -
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  disableDefaultCNI: true
  podSubnet: "192.168.0.0/16"
nodes:
  - role: control-plane
EOF
}

install_calico() {
  log "installing Calico $CALICO_VERSION"
  # From v3.30 the operator's own CRDs ship separately from the operator
  # Deployment. Applying only tigera-operator.yaml leaves Installation and
  # APIServer unresolvable ("no matches for kind").
  kubectl apply --server-side --force-conflicts -f "${CALICO_MANIFESTS}/operator-crds.yaml"
  kubectl apply --server-side --force-conflicts -f "${CALICO_MANIFESTS}/tigera-operator.yaml"
  kubectl -n tigera-operator rollout status deploy/tigera-operator --timeout=300s

  # The APIServer CR is what makes projectcalico.org available alongside the
  # crd.projectcalico.org CRDs. Both groups then serve the same stored objects,
  # which is the case Radar's Calico handling exists for — without it the demo
  # cannot exercise the dual-group paths at all.
  kubectl apply -f - <<'EOF'
apiVersion: operator.tigera.io/v1
kind: Installation
metadata:
  name: default
spec:
  calicoNetwork:
    ipPools:
      - name: default-ipv4-ippool
        blockSize: 26
        cidr: 192.168.0.0/16
        encapsulation: VXLANCrossSubnet
        natOutgoing: Enabled
        nodeSelector: all()
---
apiVersion: operator.tigera.io/v1
kind: APIServer
metadata:
  name: default
spec: {}
EOF

  log "waiting for calico-system (the slow part, a few minutes on first run)"
  # The operator creates the namespace and workloads asynchronously, so wait for
  # them to exist before waiting on their readiness.
  for _ in $(seq 1 60); do
    kubectl -n calico-system get ds/calico-node >/dev/null 2>&1 && break
    sleep 5
  done
  kubectl -n calico-system rollout status ds/calico-node --timeout=600s
  kubectl wait --for=condition=Available tigerastatus/calico --timeout=600s

  # On a single node one calico-apiserver replica stays Pending forever (pod
  # anti-affinity), so a rollout wait never returns. One ready replica serves.
  for _ in $(seq 1 60); do
    local ready
    ready="$(kubectl -n calico-system get deploy/calico-apiserver -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)"
    [ -n "$ready" ] && [ "$ready" -ge 1 ] && break
    sleep 5
  done
  kubectl wait --for=condition=Available tigerastatus/apiserver --timeout=300s
}

apply_fixtures() {
  log "applying workloads and the core Kubernetes NetworkPolicy"
  kubectl apply -f "${FIXTURES_DIR}/01-namespaces-workloads.yaml"

  log "applying Calico tiers and enforced policies"
  kubectl apply -f "${FIXTURES_DIR}/02-tiers-policies.yaml"

  log "applying staged policies"
  kubectl apply -f "${FIXTURES_DIR}/03-staged-policies.yaml"

  log "applying IP pools and the host endpoint"
  # The HostEndpoint names a real node and its address, so it is templated here
  # rather than checked in with a node name that only exists on one machine.
  local node address
  node="$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')"
  address="$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')"
  sed -e "s|__NODE__|${node}|g" -e "s|__NODE_IP__|${address}|g" \
    "${FIXTURES_DIR}/04-infrastructure.yaml" | kubectl apply -f -
}

cmd_up() {
  require kind
  require kubectl
  create_cluster
  kubectl config use-context "$KUBECTL_CTX" >/dev/null
  install_calico
  apply_fixtures
  echo
  log "ready. Point Radar at it:"
  echo "    kubectl config use-context $KUBECTL_CTX"
  echo "    ./scripts/visual-test-start.sh"
}

cmd_down() {
  require kind
  log "deleting cluster $CLUSTER_NAME"
  kind delete cluster --name "$CLUSTER_NAME"
}

cmd_status() {
  require kubectl
  kubectl config use-context "$KUBECTL_CTX" >/dev/null

  echo "== API groups served"
  for group in projectcalico.org crd.projectcalico.org; do
    printf '   %-24s %s resources\n' "$group" \
      "$(kubectl api-resources --api-group="$group" --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  done

  echo
  echo "== the same policies, through each group (they are one stored object, not two)"
  echo "-- projectcalico.org"
  kubectl get networkpolicies.projectcalico.org -A --no-headers 2>/dev/null | awk '{print "   " $1 "/" $2}'
  echo "-- crd.projectcalico.org"
  kubectl get networkpolicies.crd.projectcalico.org -A --no-headers 2>/dev/null | awk '{print "   " $1 "/" $2}'

  echo
  echo "== core Kubernetes NetworkPolicies (a separate family, never merged with the above)"
  kubectl get networkpolicies.networking.k8s.io -A --no-headers 2>/dev/null | awk '{print "   " $1 "/" $2}'

  echo
  echo "== staged policies and what each one stages"
  kubectl get stagednetworkpolicies.projectcalico.org,stagedglobalnetworkpolicies.projectcalico.org,stagedkubernetesnetworkpolicies.projectcalico.org \
    -A -o jsonpath='{range .items[*]}   {.kind}{"\t"}{.metadata.namespace}{"/"}{.metadata.name}{"\t"}stagedAction={.spec.stagedAction}{"\n"}{end}' 2>/dev/null

  echo
  echo "== global policies, tiers, IP pools, host endpoints"
  kubectl get globalnetworkpolicies.projectcalico.org tiers.projectcalico.org \
    ippools.projectcalico.org hostendpoints.projectcalico.org --no-headers 2>/dev/null | awk '{print "   " $0}'
}

case "${1:-up}" in
  up) cmd_up ;;
  down) cmd_down ;;
  status) cmd_status ;;
  help|-h|--help) sed -n '2,21p' "$0" | sed 's|^# \?||' ;;
  *) echo "unknown subcommand: $1" >&2; sed -n '2,21p' "$0" | sed 's|^# \?||'; exit 1 ;;
esac
