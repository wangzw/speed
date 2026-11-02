/**
 * The two-step-verification section's enrollment panel.
 *
 * A method's entry opens the panel with its flow already started: the name
 * field, the method's body and one confirm share the panel. The field
 * suggests a browser/OS name; the caller replaces it with a fixed,
 * recognisable string that a later step choosing the factor by name can
 * address regardless of the browser.
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
  // The suggested name is a value, not a placeholder: fill replaces it.
  await expect(field).not.toHaveValue('')
  await field.fill(name)
}
