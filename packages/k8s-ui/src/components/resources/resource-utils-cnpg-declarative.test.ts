import { describe, it, expect } from 'vitest'
import {
  splitCNPGDeclarativeByApplied,
  getCNPGDeclarativeStatus,
  getCNPGDeclarativeMessage,
  getCNPGReclaimPolicy,
  getCNPGImageCatalogEntries,
} from './resource-utils-cnpg'

/**
 * `status.applied` has three meanings and the middle one is easy to lose.
 * Verified against a live CNPG 1.27 operator: `demo-app` came back true,
 * `demo-broken` came back false with `role "nobody-owns-this" does not exist`,
 * and every object is briefly absent-status before the operator reaches it.
 */
describe('declarative object status', () => {
  it('is healthy once the operator has applied it', () => {
    expect(getCNPGDeclarativeStatus({ status: { applied: true } }).level).toBe('healthy')
  })

  it('is unhealthy when the operator tried and failed', () => {
    expect(getCNPGDeclarativeStatus({ status: { applied: false } }).level).toBe('unhealthy')
  })

  // The one that matters: a freshly created object has no `applied` at all, and
  // reporting that as failed condemns everything in its first seconds.
  it('is unknown — not failed — before the operator has reconciled', () => {
    expect(getCNPGDeclarativeStatus({}).level).toBe('unknown')
    expect(getCNPGDeclarativeStatus({ status: {} }).level).toBe('unknown')
    expect(getCNPGDeclarativeStatus({ status: { applied: undefined } }).text).toBe('Pending')
  })

  it('passes the operator’s message through untouched', () => {
    const msg = 'while creating database "demo_broken": ERROR: role "nobody-owns-this" does not exist'
    expect(getCNPGDeclarativeMessage({ status: { message: msg } })).toBe(msg)
    expect(getCNPGDeclarativeMessage({ status: { message: '' } })).toBeUndefined()
    expect(getCNPGDeclarativeMessage({})).toBeUndefined()
  })
})

/**
 * The reclaim policy decides whether deleting a manifest drops real data, so
 * the destructive case has to be identified rather than printed.
 */
describe('reclaim policy', () => {
  it('flags delete as destructive across all three kinds', () => {
    expect(getCNPGReclaimPolicy({ spec: { databaseReclaimPolicy: 'delete' } }).destructive).toBe(true)
    expect(getCNPGReclaimPolicy({ spec: { publicationReclaimPolicy: 'delete' } }).destructive).toBe(true)
    expect(getCNPGReclaimPolicy({ spec: { subscriptionReclaimPolicy: 'delete' } }).destructive).toBe(true)
  })

  it('does not flag retain', () => {
    expect(getCNPGReclaimPolicy({ spec: { databaseReclaimPolicy: 'retain' } }).destructive).toBe(false)
  })

  // CNPG defaults to retain, and defaulting the other way in the UI would
  // warn about data loss that is not going to happen.
  it('defaults to retain when unset', () => {
    const r = getCNPGReclaimPolicy({ spec: {} })
    expect(r.value).toBe('retain')
    expect(r.destructive).toBe(false)
  })
})

describe('image catalog entries', () => {
  it('sorts by major so the list does not reshuffle between reads', () => {
    const e = getCNPGImageCatalogEntries({
      spec: { images: [{ major: 17, image: 'pg:17' }, { major: 15, image: 'pg:15' }, { major: 16, image: 'pg:16' }] },
    })
    expect(e.map((x) => x.major)).toEqual([15, 16, 17])
  })

  it('drops entries with no image rather than rendering a blank row', () => {
    expect(getCNPGImageCatalogEntries({ spec: { images: [{ major: 16 }] } })).toEqual([])
  })

  it('returns nothing for a catalog with no images', () => {
    expect(getCNPGImageCatalogEntries({ spec: {} })).toEqual([])
    expect(getCNPGImageCatalogEntries({})).toEqual([])
  })
})

/**
 * The Database page renders these three buckets with three different claims, so
 * collapsing pending into failed puts "this exists in Kubernetes and not in
 * PostgreSQL" under an object nothing has looked at yet.
 */
describe('splitCNPGDeclarativeByApplied', () => {
  it('keeps pending out of the failures', () => {
    const live = { status: { applied: true } }
    const broken = { status: { applied: false, message: 'boom' } }
    const fresh = {}
    const alsoFresh = { status: {} }

    const out = splitCNPGDeclarativeByApplied([live, broken, fresh, alsoFresh])
    expect(out.applied).toEqual([live])
    expect(out.notApplied).toEqual([broken])
    expect(out.pending).toEqual([fresh, alsoFresh])
  })

  it('returns three empty buckets for nothing', () => {
    expect(splitCNPGDeclarativeByApplied([])).toEqual({ applied: [], notApplied: [], pending: [] })
  })
})
