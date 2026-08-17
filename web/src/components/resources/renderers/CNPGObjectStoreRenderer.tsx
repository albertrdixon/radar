import { Database } from 'lucide-react'
import { CNPGObjectStoreRenderer as BaseCNPGObjectStoreRenderer } from '@skyhook-io/k8s-ui/components/resources/renderers/CNPGObjectStoreRenderer'
import { Section, RelationshipGroup, CNPG_BARMAN_PLUGIN_NAME } from '@skyhook-io/k8s-ui'
import { LookupFailureNote } from '@skyhook-io/k8s-ui/components/resources/renderers/LookupFailureNote'
import type { ResourceRef } from '@skyhook-io/k8s-ui'
import { useResources } from '../../../api/client'

/**
 * Host wrapper adding the reverse lookup the package renderer cannot do: which
 * CNPG Clusters back up into this store.
 *
 * The Cluster page already links forward to its ObjectStore. Without this the
 * relationship is one-way, so an operator reading a recovery window keyed by
 * server name has no route to the cluster behind it.
 */
export function CNPGObjectStoreRenderer({
  data,
  onNavigate,
}: {
  data: any
  onNavigate?: (ref: ResourceRef) => void
}) {
  const namespace = data?.metadata?.namespace ?? ''
  const storeName = data?.metadata?.name ?? ''

  // Clusters reference their store by name within their own namespace, so the
  // search is namespace-scoped rather than cluster-wide.
  const clusters = useResources<any>('clusters', namespace, 'postgresql.cnpg.io', {
    enabled: !!storeName && !!namespace,
  })

  // Servers the store holds data for, from its own status. Deliberately a
  // separate question from the one below.
  const servers: string[] = Object.keys(data?.status?.serverRecoveryWindow ?? {})

  const users = (clusters.data ?? []).filter((c: any) => {
    const plugins = c?.spec?.plugins
    if (!Array.isArray(plugins)) return false
    return plugins.some(
      (p: any) =>
        p?.name === CNPG_BARMAN_PLUGIN_NAME &&
        p?.enabled !== false &&
        p?.parameters?.barmanObjectName === storeName,
    )
  })

  // A recovery window is keyed by the archive's server name, which defaults to
  // the cluster's name and is overridden by the plugin's own `serverName`
  // parameter — the ObjectStore rejects the field outright ("use the
  // 'serverName' plugin parameter in the Cluster resource"), so the Cluster is
  // the only place it can come from. Matching keys against cluster names alone
  // reports a cluster that renamed its archive as a stranger.
  const clusterForServer = new Map<string, string>()
  for (const c of users) {
    const plugin = (c?.spec?.plugins ?? []).find(
      (p: any) => p?.name === CNPG_BARMAN_PLUGIN_NAME && p?.parameters?.barmanObjectName === storeName,
    )
    const server = plugin?.parameters?.serverName || c?.metadata?.name
    if (server && c?.metadata?.name) clusterForServer.set(server, c.metadata.name)
  }

  // A recovery window is only advancing while WAL archiving works. The Cluster
  // owns that condition, and the ObjectStore's own status cannot see it — so a
  // server whose archiving has stopped reports its last successful backup and
  // reads as green here while the recovery point is frozen. The page whose job is
  // "how far back can I restore" is the last place that should have to be
  // cross-checked against another screen.
  const archivingFailing = new Set<string>()
  for (const c of users) {
    const stalled = (c?.status?.conditions ?? []).some(
      (cond: any) => cond?.type === 'ContinuousArchiving' && cond?.status === 'False',
    )
    if (!stalled) continue
    const plugin = (c?.spec?.plugins ?? []).find(
      (p: any) => p?.name === CNPG_BARMAN_PLUGIN_NAME && p?.parameters?.barmanObjectName === storeName,
    )
    const server = plugin?.parameters?.serverName || c?.metadata?.name
    if (server) archivingFailing.add(server)
  }

  // Named in the window, traceable to none of the clusters above — the gap the
  // note explains.
  const unlisted = servers.filter((s) => !clusterForServer.has(s))

  return (
    <>
      <BaseCNPGObjectStoreRenderer
        data={data}
        onNavigate={onNavigate}
        // Server key -> the Cluster that archives under it. Built from the
        // plugin parameters rather than by name, so a cluster that renamed its
        // archive still resolves. Undefined while the lookup is unresolved.
        clusterForServer={clusters.isLoading || clusters.error ? undefined : clusterForServer}
        archivingFailing={clusters.isLoading || clusters.error ? undefined : archivingFailing}
      />
      <Section title="Used By" icon={Database} defaultExpanded>
        {clusters.isLoading ? (
          <div className="text-sm text-theme-text-tertiary">Looking for clusters…</div>
        ) : users.length === 0 ? (
          // Not the same as "nothing uses it" — the caller may not be able to
          // list Clusters here, and a store with no users is worth noticing.
          clusters.error ? (
            <LookupFailureNote
              errors={[clusters.error]}
              what="which clusters use this store"
            />
          ) : (
            <div className="text-sm text-theme-text-tertiary">
              No cluster in this namespace backs up to this store.
            </div>
          )
        ) : (
          // The house pattern for "these other resources relate to this one":
          // labelled group with a count, ref badges, truncate-then-expand.
          <RelationshipGroup
            label="Archiving here through the plugin"
            refs={users.map((c: any) => ({
              kind: 'Cluster',
              namespace: c.metadata?.namespace ?? '',
              name: c.metadata?.name ?? '',
              group: 'postgresql.cnpg.io',
            }))}
            onNavigate={onNavigate}
          />
        )}
        {/* This lookup can only see `spec.plugins`. A cluster still on the
            deprecated in-tree `spec.backup.barmanObjectStore` names a
            destination path and never references this CR, so it CANNOT appear
            above however much data it has here. Read as blast radius — "who
            breaks if I rotate these credentials" — the list would silently omit
            every legacy-path cluster, which mid-migration is most of a fleet.
            The recovery window is the other half of the answer. */}
        {/* Only once the plugin lookup has actually answered. Derived while the
            list is still loading — or after it failed — every server in the
            window looks like an in-tree cluster, which is a claim about their
            configuration made from having no data. */}
        {!clusters.isLoading && !clusters.error && unlisted.length > 0 && (
          <div className="mt-2 pt-2 border-t border-theme-border text-xs text-theme-text-secondary">
            {/* Two causes reach this note and the screen cannot tell them
                apart, so it names both rather than asserting the one that is
                usually true: a cluster on the older in-tree settings never
                references this record, and a cluster that renamed its archive
                leaves the data it already wrote under the previous name. */}
            {`${unlisted.join(', ')} ${unlisted.length === 1 ? 'has' : 'have'} data here but ${
              unlisted.length === 1 ? 'is' : 'are'
            } not listed above. This finds clusters through the barman-cloud plugin, so an archive written by a cluster on the older in-tree backup settings — or under a server name it has since changed — is not traced back to one.`}
          </div>
        )}
      </Section>
    </>
  )
}
