import { LookupFailureNote } from '@skyhook-io/k8s-ui/components/resources/renderers/LookupFailureNote'
import { VeleroRestoreRenderer as BaseVeleroRestoreRenderer } from '@skyhook-io/k8s-ui/components/resources/renderers/VeleroRestoreRenderer'
import type { ResourceRef } from '@skyhook-io/k8s-ui'
import { useVeleroRunMessages } from '../../../api/policy'

/**
 * Host wrapper adding the messages behind the run's error and warning counts.
 *
 * The counts are on the Restore; the text is not. It lives in a results file in
 * object storage that only Velero's controller can hand out a link to — so the
 * page showed a number an operator could worry about but not act on. A stuck
 * restore is the more urgent of the two kinds, because someone is waiting on
 * their data.
 *
 * Fetched on click, not on open: it creates a DownloadRequest and pulls an
 * object out of storage. Same bargain as the network trace's probes.
 */
export function VeleroRestoreRenderer({ data, onNavigate }: { data: any; onNavigate?: (ref: ResourceRef) => void }) {
  const namespace = data?.metadata?.namespace ?? ''
  const name = data?.metadata?.name ?? ''
  const fetcher = useVeleroRunMessages('restores', namespace, name)

  return (
    <BaseVeleroRestoreRenderer
      data={data}
      onNavigate={onNavigate}
      messages={{
        messages: fetcher.data,
        loading: fetcher.isPending,
        lookupNote: fetcher.error ? <LookupFailureNote errors={[fetcher.error]} what="the messages behind these counts" /> : undefined,
        onFetch: namespace && name ? () => fetcher.mutate() : undefined,
      }}
    />
  )
}
