/**
 * The object state vocabulary's display rule.
 *
 * The object view's state is a closed set (pending / uploaded / completed
 * / deleted). Each maps to a bundle key under the `state` section; a value
 * outside the set -- a state a future server adds, a value the client does
 * not know -- renders the generic `other` label rather than the raw wire
 * value, so the page never shows an untranslated token.
 */

const KNOWN_STATES = new Set<string>([
  'pending',
  'uploaded',
  'completed',
  'deleted',
])

/** The bundle key a state label resolves from, guarding the closed set. */
export function stateLabelKey(state: string): string {
  return KNOWN_STATES.has(state) ? `state.${state}` : 'state.other'
}
