/** Semantic heading levels for the screens' titles. */

export type HeadingLevel = 'h1' | 'h2' | 'h3' | 'h4' | 'h5' | 'h6'

const LEVELS: readonly HeadingLevel[] = ['h1', 'h2', 'h3', 'h4', 'h5', 'h6']

export function nextHeadingLevel(level: HeadingLevel): HeadingLevel {
  const index = LEVELS.indexOf(level)
  return LEVELS[Math.min(index + 1, LEVELS.length - 1)] ?? 'h6'
}
