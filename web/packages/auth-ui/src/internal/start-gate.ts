/** The sign-in surface's start gate and its context types. */

/** The presentational mode of the sign-in surface. */
export type SignInMode = 'sign-in' | 'register'

/** What the start gate receives: the method and, when known, the mode. */
export interface SignInStartContext {
  /** The method name the flow would start with. */
  readonly method: string
  /** The presentational mode; omitted by a standalone form. */
  readonly mode?: SignInMode
}

/**
 * Decides whether a channel may begin its flow. Returning false (or a
 * promise of false) starts nothing and shows no error.
 */
export type SignInBeforeStart = (
  context: SignInStartContext,
) => boolean | Promise<boolean>

export async function allowedToStart(
  gate: SignInBeforeStart | undefined,
  context: SignInStartContext,
): Promise<boolean> {
  if (gate === undefined) {
    return true
  }
  try {
    return (await gate(context)) !== false
  } catch {
    return false
  }
}
