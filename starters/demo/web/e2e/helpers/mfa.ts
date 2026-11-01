/**
 * The two-step-verification section's enrollment name form.
 *
 * A method's entry opens a name form before its flow starts: the label travels
 * with the flow's first input, so it is collected first. The field is
 * prefilled with the browser's suggested name and may be submitted empty; a
 * caller that needs a row distinguishable by name supplies one.
 */
import { expect, type Locator } from '@playwright/test'

import { copy } from './i18n'

/** Submit the name form that a method's entry opened. */
export async function submitFactorNameForm(
  mfa: Locator,
  name?: string,
): Promise<void> {
  const field = mfa.getByRole('textbox', {
    name: copy('accountUi', 'mfa.enroll.labelField'),
    exact: true,
  })
  await expect(field).toBeVisible()
  expect((await field.inputValue()).trim()).not.toBe('')
  if (name !== undefined) {
    await field.fill(name)
  }
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'mfa.enroll.confirmLabel'),
      exact: true,
    })
    .click()
}
