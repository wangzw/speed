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
  // The entry renders the initials and the identity side by side: the text
  // strips the identity off the end to read the glyph, and the accessible
  // name is that glyph, a space, and the identity.
  const text = (await trigger.textContent())?.trim() ?? ''
  expect(text.endsWith(identity), 'the trigger renders the identity').toBe(
    true,
  )
  const glyph = text.slice(0, text.length - identity.length).trim()
  expect(glyph, 'the trigger renders the initials glyph').not.toBe('')
  await expect(trigger).toHaveAccessibleName(
    new RegExp(`^${escapeRegExp(glyph)} ${escapeRegExp(identity)}$`),
  )
}
