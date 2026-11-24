import { expect, type Locator } from '@playwright/test'

import { copy } from './i18n'

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
