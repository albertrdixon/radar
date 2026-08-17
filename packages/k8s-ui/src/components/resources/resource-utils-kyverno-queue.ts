import { healthColors } from './resource-utils'
import type { StatusBadge } from './resource-utils'

/**
 * UpdateRequest and EphemeralReport — Kyverno's two working records.
 *
 * Both are shaped differently from every other Kyverno object, and in ways the
 * CRD schema does not warn about. Everything here was read off live objects
 * captured with a watch on Kyverno 1.18.2 while the rules that produce them
 * ran, rather than derived from the schema:
 *
 *   - A generate UpdateRequest leaves `spec.resource` EMPTY and `spec.rule` an
 *     empty string. Its triggers live in `spec.ruleContext[].trigger`, and one
 *     request can carry hundreds of them. A mutate UpdateRequest is the
 *     opposite: `spec.resource` and `spec.rule` are populated and there is no
 *     ruleContext at all.
 *   - An EphemeralReport carries its findings in `spec`, not `status`, and its
 *     `spec.owner` is present but blank — the subject is only in
 *     `metadata.ownerReferences` and in the `audit.kyverno.io/resource.*`
 *     labels.
 *   - Report timestamps are `{seconds, nanos}` objects, not RFC 3339 strings.
 */

export type KyvernoRequestType = 'generate' | 'mutate' | 'unknown'

export interface KyvernoTrigger {
  apiVersion?: string
  kind?: string
  name?: string
  namespace?: string
  uid?: string
  /** Whether deleting this trigger also deletes what the rule made for it. */
  deleteDownstream: boolean
  rule?: string
}

export function getKyvernoRequestType(resource: any): KyvernoRequestType {
  const t = resource?.spec?.requestType
  return t === 'generate' || t === 'mutate' ? t : 'unknown'
}

/**
 * Every resource this request acts on.
 *
 * Reading `spec.resource` alone — the obvious field, and the only one the CRD
 * documents as "the resource" — returns nothing at all for a generate request.
 */
export function getKyvernoRequestTriggers(resource: any): KyvernoTrigger[] {
  const ctx = resource?.spec?.ruleContext
  if (Array.isArray(ctx) && ctx.length > 0) {
    return ctx
      .map((c: any) => ({
        ...(c?.trigger ?? {}),
        deleteDownstream: c?.deleteDownstream === true,
        rule: c?.rule,
      }))
      .filter((t: KyvernoTrigger) => !!t.name)
  }
  const r = resource?.spec?.resource
  if (r?.name) {
    return [{ ...r, deleteDownstream: resource?.spec?.deleteDownstream === true, rule: resource?.spec?.rule }]
  }
  return []
}

/** Resources the request has created so far. Absent on mutate requests. */
export function getKyvernoRequestGenerated(resource: any): Array<{
  apiVersion?: string
  kind?: string
  name?: string
  namespace?: string
}> {
  const g = resource?.status?.generatedResources
  return Array.isArray(g) ? g.filter((r: any) => !!r?.name) : []
}

/**
 * State of the request.
 *
 * `Pending` is the one that matters: a request stuck there is generation that
 * has silently stopped, which is the reason to look at this object at all.
 */
export function getKyvernoRequestState(resource: any): StatusBadge {
  const state = resource?.status?.state
  switch (state) {
    case 'Completed':
      return { text: 'Completed', color: healthColors.healthy, level: 'healthy' }
    case 'Failed':
      return { text: 'Failed', color: healthColors.unhealthy, level: 'unhealthy' }
    case 'Pending':
      return { text: 'Pending', color: healthColors.degraded, level: 'degraded' }
    case 'Skip':
      return { text: 'Skipped', color: healthColors.neutral, level: 'neutral' }
    default:
      return { text: state || 'Unknown', color: healthColors.unknown, level: 'unknown' }
  }
}

export function getKyvernoRequestPolicy(resource: any): string | undefined {
  return resource?.spec?.policy || undefined
}

export function getKyvernoRequestMessage(resource: any): string | undefined {
  const m = resource?.status?.message
  return typeof m === 'string' && m !== '' ? m : undefined
}

// ============================================================================
// EphemeralReport
// ============================================================================

export interface KyvernoReportSubject {
  apiVersion?: string
  kind?: string
  name?: string
  namespace?: string
  uid?: string
}

/**
 * The resource an EphemeralReport is about.
 *
 * `spec.owner` exists on every one of these and is blank on every one observed,
 * so the owner reference is the real source and the labels are the fallback for
 * a report whose owner has already been deleted.
 */
export function getKyvernoReportSubject(resource: any): KyvernoReportSubject | undefined {
  const owner = resource?.spec?.owner
  if (owner?.name) return owner

  const ref = (resource?.metadata?.ownerReferences ?? [])[0]
  if (ref?.name) {
    return {
      apiVersion: ref.apiVersion,
      kind: ref.kind,
      name: ref.name,
      namespace: resource?.metadata?.namespace,
      uid: ref.uid,
    }
  }

  const labels = resource?.metadata?.labels ?? {}
  const kind = labels['audit.kyverno.io/resource.kind']
  if (!kind) return undefined
  const group = labels['audit.kyverno.io/resource.group']
  const version = labels['audit.kyverno.io/resource.version']
  return {
    apiVersion: group ? `${group}/${version}` : version,
    kind,
    namespace: resource?.metadata?.namespace,
    uid: labels['audit.kyverno.io/resource.uid'],
  }
}

/** How the report was produced — background scan or admission. */
export function getKyvernoReportSource(resource: any): string | undefined {
  return resource?.metadata?.labels?.['audit.kyverno.io/source'] || undefined
}

export function getKyvernoReportSummary(resource: any): {
  pass: number
  fail: number
  warn: number
  error: number
  skip: number
  total: number
} {
  const s = resource?.spec?.summary ?? {}
  const n = (v: unknown) => (typeof v === 'number' ? v : 0)
  const out = { pass: n(s.pass), fail: n(s.fail), warn: n(s.warn), error: n(s.error), skip: n(s.skip) }
  return { ...out, total: out.pass + out.fail + out.warn + out.error + out.skip }
}

export function getKyvernoReportResults(resource: any): any[] {
  const r = resource?.spec?.results
  return Array.isArray(r) ? r : []
}

/**
 * Report timestamps are `{seconds, nanos}`, and `new Date(...)` on that object
 * yields Invalid Date rather than failing loudly.
 */
export function kyvernoReportTimestamp(ts: any): string | undefined {
  if (typeof ts === 'string') return ts || undefined
  const seconds = ts?.seconds
  if (typeof seconds !== 'number' || seconds <= 0) return undefined
  return new Date(seconds * 1000).toISOString()
}

/** List badge: worst outcome in the report, matching the PolicyReport columns. */
export function getKyvernoReportStatus(resource: any): StatusBadge {
  const s = getKyvernoReportSummary(resource)
  if (s.total === 0) return { text: 'No results', color: healthColors.neutral, level: 'neutral' }
  if (s.error > 0) return { text: `${s.error} error`, color: healthColors.alert, level: 'alert' }
  if (s.fail > 0) return { text: `${s.fail} fail`, color: healthColors.unhealthy, level: 'unhealthy' }
  if (s.warn > 0) return { text: `${s.warn} warn`, color: healthColors.degraded, level: 'degraded' }
  return { text: `${s.pass} pass`, color: healthColors.healthy, level: 'healthy' }
}
