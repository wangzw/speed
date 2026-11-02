/**
 * The two-step-verification section's enrollment panel.
 *
 * A method's entry opens the panel with its flow already started: the name
 * field, the method's body and one confirm share the panel. The field starts
 * empty, and the typed name is written to the row the flow adds; a later step
 * that chooses the factor by name therefore addresses a fixed, recognisable
 * string rather than the browser's suggestion.
 */
import { expect, type Locator } from '@playwright/test'

import { copy } from './i18n'

/** Fill the name field of a method's open enrollment panel. */
export async function fillFactorName(
  mfa: Locator,
  name: string,
): Promise<void> {
  const field = mfa.getByRole('textbox', {
    name: copy('accountUi', 'mfa.enroll.labelField'),
    exact: true,
  })
  await expect(field).toBeVisible()
  // The field carries no prefill: the reader's name is the only value.
  await expect(field).toHaveValue('')
  await field.fill(name)
}
