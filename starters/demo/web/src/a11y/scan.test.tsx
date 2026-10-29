/**
 * The accessibility report line's shape.
 *
 * A finding is reported with the selector of each flagged node, so an
 * incomplete result -- the one bucket jsdom cannot turn into a verdict -- still
 * points at the element it concerns. The jsdom sweep prints this line; without
 * the selector the hint names a rule and nothing else.
 */
import { describe, expect, it } from 'vitest'
import { formatFinding } from './scan.js'
import type { A11yFinding } from './scan.js'

describe('formatFinding', () => {
  it('names the flagged node selector in the report line', () => {
    const finding: A11yFinding = {
      rule: 'label-content-name-mismatch',
      impact: 'serious',
      nodes: 1,
      help: 'Elements must have their visible text as part of their accessible name',
      targets: ['button[aria-label="Notifications"]'],
    }
    expect(formatFinding(finding)).toContain(
      'button[aria-label="Notifications"]',
    )
  })
})
