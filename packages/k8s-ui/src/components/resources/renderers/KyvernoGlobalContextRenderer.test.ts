import { describe, it, expect } from 'vitest'
import { getKyvernoContextRefresh } from './KyvernoGlobalContextRenderer'

/**
 * Kyverno records a successful refresh and records nothing at all for a failed
 * one — no message, no condition. Verified on 1.18.2: `cluster-namespaces` came
 * back with `lastRefreshTime`, `broken-lookup` came back with an empty status.
 * Age is therefore the only thing separating "failing" from "not yet".
 */
describe('global context refresh state', () => {
  const ago = (ms: number) => new Date(Date.now() - ms).toISOString()

  it('is healthy once it has refreshed', () => {
    const r = getKyvernoContextRefresh({ status: { lastRefreshTime: ago(60_000) } })
    expect(r.refreshed).toBe(true)
    expect(r.level).toBe('healthy')
  })

  it('is unknown while the entry is young enough not to have run yet', () => {
    const r = getKyvernoContextRefresh({ metadata: { creationTimestamp: ago(10_000) }, status: {} })
    expect(r.level).toBe('unknown')
  })

  // Old and never refreshed is the diagnosis; anything sooner would accuse the
  // entry before its first attempt.
  it('is unhealthy once an old entry still has no refresh', () => {
    const r = getKyvernoContextRefresh({ metadata: { creationTimestamp: ago(10 * 60_000) }, status: {} })
    expect(r.level).toBe('unhealthy')
    expect(r.refreshed).toBe(false)
  })
})
