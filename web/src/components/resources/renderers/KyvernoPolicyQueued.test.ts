import { describe, it, expect } from 'vitest'
import { policyQueuesWork, queueBanner, requestBelongsTo } from './KyvernoPolicyQueued'

/**
 * Kyverno permits a namespaced Policy and a ClusterPolicy to share a name, and
 * records the first qualified and the second bare. Accepting the bare form as a
 * fallback for a namespaced policy shows it someone else's backlog — the same
 * collision the coverage lookup refuses, reintroduced here once already.
 */
describe('requestBelongsTo', () => {
  const req = (policy: string) => ({ spec: { policy } })

  it('matches a cluster-scoped policy on the bare name only', () => {
    expect(requestBelongsTo(req('require-labels'), 'require-labels', '')).toBe(true)
    expect(requestBelongsTo(req('team-a/require-labels'), 'require-labels', '')).toBe(false)
  })

  it('matches a namespaced policy on the qualified name only', () => {
    expect(requestBelongsTo(req('team-a/require-labels'), 'require-labels', 'team-a')).toBe(true)
    // The bug: this is a ClusterPolicy's request and must not appear here.
    expect(requestBelongsTo(req('require-labels'), 'require-labels', 'team-a')).toBe(false)
  })

  it('does not match another namespace', () => {
    expect(requestBelongsTo(req('team-b/require-labels'), 'require-labels', 'team-a')).toBe(false)
  })

  it('matches nothing when either side is missing', () => {
    expect(requestBelongsTo({}, 'require-labels', '')).toBe(false)
    expect(requestBelongsTo(req('require-labels'), '', '')).toBe(false)
  })
})

/**
 * The banner is the only thing on this page that makes a claim rather than a
 * count, so the claim has to be the one the numbers support.
 */
describe('queueBanner', () => {
  it('says work stopped only when something has actually sat still', () => {
    const b = queueBanner(3, 12, '12m')
    expect(b?.title).toBe('Queued work has not moved for 12m')
    expect(b?.message).toContain('cannot keep up')
  })

  // The bug: this branch fires when NOTHING is older than the stall threshold,
  // which is the case most likely to be a burst draining normally. The body has
  // to agree with the headline — diagnosing a stalled controller underneath a
  // headline that only reports a size puts the claim straight back.
  it('does not tell you a moving backlog is not being processed', () => {
    const b = queueBanner(30, 1, '1m')
    expect(b?.title).toBe('30 requests are queued')
    expect(b?.title).not.toContain('not being processed')
    expect(b?.message).toContain('may be a burst still draining')
    expect(b?.message).not.toContain('cannot keep up')
    expect(b?.message).not.toContain('grows rather than drains')
  })

  it('stays quiet for a queue doing what a queue does', () => {
    expect(queueBanner(3, 1, '1m')).toBeNull()
    expect(queueBanner(0, 0, '')).toBeNull()
  })

  it('prefers the measured stall over the size when both apply', () => {
    expect(queueBanner(40, 30, '30m')?.title).toContain('has not moved')
  })
})

/**
 * A denial is only worth disclosing where it can cost something. Silence on a
 * policy that queues nothing keeps the page clean; silence on one that DOES
 * queue hides a backlog behind a page that looks healthy.
 */
describe('policyQueuesWork', () => {
  it('is true for a policy that generates resources', () => {
    expect(policyQueuesWork({ spec: { rules: [{ name: 'g', generate: {} }] } })).toBe(true)
    expect(policyQueuesWork({ kind: 'GeneratingPolicy', spec: {} })).toBe(true)
  })

  it('is true for a policy that mutates resources which already exist', () => {
    expect(policyQueuesWork({ spec: { rules: [{ mutate: { targets: [{ kind: 'Pod' }] } }] } })).toBe(true)
    expect(policyQueuesWork({ spec: { evaluation: { mutateExisting: { enabled: true } } } })).toBe(true)
  })

  // The common case, and the reason a blanket note would be noise.
  it('is false for a validate-only policy', () => {
    expect(policyQueuesWork({ spec: { rules: [{ name: 'v', validate: {} }] } })).toBe(false)
    expect(policyQueuesWork({ kind: 'ValidatingPolicy', spec: {} })).toBe(false)
  })

  // A mutate rule with no targets runs at admission only — nothing is queued.
  it('is false for admission-time mutation', () => {
    expect(policyQueuesWork({ spec: { rules: [{ mutate: { patchStrategicMerge: {} } }] } })).toBe(false)
  })

  it('handles a policy with no spec at all', () => {
    expect(policyQueuesWork({})).toBe(false)
    expect(policyQueuesWork(undefined)).toBe(false)
  })
})
