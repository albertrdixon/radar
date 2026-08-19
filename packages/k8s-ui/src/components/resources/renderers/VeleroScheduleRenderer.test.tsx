import { describe, expect, it } from 'vitest'
import { renderToString } from 'react-dom/server'
import { VeleroScheduleRenderer } from './VeleroScheduleRenderer'
import { ScheduleCell } from './velero-cells'

const schedule = (over: Record<string, unknown> = {}) => ({
  metadata: { name: 'nightly', namespace: 'velero' },
  spec: {
    schedule: '0 1 * * *',
    paused: false,
    template: { storageLocation: 'default', ttl: '720h0m0s' },
    ...(over.spec as object),
  },
  status: { phase: 'Enabled', ...(over.status as object) },
})

/**
 * A schedule is the only Velero object that describes work that has not happened
 * yet, so every sentence on this page is a prediction. Velero does not
 * re-validate a paused schedule, which bounds what those predictions may say.
 */
describe('what a schedule promises', () => {
  it('says a paused schedule creates nothing', () => {
    const html = renderToString(
      <VeleroScheduleRenderer data={schedule({ spec: { paused: true } })} />,
    )
    expect(html).toContain('No new backups will be created')
  })

  it('says an invalid active schedule is not running', () => {
    const html = renderToString(
      <VeleroScheduleRenderer
        data={schedule({ status: { phase: 'FailedValidation', validationErrors: ['invalid cron'] } })}
      />,
    )
    expect(html).toContain('no backups are being created')
    expect(html).toContain('invalid cron')
  })

  /**
   * The claim to guard. Velero validates a schedule when it is active and leaves
   * the result in place while it is paused, so a validation error recorded
   * before the pause may already be fixed in the spec. Radar's issue source
   * deliberately declines to raise this for a paused schedule; the page saying
   * "will not create backups when resumed until this is fixed" made the stronger
   * claim the data does not support.
   */
  it('does not predict what a paused schedule will do on resume', () => {
    const html = renderToString(
      <VeleroScheduleRenderer
        data={schedule({
          spec: { paused: true },
          status: { phase: 'FailedValidation', validationErrors: ['invalid cron'] },
        })}
      />,
    )
    expect(html).toContain('does not re-validate a paused schedule')
    expect(html).toContain('may or may not still be true')
    expect(html).not.toContain('when resumed until this is fixed')
  })

  // A paused schedule that is also rejected carries two independent facts, and
  // the badge only has room for one. The rejection is the one to act on, so
  // paused rides alongside — but it has to say so, or the row reads as a
  // schedule that is merely broken rather than one that is also switched off.
  it('keeps the pause visible on a row whose badge is showing the rejection', () => {
    const html = renderToString(
      <ScheduleCell
        resource={schedule({
          spec: { paused: true },
          status: { phase: 'FailedValidation', validationErrors: ['invalid cron'] },
        })}
        column="status"
      />,
    )
    expect(html).toContain('will not run until it is resumed')
  })

  // Every other Velero page reaches the thing it names. The template's storage
  // location is the location every backup this schedule creates gets written to.
  it('lets the reader reach the location its backups will be written to', () => {
    const html = renderToString(
      <VeleroScheduleRenderer data={schedule()} onNavigate={() => {}} />,
    )
    expect(html).toContain('default')
    expect(html).toMatch(/role="button"|<button|cursor-pointer/)
  })
})
