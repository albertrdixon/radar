import { VeleroBSLRenderer as BaseVeleroBSLRenderer } from '@skyhook-io/k8s-ui/components/resources/renderers/VeleroBSLRenderer'
import { LookupFailureNote } from '@skyhook-io/k8s-ui/components/resources/renderers/LookupFailureNote'
import type { ResourceRef } from '@skyhook-io/k8s-ui'
import { useVeleroStoredBackups } from '../../../api/policy'

/**
 * Host wrapper adding the reverse lookup the package renderer cannot do: which
 * Backups this storage location holds.
 *
 * Without it the page states a phase and stops. "Unavailable" is a fact about a
 * bucket; what an operator came to find out is what it costs them, and that is
 * the list of backups they cannot restore from until it recovers — every one of
 * which Velero still reports as Completed.
 */
export function VeleroBSLRenderer({
  data,
  onNavigate,
}: {
  data: any
  onNavigate?: (ref: ResourceRef) => void
}) {
  const namespace = data?.metadata?.namespace ?? ''
  const name = data?.metadata?.name ?? ''
  const stored = useVeleroStoredBackups(namespace, name, !!namespace && !!name)

  return (
    <BaseVeleroBSLRenderer
      data={data}
      // Undefined while unresolved: an empty list would say this location holds
      // nothing, which is a different answer from not having looked yet.
      storedBackups={stored.isLoading || stored.error ? undefined : (stored.data?.backups ?? [])}
      storedTotal={stored.data?.stored}
      restorableTotal={stored.data?.restorable}
      expiredTotal={stored.data?.expired}
      listTruncated={stored.data?.truncated}
      lookupNote={
        stored.error ? (
          <LookupFailureNote errors={[stored.error]} what="which backups are stored here" />
        ) : undefined
      }
      onNavigate={onNavigate}
    />
  )
}
