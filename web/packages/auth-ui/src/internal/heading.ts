/**
 * The semantic heading level a screen's titles render as, and the level
 * that continues the outline beneath one.
 *
 * A screen that may be mounted at different depths of the host's page
 * accepts the level that continues the real page's heading order. A
 * subheading (the invitation's "accepted" confirmation) takes the next
 * level beneath the screen's own title, never the same level: two
 * sibling h1s would misstate the outline.
 */

/** The semantic heading levels the screens' titles can render as. */
export type HeadingLevel = 'h1' | 'h2' | 'h3' | 'h4' | 'h5' | 'h6'

const LEVELS: readonly HeadingLevel[] = ['h1', 'h2', 'h3', 'h4', 'h5', 'h6']

/** The level one step below `level` (h6 stays h6: there is no h7). */
export function nextHeadingLevel(level: HeadingLevel): HeadingLevel {
  const index = LEVELS.indexOf(level)
  return LEVELS[Math.min(index + 1, LEVELS.length - 1)] ?? 'h6'
}
