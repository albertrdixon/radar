import { useState } from 'react'
import { useLocation } from 'react-router-dom'
import { PolicyCoverageSection } from '@skyhook-io/k8s-ui/components/resources/renderers/PolicyCoverageSection'
import type { PolicyCoverageSubject, ResourceRef } from '@skyhook-io/k8s-ui'
import { usePolicyCoverage } from '../../../api/policy'

/** Mirrors maxPolicyCoverageSubjectsHard in internal/server/policy_handlers.go —
 *  asking for more than the server will send just returns the same list. */
const COVERAGE_MAX_SUBJECTS = 5000

/**
 * Host wrapper for the policy coverage section — the inverse lookup that answers
 * "which resources does this policy pass and fail".
 *
 * The package renderers take it as a slot rather than fetching it themselves, so
 * a library consumer that does not wire this endpoint keeps the original policy
 * drawer and nothing breaks.
 */
export function KyvernoPolicyCoverage({
  data,
  onNavigate,
}: {
  data: any
  onNavigate?: (ref: ResourceRef) => void
}) {
  const name = data?.metadata?.name ?? ''
  // A namespaced Kyverno Policy reports as "namespace/name"; the server tries
  // both shapes, so the namespace is passed through whenever the policy has one.
  const namespace = data?.metadata?.namespace ?? ''
  // The server bounds each rule's subject list so an ordinary drawer open stays
  // small. Asking for the rest raises that bound once, up to the server's own
  // ceiling — past which the response says what it could not send.
  const [limit, setLimit] = useState<number | undefined>(undefined)

  // The header's namespace filter is applied SERVER-side from session state, so
  // the same URL returns a different body once it changes and nothing in the
  // request distinguishes the two. Without it in the cache key, a policy opened
  // under "All namespaces" keeps serving that body after the filter narrows —
  // the view then shows other people's namespaces under your scope, and which
  // behaviour you get depends on where you navigated from.
  const viewFilter = useLocation().search
  const query = usePolicyCoverage(name, namespace || undefined, !!name, limit, viewFilter)

  return (
    <PolicyCoverageSection
      resource={data}
      data={query.data ?? null}
      loading={query.isLoading}
      error={query.error as Error | null}
      onLoadMore={limit ? undefined : () => setLimit(COVERAGE_MAX_SUBJECTS)}
      loadingMore={query.isFetching}
      onSelectSubject={
        onNavigate
          ? (subject: PolicyCoverageSubject) =>
              onNavigate({
                kind: subject.kind,
                namespace: subject.namespace ?? '',
                name: subject.name,
                group: subject.group,
              })
          : undefined
      }
    />
  )
}
