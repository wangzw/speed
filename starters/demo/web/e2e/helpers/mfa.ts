/**
 * The two-step-verification section's enrollment name form.
 *
 * A method's entry opens a name form before its flow starts: the label travels
 * with the flow's first input, so it is collected first. The field is
 * prefilled with the browser's suggested name, which the caller overwrites
 * with a fixed, recognisable name: a later step that chooses the factor by
 * name then does not depend on the browser's suggestion. The prefill is
 * asserted non-empty first, so a form that stopped suggesting one is caught
 * before the overwrite hides it.
 */
import { expect, type Locator } from '@playwright/test'

import { copy } from './i18n'

/** Submit the name form that a method's entry opened. */
export async function submitFactorNameForm(
  mfa: Locator,
  name: string,
): Promise<void> {
  const field = mfa.getByRole('textbox', {
    name: copy('accountUi', 'mfa.enroll.labelField'),
    exact: true,
  })
  await expect(field).toBeVisible()
  expect((await field.inputValue()).trim()).not.toBe('')
  await field.fill(name)
  await mfa
    .getByRole('button', {
      name: copy('accountUi', 'mfa.enroll.confirmLabel'),
      exact: true,
    })
    .click()
}
