import { describe, expect, it } from 'vitest'
import { renderToString } from 'react-dom/server'
import { LookupFailureNote } from './LookupFailureNote'

function err(message: string, status?: number): Error {
  return Object.assign(new Error(message), status === undefined ? {} : { status })
}

describe('LookupFailureNote', () => {
  it('says nothing when every lookup came back', () => {
    expect(renderToString(<LookupFailureNote errors={[]} what="x" />)).toBe('')
    expect(renderToString(<LookupFailureNote errors={[null, undefined]} what="x" />)).toBe('')
  })

  // Being unable to see something is expected and not actionable; a fault is
  // neither. The two must not read alike.
  it('renders a permission answer as a calm note, not a red error', () => {
    const html = renderToString(
      <LookupFailureNote errors={[err('forbidden', 403)]} what="which clusters use this store" />,
    )
    expect(html).toContain('permission')
    expect(html).not.toContain('text-red-400')
  })

  it('keeps a genuine fault loud, with the reason', () => {
    const html = renderToString(
      <LookupFailureNote errors={[err('Failed to fetch')]} what="what replicates here" />,
    )
    expect(html).toContain('text-red-400')
    expect(html).toContain('Failed to fetch')
  })

  // A fault is actionable and a permission answer is not, so the actionable one
  // is the one worth showing when both happened.
  it('prefers the fault when a lookup was both forbidden and broken', () => {
    const html = renderToString(
      <LookupFailureNote errors={[err('forbidden', 403), err('boom', 500)]} what="x" />,
    )
    expect(html).toContain('boom')
    expect(html).toContain('text-red-400')
  })

  // The case that loses counts: rows are on screen, so the failure means the
  // list is short — not that nothing was found.
  it('says the list is incomplete rather than unchecked when rows are showing', () => {
    const html = renderToString(
      <LookupFailureNote errors={[err('boom', 500)]} what="x" incomplete />,
    )
    expect(html).toContain('This may be incomplete')
    expect(html).toContain('boom')
  })

  it('does the same for a partial permission answer', () => {
    const html = renderToString(
      <LookupFailureNote errors={[err('forbidden', 403)]} what="x" incomplete />,
    )
    expect(html).toContain('may be incomplete')
    expect(html).not.toContain('text-red-400')
  })
})
