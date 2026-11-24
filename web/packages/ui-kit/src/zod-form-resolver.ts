/** The zod bridge of the form family: a zod schema as a react-hook-form resolver. */

import type { ZodType } from 'zod'
import type { FieldErrors, FieldValues, Resolver } from 'react-hook-form'
import { REQUIRED_ERROR_KEY } from './components/FormField.js'

/** The message key a non-required zod failure resolves to. */
export const INVALID_ERROR_KEY = 'form.invalid'

/** Structural, so the resolver does not pin a zod issue-type name across versions. */
interface ZodIssueLike {
  readonly code?: string
  readonly input?: unknown
  readonly path: readonly PropertyKey[]
}

function messageKeyFor(issue: ZodIssueLike): string {
  return issue.code === 'invalid_type' && issue.input === undefined
    ? REQUIRED_ERROR_KEY
    : INVALID_ERROR_KEY
}

function toFieldErrors<TFieldValues extends FieldValues>(
  issues: readonly ZodIssueLike[],
): FieldErrors<TFieldValues> {
  const errors: Record<string, unknown> = {}
  for (const issue of issues) {
    // react-hook-form reads a whole-form failure under `root`.
    const segments = issue.path.length === 0 ? ['root'] : issue.path.map(String)
    let cursor = errors
    for (const segment of segments.slice(0, -1)) {
      const next = cursor[segment]
      if (next === undefined || typeof next !== 'object') {
        cursor[segment] = {}
      }
      cursor = cursor[segment] as Record<string, unknown>
    }
    const leaf = segments[segments.length - 1] as string
    if (cursor[leaf] === undefined) {
      cursor[leaf] = {
        type: issue.code ?? 'validation',
        message: messageKeyFor(issue),
      }
    }
  }
  return errors as FieldErrors<TFieldValues>
}

/**
 * Builds the react-hook-form resolver for a zod schema. Emits ui-kit
 * namespace keys only: `form.required` for a missing value, `form.invalid`
 * otherwise.
 */
export function zodFormResolver<TFieldValues extends FieldValues>(
  schema: ZodType<TFieldValues>,
): Resolver<TFieldValues> {
  return (values) => {
    const result = schema.safeParse(values)
    if (result.success) {
      return { values: result.data, errors: {} }
    }
    return {
      values: {},
      errors: toFieldErrors<TFieldValues>(result.error.issues),
    }
  }
}
