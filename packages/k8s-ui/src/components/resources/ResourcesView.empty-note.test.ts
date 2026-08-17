import { describe, expect, it } from 'vitest'
import { emptyKindNote } from './ResourcesView'

describe('emptyKindNote', () => {
  it('explains that no UpdateRequest is the resting state, and that it looks the same as a rule that never ran', () => {
    const note = emptyKindNote('UpdateRequest', 'kyverno.io')
    expect(note).toContain('deleted seconds after')
    expect(note).toContain('never ran')
  })

  it('says an absent EphemeralReport means the scan finished, not that nothing was checked', () => {
    expect(emptyKindNote('EphemeralReport', 'reports.kyverno.io')).toContain('scan has finished')
    expect(emptyKindNote('ClusterEphemeralReport', 'reports.kyverno.io')).toContain('scan has finished')
  })

  it('is case-insensitive on the kind, which arrives from a URL as often as from discovery', () => {
    expect(emptyKindNote('updaterequest', 'kyverno.io')).toBeDefined()
  })

  // Same plural, different operator: Kyverno's group is what makes the note true.
  it('says nothing about a kind of the same name from another group', () => {
    expect(emptyKindNote('UpdateRequest', 'example.com')).toBeUndefined()
    expect(emptyKindNote('EphemeralReport', '')).toBeUndefined()
  })

  it('says nothing about kinds with no such story', () => {
    expect(emptyKindNote('Pod', '')).toBeUndefined()
  })

  // The table can be empty because the reader emptied it. Telling them none is
  // normal then answers a question they did not ask, with their term still in
  // the search box.
  it('stays silent when the kind has rows and a filter is what emptied the table', () => {
    expect(emptyKindNote('UpdateRequest', 'kyverno.io', 3)).toBeUndefined()
    expect(emptyKindNote('EphemeralReport', 'reports.kyverno.io', 192)).toBeUndefined()
  })

  it('still speaks when the kind is genuinely empty', () => {
    expect(emptyKindNote('UpdateRequest', 'kyverno.io', 0)).toBeDefined()
  })
})
