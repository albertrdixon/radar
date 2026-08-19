import { describe, expect, it } from 'vitest'
import { renderToString } from 'react-dom/server'
import { VeleroBackupRenderer } from './VeleroBackupRenderer'

const backup = (location: string) => ({
  metadata: { name: 'nightly', namespace: 'velero' },
  spec: { storageLocation: location, ttl: '720h0m0s' },
  status: { phase: 'Completed', startTimestamp: '2026-08-14T01:00:00Z' },
})

/**
 * A backup cannot see the bucket behind it. Its own status says Completed and
 * keeps saying it after the storage location goes Unavailable, which is exactly
 * when someone opens this page to ask whether they can restore to this point.
 */
describe('a backup whose storage location is unreachable', () => {
  it('says so on the backup, not one screen away', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={backup('dr-replica')} storageLocationPhase="Unavailable" />,
    )
    expect(html).toContain('nothing here to restore from')
    expect(html).toContain('Unavailable')
  })

  // On a healthy location this would be a warning about nothing, on every backup
  // in the cluster.
  it('stays quiet when the location is fine', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={backup('default')} storageLocationPhase="Available" />,
    )
    expect(html).not.toContain('nothing here to restore from')
  })

  // The distinction that matters: a location we have not read is not a healthy
  // one, and must not render as one.
  it('claims nothing while the lookup is unresolved', () => {
    const html = renderToString(<VeleroBackupRenderer data={backup('default')} />)
    expect(html).not.toContain('nothing here to restore from')
    expect(html).not.toContain('Unavailable')
  })
})

/**
 * FailedValidation is the one outcome where Velero usually explains itself, in
 * status.failureReason. Radar's own sentence is the fallback for when it does
 * not — a run rejected before it started leaves no errors, no warnings and no
 * item counts, so without either the page is an empty red banner.
 */
describe('a run Velero refused to start', () => {
  const rejected = (failureReason?: string) => ({
    metadata: { name: 'live-rejected', namespace: 'velero' },
    spec: { storageLocation: 'dr-replica' },
    status: {
      phase: 'FailedValidation',
      ...(failureReason ? { failureReason } : {}),
      validationErrors: ['Backup storage location "dr-replica" is unavailable'],
    },
  })

  // Velero's own words beat ours whenever it supplies them.
  it('leads with the reason Velero gave', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={rejected('backup storage location is in read-only mode')} />,
    )
    expect(html).toContain('backup storage location is in read-only mode')
    expect(html).not.toContain('Velero rejected this backup before it started')
  })

  it('says nothing was backed up when Velero gave no reason', () => {
    const html = renderToString(<VeleroBackupRenderer data={rejected()} />)
    expect(html).toContain('nothing was backed up')
    // The validation errors are the actionable half either way.
    expect(html).toContain('dr-replica&quot; is unavailable')
  })
})

/**
 * Velero resolves an unset spec.storageLocation to whichever location carries
 * spec.default, which is a flag and not a name. An install that renamed its
 * default location has no object called "default" at all, so resolving by name
 * finds nothing: the unavailable-location warning never appears and the link
 * points at something that does not exist.
 */
describe('a backup that names no location', () => {
  const unset = {
    metadata: { name: 'nightly', namespace: 'velero' },
    spec: { ttl: '720h0m0s' },
    status: { phase: 'Completed' },
  }

  it('links the location that actually holds it', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={unset} storageLocationName="primary" onNavigate={() => {}} />,
    )
    expect(html).toContain('primary')
  })

  it('still warns when that location is the unavailable one', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={unset} storageLocationName="primary" storageLocationPhase="Unavailable" />,
    )
    expect(html).toContain('nothing here to restore from')
  })

  // Replaced the by-name fallback assertion: it checked for the substring
  // "default", which the "Velero's default location, not yet resolved" sentence
  // also contains — so it passed while documenting the opposite of what the
  // renderer now does. A host that does not resolve gets no asserted name.
  it('asserts no location name when the host does not resolve one', () => {
    const html = renderToString(<VeleroBackupRenderer data={unset} onNavigate={() => {}} />)
    expect(html).toContain('not yet resolved')
  })
})

