import { AlertTriangle, XCircle, Loader2 } from 'lucide-react'
import type { VeleroMessage } from '../../../types/policy'

/**
 * The messages behind a run's error and warning counts.
 *
 * A Backup that reports "Warnings: 2" carries the number and nothing else — the
 * text is in a results file in object storage, and only Velero's controller can
 * hand out a link to it. So the counts rendered as two coloured digits with
 * nowhere to go: enough to worry an operator, not enough to act on.
 *
 * Fetched on a click rather than on page load. Reading them asks Velero to
 * create a DownloadRequest and then pulls an object out of storage, which is
 * real work to do on every drawer open, and the counts already answer "is
 * anything wrong". Same bargain as the network trace's probes.
 *
 * Both failure modes are rendered here rather than swallowed, because both are
 * ordinary and each points somewhere different: a controller that is not running
 * (nothing serves the request) and storage this process cannot reach (the link
 * resolves to somewhere only the cluster can see).
 */

/**
 * What the host supplies: the fetch and its outcome. Deliberately separate from
 * the counts, which the renderer already has from the object itself — a host
 * that had to pass those too would be restating data it was handed.
 */
export interface VeleroRunMessagesFetch {
  /** Undefined until fetched. */
  messages?: { warnings: VeleroMessage[]; errors: VeleroMessage[]; truncated?: boolean }
  loading?: boolean
  /** Rendered by the host when the fetch failed, so a denial is not silence and
   *  is not dressed up as a fault. Same slot the storage-location panel uses for
   *  its lookup, so both failures in this integration read the same way. */
  lookupNote?: React.ReactNode
  onFetch?: () => void
}

export interface VeleroRunMessagesProps extends VeleroRunMessagesFetch {
  errors: number
  warnings: number
}

export function VeleroRunMessages({
  errors,
  warnings,
  messages,
  loading,
  lookupNote,
  onFetch,
}: VeleroRunMessagesProps) {
  const total = errors + warnings
  // Nothing to explain, and no button for a run that reported nothing.
  if (total === 0) return null

  const rows = messages ? [...messages.errors, ...messages.warnings] : []
  const severityOf = (i: number) => (messages && i < messages.errors.length ? 'error' : 'warning')

  return (
    // aria-live so the outcome reaches a screen reader: this region swaps a
    // button for a list (or a failure) with no navigation, and the same
    // treatment PaneLoader and BoardSkeleton already use for content that
    // arrives late.
    <div className="mt-2 space-y-2" aria-live="polite">
      {!messages && (
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={onFetch}
            disabled={loading || !onFetch}
            className="btn-brand text-xs px-2 py-1 disabled:opacity-50"
          >
            {loading ? (
              <span className="flex items-center gap-1.5">
                <Loader2 className="w-3 h-3 animate-spin" />
                Asking Velero…
              </span>
            ) : (
              // Not "all N": the server caps what it returns, so a run with
              // thousands of them would have the button promise more than the
              // fetch can deliver. The counts are on the two lines above.
              total === 1 ? 'Show the message' : 'Show the messages'
            )}
          </button>
          <span className="text-xs text-theme-text-tertiary">
            Not stored on this object — Velero has to be asked for them.
          </span>
        </div>
      )}

      {lookupNote}

      {/* The count comes from the object; the messages come from a file written
          separately, so they can disagree — and the counts are rendered two
          lines above this list. A list of one under "Warnings: 2" is the same
          defect as any other number that cannot be reconciled with what is
          beside it. Truncation is excluded: it has its own line below. */}
      {messages && !messages.truncated && rows.length !== total && (
        <div className="text-xs text-theme-text-tertiary">
          {rows.length === 0
            ? `Velero reported ${total} ${total === 1 ? 'message' : 'messages'} on this run, but the results file it returned holds none.`
            : `Velero reported ${total} on this run and returned ${rows.length}.`}
        </div>
      )}

      {messages && rows.length > 0 && (
        <ul className="space-y-1">
          {rows.map((m, i) => (
            <li key={`${m.scope}-${i}`} className="flex items-start gap-2 text-xs">
              {severityOf(i) === 'error' ? (
                <XCircle className="w-3.5 h-3.5 mt-0.5 shrink-0 text-red-500 dark:text-red-400" aria-hidden />
              ) : (
                <AlertTriangle className="w-3.5 h-3.5 mt-0.5 shrink-0 text-amber-500 dark:text-amber-400" aria-hidden />
              )}
              {/* The icon and its colour were the only thing separating an error
                  from a warning, which is the distinction that decides whether
                  anyone acts. Said in text for anyone not seeing the icon. */}
              <span className="sr-only">{severityOf(i) === 'error' ? 'Error: ' : 'Warning: '}</span>
              <div className="min-w-0">
                {/* The separator is a real text node, not just the margin: to a
                    screen reader two adjacent spans run together, and this read
                    as "clusterfailed to list stub VolumeGroupSnapshotContents". */}
                <span className="text-theme-text-tertiary font-mono">{m.scope}</span>
                <span className="sr-only">: </span>
                <span className="text-theme-text-secondary break-words ml-1.5">{m.message}</span>
              </div>
            </li>
          ))}
        </ul>
      )}

      {messages?.truncated && (
        <div className="text-xs text-theme-text-tertiary">
          Only the first {rows.length} are shown.
        </div>
      )}
    </div>
  )
}
