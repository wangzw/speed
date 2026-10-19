/**
 * The sign-in surface's start gate.
 *
 * Every channel starts one login flow, and a host may want to decide per
 * attempt whether that flow may begin -- a consent wall, an experiment
 * assignment, a product-level block. `beforeStart` is that seam: it
 * receives the method about to start plus the surface's presentational
 * mode (the screen supplies it; a standalone form has no mode to report)
 * and answers whether the flow begins. A `false` answer starts nothing
 * and renders no error: the viewer's action was refused before it left
 * the page, so there is no server answer to show. Only the first step is
 * gated -- the flow's own later answers are not, because the gate decides
 * whether a flow may begin, not how it proceeds.
 *
 * A throwing gate is contained like every other host callback: the flow
 * does not start, and the surface renders no error banner (the throw is a
 * host bug, not a refusal of a login).
 */

/** The presentational mode of the sign-in surface (both run the one flow). */
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

/**
 * Resolve a gate's answer. An absent gate allows; a throwing gate is a
 * contained refusal (see the file header).
 */
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
