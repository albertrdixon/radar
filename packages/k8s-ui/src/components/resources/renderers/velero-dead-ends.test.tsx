import { describe, expect, it, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import { VeleroBackupRenderer } from './VeleroBackupRenderer'
import { VeleroRestoreRenderer } from './VeleroRestoreRenderer'

/**
 * Every screen has to end somewhere the reader can act.
 *
 * These two pages each name another object as the reason the reader should care
 * — a storage location that is holding a backup hostage, and the backup a
 * restore came from — and each rendered that name as plain text. The page said
 * "go look at dr-replica" and gave no way to get there.
 *
 * The link has to disappear cleanly when the host does not wire navigation:
 * library consumers render these renderers without it, and a button that goes
 * nowhere is worse than text.
 */

const backup = (location: string) => ({
  metadata: { name: 'nightly', namespace: 'velero' },
  spec: { storageLocation: location },
  status: { phase: 'Completed' },
})

const restore = {
  metadata: { name: 'recovery', namespace: 'velero' },
  spec: { backupName: 'nightly' },
  status: { phase: 'Completed' },
}

describe('a backup that blames its storage location', () => {
  it('lets the reader get to the location', () => {
    const html = renderToString(
      <VeleroBackupRenderer
        data={backup('dr-replica')}
        storageLocationPhase="Unavailable"
        onNavigate={vi.fn()}
      />,
    )
    // The name is a control, not a label.
    expect(html).toMatch(/<button[^>]*>dr-replica<\/button>/)
  })

  it('renders the location as text when navigation is not wired', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={backup('dr-replica')} storageLocationPhase="Unavailable" />,
    )
    expect(html).toContain('dr-replica')
    expect(html).not.toMatch(/<button[^>]*>dr-replica<\/button>/)
  })

  // The link is the way to the location whether or not it is broken — a healthy
  // one is still the thing you check when deciding where a backup lives.
  it('links the location even when it is healthy', () => {
    const html = renderToString(
      <VeleroBackupRenderer data={backup('default')} storageLocationPhase="Available" onNavigate={vi.fn()} />,
    )
    expect(html).toMatch(/<button[^>]*>default<\/button>/)
  })
})

describe('a restore that names its backup', () => {
  it('lets the reader get to the backup it restored from', () => {
    const html = renderToString(<VeleroRestoreRenderer data={restore} onNavigate={vi.fn()} />)
    expect(html).toMatch(/<button[^>]*>nightly<\/button>/)
  })

  it('renders the backup as text when navigation is not wired', () => {
    const html = renderToString(<VeleroRestoreRenderer data={restore} />)
    expect(html).toContain('nightly')
    expect(html).not.toMatch(/<button[^>]*>nightly<\/button>/)
  })
})

/**
 * The stalled-run issue says a run is "past the 1m Velero allows for one
 * operation". That budget is spec.itemOperationTimeout, and the Options section
 * listed every sibling spec option except it — so the page quoted a number whose
 * source it did not show, next to the place that source belongs.
 */
describe('the budget the stall message measures against', () => {
  it('shows the timeout the message quotes', () => {
    const html = renderToString(
      <VeleroBackupRenderer
        data={{
          metadata: { name: 'stuck', namespace: 'velero' },
          spec: { storageLocation: 'default', itemOperationTimeout: '1m0s' },
          status: { phase: 'InProgress' },
        }}
      />,
    )
    expect(html).toContain('Item Operation Timeout')
    expect(html).toContain('1m0s')
  })

  // Unset is the common case, and it is not "none" — a default applies. But the
  // number is not ours to print: Velero's built-in four hours can be overridden
  // on the controller, so a page that renders "4h" would be stating an install
  // setting it cannot read.
  it('does not invent a number when the field is unset', () => {
    const html = renderToString(
      <VeleroBackupRenderer
        data={{
          metadata: { name: 'plain', namespace: 'velero' },
          spec: { storageLocation: 'default' },
          status: { phase: 'InProgress' },
        }}
      />,
    )
    // 'default' is this file's own word for "unset, and something else decides".
    expect(html).toContain('default')
    expect(html).not.toContain('4h0m0s')
  })
})

/**
 * Claims the phase does not support.
 *
 * The warning banner fired on any run with warnings that was not failed or
 * partially failed — which includes runs still in flight — and said the run
 * "completed". A count taken mid-run is not a final count, and saying otherwise
 * is the same defect as calling a Completed backup restorable.
 */
describe('a run that has warnings but has not finished', () => {
  const withWarnings = (phase: string) => ({
    metadata: { name: 'w', namespace: 'velero' },
    spec: { storageLocation: 'default' },
    status: { phase, warnings: 2 },
  })

  it('does not say a run in flight completed', () => {
    const html = renderToString(<VeleroBackupRenderer data={withWarnings('InProgress')} />)
    expect(html).not.toMatch(/completed, with/)
    expect(html).toContain('not a final count')
  })

  it('still says completed when it did', () => {
    const html = renderToString(<VeleroBackupRenderer data={withWarnings('Completed')} />)
    expect(html).toContain('completed, with 2 warning(s)')
    expect(html).not.toContain('not a final count')
  })
})
