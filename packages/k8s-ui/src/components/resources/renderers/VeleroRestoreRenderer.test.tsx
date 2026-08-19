import { describe, expect, it } from 'vitest'
import { renderToString } from 'react-dom/server'
import { VeleroRestoreRenderer } from './VeleroRestoreRenderer'

const restore = (status: Record<string, unknown>) => ({
  metadata: { name: 'live-restore', namespace: 'velero' },
  spec: { backupName: 'live-completed' },
  status,
})

/**
 * The restore side carries the same claims as the backup side and its own
 * wording for them, so pinning one does not pin the other. Both banners below
 * were wrong in the same way at the same time.
 */
describe('what a restore claims about itself', () => {
  // A count taken mid-run is not a final count. Velero raises warnings as it
  // goes, so a restore still working can already have several.
  it('does not say a restore in flight completed', () => {
    const html = renderToString(
      <VeleroRestoreRenderer data={restore({ phase: 'InProgress', warnings: 2 })} />,
    )
    expect(html).not.toMatch(/completed, with/)
    expect(html).toContain('not a final count')
  })

  it('still says completed when it did', () => {
    const html = renderToString(
      <VeleroRestoreRenderer data={restore({ phase: 'Completed', warnings: 2 })} />,
    )
    expect(html).toContain('completed, with 2 warning(s)')
    expect(html).not.toContain('not a final count')
  })

  // Velero's own words beat ours whenever it supplies them.
  it('leads with the reason Velero gave for refusing to start', () => {
    const html = renderToString(
      <VeleroRestoreRenderer
        data={restore({ phase: 'FailedValidation', failureReason: 'backup not found' })}
      />,
    )
    expect(html).toContain('backup not found')
    expect(html).not.toContain('Velero rejected this restore before it started')
  })

  it('says nothing was restored when Velero gave no reason', () => {
    const html = renderToString(
      <VeleroRestoreRenderer
        data={restore({ phase: 'FailedValidation', validationErrors: ['Backup live-completed not found'] })}
      />,
    )
    expect(html).toContain('nothing was restored')
    expect(html).toContain('Backup live-completed not found')
  })
})
