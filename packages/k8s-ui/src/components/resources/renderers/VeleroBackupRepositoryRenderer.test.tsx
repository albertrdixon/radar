import { describe, expect, it } from 'vitest'
import { renderToString } from 'react-dom/server'
import { VeleroBackupRepositoryRenderer } from './VeleroBackupRepositoryRenderer'

const repo = (over: { phase?: string; message?: string } = {}) => ({
  metadata: { name: 'repo-notready', namespace: 'velero' },
  spec: {
    repositoryType: 'kopia',
    volumeNamespace: 'legacy-app',
    backupStorageLocation: 'dr-replica',
    maintenanceFrequency: '1h0m0s',
  },
  status: {
    phase: over.phase ?? 'NotReady',
    ...(over.message !== undefined ? { message: over.message } : {}),
  },
})

/**
 * A BackupRepository raises `BackupRepositoryNotReady`, and `status.message` is
 * the only place Velero records why. Without a renderer of its own the issue
 * pointed at a generic drawer, so the one fact worth arriving for was the one
 * the page could not show.
 */
describe('a repository Velero reports as not ready', () => {
  it('leads with the reason Velero gave', () => {
    const html = renderToString(
      <VeleroBackupRepositoryRenderer
        data={repo({ message: 'error running maintenance: unable to connect to repository' })}
      />,
    )
    expect(html).toContain('unable to connect to repository')
  })

  // Velero can set NotReady with no message. Saying nothing would leave a red
  // badge and no consequence, so the page states what not-ready costs.
  it('names the consequence when Velero gave no reason', () => {
    const html = renderToString(<VeleroBackupRepositoryRenderer data={repo()} />)
    expect(html).toContain('legacy-app')
    expect(html).toContain('cannot be written')
  })

  it('says nothing alarming when the repository is ready', () => {
    const html = renderToString(
      <VeleroBackupRepositoryRenderer data={repo({ phase: 'Ready' })} />,
    )
    expect(html).not.toContain('Repository Not Ready')
    expect(html).not.toContain('cannot be written')
  })

  // The same dead end this integration closed on Backup and Restore: naming a
  // Velero object the reader cannot reach.
  it('lets the reader reach the location it writes into', () => {
    const html = renderToString(
      <VeleroBackupRepositoryRenderer data={repo()} onNavigate={() => {}} />,
    )
    expect(html).toContain('dr-replica')
    expect(html).toMatch(/role="button"|<button|cursor-pointer/)
  })

  // Maintenance frequency alone does not say whether maintenance is happening.
  // The pair does, which is why both are on the page.
  it('shows the maintenance schedule beside the last run', () => {
    const html = renderToString(<VeleroBackupRepositoryRenderer data={repo({ phase: 'Ready' })} />)
    expect(html).toContain('Maintenance Frequency')
    expect(html).toContain('1h0m0s')
    expect(html).toContain('Last Maintenance')
  })
})
