import { describe, expect, it } from 'vitest'
import { isSameCoverageSubject } from './policy'

/**
 * The drawer reuses one query observer as it opens different policies, so
 * keeping the previous result unconditionally renders policy A's resources and
 * counts under policy B's name — with no loading state to suggest otherwise.
 * Only a changed `limit` may reuse it.
 */
const key = (policy: string, ns: string, limit: number, view: string) =>
  ['policy', 'coverage', policy, ns, limit, view] as const

describe('reusing a cached coverage result', () => {
  it('reuses it when only the limit changed', () => {
    expect(isSameCoverageSubject(key('require-limits', '', 0, ''), 'require-limits', '', '')).toBe(true)
    expect(isSameCoverageSubject(key('require-limits', '', 5000, ''), 'require-limits', '', '')).toBe(true)
  })

  it('refuses it for a different policy', () => {
    expect(isSameCoverageSubject(key('policy-a', '', 0, ''), 'policy-b', '', '')).toBe(false)
  })

  // Kyverno allows a namespaced Policy and a ClusterPolicy to share a name, so
  // the namespace is what separates two same-named subjects.
  it('refuses it for the same name in a different namespace', () => {
    expect(isSameCoverageSubject(key('shared', 'team-a', 0, ''), 'shared', 'team-b', '')).toBe(false)
    expect(isSameCoverageSubject(key('shared', 'team-a', 0, ''), 'shared', '', '')).toBe(false)
  })

  it('refuses it when the namespace view changed', () => {
    expect(isSameCoverageSubject(key('p', '', 0, '?namespaces=app'), 'p', '', '?namespaces=other')).toBe(false)
  })

  it('refuses it when there is no previous query', () => {
    expect(isSameCoverageSubject(undefined, 'p', '', '')).toBe(false)
    expect(isSameCoverageSubject(['policy', 'coverage'], 'p', '', '')).toBe(false)
  })
})