/**
 * Three states, not two. The lookup can be unanswered, answered-and-found, or
 * answered-and-the-location-is-gone — and the last one has no phase to report,
 * so without saying it the backup renders exactly as it would on a healthy
 * bucket.
 */
describe('what the page claims before the location lookup answers', () => {
  const unset = {
    metadata: { name: 'nightly', namespace: 'velero' },
    spec: {},
    status: { phase: 'Completed' },
  }
  const named = {
    metadata: { name: 'nightly', namespace: 'velero' },
    spec: { storageLocation: 'dr-replica' },
    status: { phase: 'Completed' },
  }

  // Scoped to the Storage Location row: "default" is also the rendered value of
  // two unset fields in Options, so a whole-document match proves nothing.
  const storageCell = (html: string) => {
    const i = html.indexOf('Storage Location')
    if (i < 0) return ''
    // Up to the end of this property row only. A fixed-width window runs into
    // the next Section's header, which is itself a <button>, so every case
    // matched a link whether or not one was rendered.
    const end = html.indexOf('</div>', i)
    return html.slice(i, end < 0 ? undefined : end)
  }

  // An unset location is only knowable from the list. Printing "default" before
  // it answers is a guess that is wrong on any install that renamed it.
  it('does not name a location it has not resolved', () => {
    const cell = storageCell(renderToString(<VeleroBackupRenderer data={unset} onNavigate={() => {}} />))
    expect(cell).toContain('not yet resolved')
    expect(cell).not.toContain('>default<')
  })

  // A backup that names its own location is known regardless of the lookup.
  it('still shows a location the backup names itself', () => {
    const html = renderToString(<VeleroBackupRenderer data={named} onNavigate={() => {}} />)
    expect(html).toContain('dr-replica')
    expect(html).not.toContain('not yet resolved')
  })

  it('says so when the location it names is gone', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={named} storageLocationName="dr-replica" storageLocationMissing onNavigate={() => {}} />,
    )
    expect(html).toContain('Not found')
    expect(html).toContain('no longer exists')
  })

  // A link to an object that is not there is worse than plain text. Scoped to
  // the cell: every collapsible Section header is itself a <button>, so the
  // whole document always matches.
  it('does not link a location that is gone', () => {
    const missing = storageCell(renderToString(
      <VeleroBackupRenderer data={named} storageLocationName="dr-replica" storageLocationMissing onNavigate={() => {}} />,
    ))
    const present = storageCell(renderToString(
      <VeleroBackupRenderer data={named} storageLocationName="dr-replica" onNavigate={() => {}} />,
    ))
    expect(present).toContain('<button')
    expect(missing).not.toContain('<button')
  })
})

/**
 * Where the name came from changes what can be said about it. A backup that
 * names its own location and cannot find it has named something missing. A
 * backup that names nothing has not — the name was produced by Velero's
 * fallback rule, and blaming the object for it invents a claim.
 */
describe('a location that cannot be found', () => {
  const named = {
    metadata: { name: 'nightly', namespace: 'velero' },
    spec: { storageLocation: 'dr-replica' },
    status: { phase: 'Completed' },
  }
  const unset = {
    metadata: { name: 'nightly', namespace: 'velero' },
    spec: {},
    status: { phase: 'Completed' },
  }

  it('blames the backup only when the backup named it', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={named} storageLocationName="dr-replica" storageLocationMissing />,
    )
    expect(html).toContain('names a storage location that no longer exists')
    expect(html).toContain('Not found')
  })

  // The fallback resolved to the literal "default" because nothing carries
  // spec.default. Saying the backup "names" that is attributing an invented
  // name to the object.
  it('says the namespace has no default when the backup named nothing', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={unset} storageLocationName="default" storageLocationMissing />,
    )
    expect(html).toContain('names no storage location')
    expect(html).toContain('this namespace has none')
    expect(html).toContain('No default location')
    expect(html).not.toContain('names a storage location that no longer exists')
  })
})
