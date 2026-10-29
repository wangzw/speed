/**
 * The account-menu trigger's accessible name, asserted as the rule it obeys.
 *
 * The trigger shows the signed-in principal's initials and carries an
 * accessible name that must contain what it shows (WCAG 2.5.3 Label in Name)
 * together with the identity it stands for. The glyph is read off the trigger
 * rather than re-derived here: the assertion states the rule, so it keeps
 * holding if the initials derivation changes.
 */

import { expect, type Locator } from '@playwright/test'

/** Escapes a literal for use inside a regular expression. */
function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&')
}

/** Asserts the trigger is named "«glyph» «identity»" for the identity shown. */
export async function expectAccountMenuName(
  trigger: Locator,
  identity: string,
): Promise<void> {
  const glyph = (await trigger.textContent())?.trim() ?? ''
  expect(glyph, 'the trigger renders the initials glyph').not.toBe('')
  await expect(trigger).toHaveAccessibleName(
    new RegExp(`^${escapeRegExp(glyph)} ${escapeRegExp(identity)}$`),
  )
}
