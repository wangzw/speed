/**
 * The two-step-verification section's enrollment panel.
 *
 * A method's entry opens the panel with its flow already started. A passkey's
 * panel carries a name field beside the method's body and the one confirm;
 * the field suggests a browser/OS name, and the caller replaces it with a
 * fixed, recognisable string that a later step choosing the factor by name
 * can address regardless of the browser. The authenticator entry is single
 * and has no name field.
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
