import { describe, expect, it, vi } from 'vitest'
import { renderToString } from 'react-dom/server'

// The denial is the state under test, so the fetch is stubbed rather than run.
const forbidden = Object.assign(new Error('forbidden'), { status: 403 })
let queuedResult: { data?: unknown; error?: unknown } = {}

vi.mock('../../../api/policy', () => ({
  usePolicyQueued: () => queuedResult,
}))
vi.mock('../../../api/client', () => ({
  isForbiddenError: (e: any) => e?.status === 403,
}))

const { KyvernoPolicyQueued } = await import('./KyvernoPolicyQueued')

const generating = {
  kind: 'ClusterPolicy',
  metadata: { name: 'gen-companion' },
  spec: { rules: [{ name: 'g', generate: {} }] },
}
const validating = {
  kind: 'ClusterPolicy',
  metadata: { name: 'require-labels' },
  spec: { rules: [{ name: 'v', validate: {} }] },
}

/**
 * A denial here is cluster-static, so disclosing it everywhere would be noise on
 * the majority of policies that only validate. Staying silent about it on a
 * policy that DOES queue is worse: a stuck backlog then looks exactly like a
 * healthy policy with nothing pending, on the page someone checks to find out.
 */
describe('KyvernoPolicyQueued — a denial you cannot see', () => {
  it('says it cannot check when the policy actually queues work', () => {
    queuedResult = { error: forbidden }
    const html = renderToString(<KyvernoPolicyQueued data={generating} />)
    expect(html).toContain('permission')
    expect(html).toContain('Queued Work')
  })

  it('stays silent for a policy that never queues anything', () => {
    queuedResult = { error: forbidden }
    expect(renderToString(<KyvernoPolicyQueued data={validating} />)).toBe('')
  })

  it('stays silent when there is genuinely nothing queued', () => {
    queuedResult = { data: { requests: 0 } }
    expect(renderToString(<KyvernoPolicyQueued data={generating} />)).toBe('')
  })

  // A fault is not a denial: it is neither permanent nor beyond acting on, so it
  // is loud regardless of what the policy does.
  it('reports a genuine fault on any policy', () => {
    queuedResult = { error: new Error('could not read queued work') }
    const html = renderToString(<KyvernoPolicyQueued data={validating} />)
    expect(html).toContain('could not read queued work')
  })
})
