import { Boxes } from 'lucide-react'
import { CNPGImageCatalogRenderer as BaseImageCatalog } from '@skyhook-io/k8s-ui/components/resources/renderers/CNPGDeclarativeRenderer'
import { Section, RelationshipGroup } from '@skyhook-io/k8s-ui'
import { LookupFailureNote } from '@skyhook-io/k8s-ui/components/resources/renderers/LookupFailureNote'
import { getCNPGImageCatalogEntries } from '../resource-utils-cnpg'
import type { ResourceRef } from '@skyhook-io/k8s-ui'
import { useCNPGCatalogUsers } from '../../../api/policy'

const CNPG_GROUP = 'postgresql.cnpg.io'

/**
 * Host wrapper adding the reverse lookup: which clusters are pinned to this
 * catalog.
 *
 * This is the diagnosis path. A cluster that references a catalog lacking its
 * major version reports "incomplete or invalid image catalog" and stops; the
 * catalog is where you check what it offers, and from here you need to see who
 * depends on it before changing anything.
 */
export function CNPGImageCatalogRenderer({
  data,
  onNavigate,
}: {
  data: any
  onNavigate?: (ref: ResourceRef) => void
}) {
  const kind = data?.kind
  const name = data?.metadata?.name ?? ''
  const namespace = data?.metadata?.namespace ?? ''
  // A ClusterImageCatalog is cluster-scoped and referenceable from any namespace,
  // so the answer's scope is not this resource's. Read server-side rather than
  // filtering a client-side list: omitting the namespace there means "the
  // caller's namespace view filter", which would answer a question about the
  // whole cluster from whichever namespaces they happen to be showing.
  const scoped = kind === 'ImageCatalog'
  const clusters = useCNPGCatalogUsers(name, scoped ? namespace : '', !!name)

  const users = clusters.data?.clusters ?? []

  // A cluster asking for a major this catalog does not carry is the failure the
  // catalog page exists to explain, and it is invisible from the cluster side —
  // there the reference looks fine.
  const majors = new Set(getCNPGImageCatalogEntries(data).map((e) => e.major))
  const unmet = users.filter((c) => !majors.has(c.major as number))
  const met = users.filter((c) => majors.has(c.major as number))

  const toRef = (c: { namespace: string; name: string }) => ({
    kind: 'Cluster',
    namespace: c.namespace,
    name: c.name,
    group: CNPG_GROUP,
  })

  return (
    <BaseImageCatalog
      data={data}
      usedBy={
        <Section title="Used By" icon={Boxes} defaultExpanded>
          {clusters.isLoading ? (
            <div className="text-sm text-theme-text-tertiary">Looking for clusters…</div>
          ) : users.length === 0 ? (
            clusters.error ? (
              <LookupFailureNote
                errors={[clusters.error]}
                what="which clusters use this catalog"
              />
            ) : (
              <div className="text-sm text-theme-text-tertiary">
                {/* "Changing it affects nothing" is a claim about the cluster,
                    and this search does not cover one. A ClusterImageCatalog is
                    referenced from any namespace, but the lookup that answers
                    this falls back to the reader's namespace view filter, so a
                    filtered reader is told an edit is safe on the strength of
                    the namespaces they happen to be looking at. The namespaced
                    ImageCatalog has no such gap — its users can only be in its
                    own namespace, which is exactly what was searched. */}
                {scoped
                  ? 'No cluster in this namespace is pinned to this catalog. Changing it affects nothing here today.'
                  : 'No cluster in view is pinned to this catalog. Clusters in namespaces you are not currently showing are not covered by this check.'}
              </div>
            )
          ) : (
            <div className="space-y-3">
              {met.length > 0 && (
                <RelationshipGroup label="Clusters" refs={met.map(toRef)} onNavigate={onNavigate} />
              )}
              {unmet.length > 0 && (
                <div className="space-y-1.5">
                  <RelationshipGroup
                    label="Pinned to a version this catalog does not list"
                    refs={unmet.map(toRef)}
                    onNavigate={onNavigate}
                  />
                  {unmet.some((c) => !!c.image) && (
                    <div className="text-xs text-theme-text-secondary">
                      {`Running now: ${unmet
                        .filter((c) => !!c.image)
                        .map((c) => `${c.name} on ${c.image}`)
                        .join(', ')}`}
                    </div>
                  )}
                  <div className="text-xs text-warning-text">
                    {/* What the catalog can prove, and no further. A cluster
                        that already resolved an image keeps running on it, so
                        "will not start" sends a responder hunting for
                        crash-looping pods that are not there — the failure is
                        the NEXT time an image has to be resolved. The named
                        form only when there is a major to name: a reference
                        missing one lands here too, and "asks for PostgreSQL
                        undefined" would be worse than the general sentence. */}
                    {unmet.length === 1 && typeof unmet[0]?.major === 'number'
                      ? `${unmet[0]?.name} asks for PostgreSQL ${unmet[0].major}, which this catalog does not list. Whatever image it is running now, the next time it has to resolve one it will fail.`
                      : 'These clusters ask for a major version this catalog does not list. Whatever image they are running now, the next time they have to resolve one they will fail.'}
                  </div>
                </div>
              )}
            </div>
          )}
        </Section>
      }
    />
  )
}
