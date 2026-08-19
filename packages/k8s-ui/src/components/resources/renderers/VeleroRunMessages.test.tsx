import { describe, expect, it } from 'vitest'
import { renderToString } from 'react-dom/server'
import { VeleroRunMessages } from './VeleroRunMessages'

/**
 * "Warnings: 2" was a dead end. The number is on the Backup; the text is in a
 * results file in object storage that only Velero's controller can hand out a
 * link to — so the page could tell an operator something was wrong and nothing
 * about what.
 *
 * The two failures are the interesting part and both are ordinary: the
 * controller is not running, or the storage the link points at is somewhere this
 * process cannot reach. Neither may be swallowed, because a button that does
 * nothing is worse than the bare number it replaced.
 */
describe('the messages behind a run’s counts', () => {
  it('offers to fetch them when a run reported any', () => {
    const html = renderToString(<VeleroRunMessages errors={0} warnings={2} onFetch={() => {}} />)
    expect(html).toContain('Show the messages')
    // Never "all N": the server caps the list, so the label must not promise a
    // number the fetch may not deliver.
    expect(html).not.toContain('all 2')
    expect(html).toContain('Velero has to be asked')
  })

  it('says nothing at all for a clean run', () => {
    // No button, no explanation, no row: a run that reported nothing has nothing
    // to explain, and an empty section would imply otherwise.
    expect(renderToString(<VeleroRunMessages errors={0} warnings={0} onFetch={() => {}} />)).toBe('')
  })

  it('names each message’s scope alongside it', () => {
    const html = renderToString(
      <VeleroRunMessages
        errors={0}
        warnings={1}
        messages={{
          errors: [],
          warnings: [{ scope: 'demo-app', message: 'could not restore ConfigMap' }],
        }}
      />,
    )
    expect(html).toContain('demo-app')
    expect(html).toContain('could not restore ConfigMap')
    // Scope and message are separate spans with only a margin between them, so
    // without a real separator a screen reader reads "demo-appcould not
    // restore" — correct on screen, wrong to anyone listening to it.
    expect(html).not.toMatch(/demo-app<\/span><span[^>]*>could not restore/)
    // The offer is gone once they are on screen.
    expect(html).not.toContain('Velero has to be asked')
  })

  // The note itself is the host's to build — a denial and a fault read
  // differently, and only the host knows which it got. This asserts the slot is
  // rendered, not what it says.
  it('shows why the fetch failed instead of an empty list', () => {
    const html = renderToString(
      <VeleroRunMessages
        errors={0}
        warnings={2}
        lookupNote={<span>Velero did not answer — check that it is running.</span>}
        onFetch={() => {}}
      />,
    )
    expect(html).toContain('check that it is running')
    // And the offer stays, so a failed fetch is retryable.
    expect(html).toContain('Show the messages')
  })

  // The counts come from the CR and the messages from a file written separately.
  // They can disagree, and saying so is better than rendering nothing under a
  // non-zero number and letting it read as a broken button.
  it('says so when the counts and the file disagree', () => {
    const html = renderToString(
      <VeleroRunMessages errors={0} warnings={2} messages={{ errors: [], warnings: [] }} />,
    )
    expect(html).toContain('results file it returned holds none')
  })

  // The counts sit two lines above this list. They come from the object and the
  // messages come from a file written separately, so a run can report two and
  // return one — and a list of one under "Warnings: 2" is a number the reader
  // cannot reconcile with what is beside it.
  it('says so when it returned fewer than the count promised', () => {
    const html = renderToString(
      <VeleroRunMessages
        errors={0}
        warnings={2}
        messages={{ errors: [], warnings: [{ scope: 'velero', message: 'the only one' }] }}
      />,
    )
    expect(html).toContain('reported 2 on this run and returned 1')
  })

  // Truncation is a shortfall this already explains on its own line; saying it
  // twice, in two different vocabularies, reads as two different problems.
  it('does not also report a mismatch when the list was capped', () => {
    const html = renderToString(
      <VeleroRunMessages
        errors={0}
        warnings={9000}
        messages={{
          errors: [],
          warnings: [{ scope: 'velero', message: 'one of very many' }],
          truncated: true,
        }}
      />,
    )
    expect(html).not.toContain('and returned')
    expect(html).toContain('Only the first')
  })

  it('admits when the list was capped', () => {
    const html = renderToString(
      <VeleroRunMessages
        errors={0}
        warnings={9000}
        messages={{
          errors: [],
          warnings: [{ scope: 'velero', message: 'one of very many' }],
          truncated: true,
        }}
      />,
    )
    expect(html).toContain('Only the first')
  })
})

/**
 * An error and a warning were separated by an icon and a colour, and by nothing
 * else — so to anyone not seeing them, the distinction that decides whether to
 * act simply was not there.
 */
describe('telling an error from a warning without seeing it', () => {
  const both = {
    errors: [{ scope: 'velero', message: 'the failure' }],
    warnings: [{ scope: 'demo-app', message: 'the caution' }],
  }

  it('names the severity in text', () => {
    const html = renderToString(<VeleroRunMessages errors={1} warnings={1} messages={both} />)
    expect(html).toContain('Error: ')
    expect(html).toContain('Warning: ')
  })

  it('announces the region so a late arrival is not silent', () => {
    const html = renderToString(
      <VeleroRunMessages errors={1} warnings={0} messages={{ errors: both.errors, warnings: [] }} />,
    )
    expect(html).toContain('aria-live')
  })
})
