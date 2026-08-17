import { isForbiddenError } from '../../../types/fetch-error'

interface LookupFailureNoteProps {
  /** Nulls are ignored, so callers can pass `[a.error, b.error]` from their hooks. */
  errors: unknown[]
  /** Tail of a sentence: "which clusters use this store". */
  what: string
  /** Rows are already on screen, so the failure shortened the list rather than
   *  emptying it. */
  incomplete?: boolean
}

/**
 * A reverse lookup that did not come back, told apart from one that found
 * nothing. Same split as RBACErrorSection and sharing its predicate: a denial is
 * expected and reads calm, a fault stays loud.
 */
export function LookupFailureNote({ errors, what, incomplete }: LookupFailureNoteProps) {
  const real = errors.filter(Boolean)
  if (real.length === 0) return null

  // A fault outranks a denial: only one of the two is actionable.
  const fault = real.find((e) => !isForbiddenError(e))
  if (!fault) {
    return (
      <div className="text-xs text-theme-text-tertiary">
        {incomplete
          ? `This may be incomplete: you don’t have permission to check ${what}.`
          : `You don’t have permission to check ${what}.`}
      </div>
    )
  }

  const message = fault instanceof Error ? fault.message : String(fault)
  return (
    <div className="text-xs text-red-400">
      {incomplete
        ? `This may be incomplete — could not check ${what}: ${message}`
        : `Could not check ${what}: ${message}`}
    </div>
  )
}
