/**
 * The zod bridge of the form family: turn a zod schema into the
 * react-hook-form resolver a host hands to its `useForm` instance, so a
 * screen validates through one zod schema instead of hand-written rules.
 *
 * It plugs into the form family's validation-error contract -- the seam
 * `src/internal/validation-error.ts` documents: a resolver may emit any
 * message, and `FormField` renders it. This bridge emits ui-kit-namespace
 * keys, never zod's own English text, so every field error renders in the
 * active language and follows a language switch:
 *
 *  - a value that is missing (zod reports `invalid_type` with no input)
 *    becomes `form.required`;
 *  - any other failure becomes `form.invalid`.
 *
 * zod is a peer of this package, not a bundled dependency: the host
 * builds the schema, so the schema instance and this package's `ZodType`
 * must be the same class -- one zod in the tree, exactly like
 * `react-hook-form`.
 *
 *   const form = useForm<RegisterFields>({
 *     resolver: zodFormResolver(registerSchema),
 *   })
 */

import type { ZodType } from 'zod'
import type { FieldErrors, FieldValues, Resolver } from 'react-hook-form'
import { REQUIRED_ERROR_KEY } from './components/FormField.js'

/** The message key a non-required zod failure resolves to. */
export const INVALID_ERROR_KEY = 'form.invalid'

/** The shape this bridge reads off a zod issue -- kept structural so the
 * resolver does not pin a zod issue-type name across versions; the
 * `safeParse` result infers the real issue type at the call site. */
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

/**
 * Fold zod issues into react-hook-form's nested `errors` object: one
 * entry per field, keyed by the issue's path, carrying the ui-kit message
 * key. The first issue on a field wins; later issues on the same field
 * are dropped (a field shows one message).
 */
function toFieldErrors<TFieldValues extends FieldValues>(
  issues: readonly ZodIssueLike[],
): FieldErrors<TFieldValues> {
  const errors: Record<string, unknown> = {}
  for (const issue of issues) {
    // A path-less issue is a whole-form failure; react-hook-form reads
    // those under `root`.
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
 * Build the react-hook-form resolver for a zod schema. The resolver
 * validates the whole form and emits ui-kit-namespace messages for the
 * form family to render.
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
