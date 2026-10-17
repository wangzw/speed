/**
 * Drives the console-ui account picker in the suites.
 *
 * The picker reads accounts only after the operator types, so a test
 * selects an account the way an operator does: type into the labelled
 * combobox, wait for the search's options, and click the one that names
 * the account. The option's text is the account's display name beside its
 * id ("Ada (acc-2)").
 */

import { screen } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'

export async function chooseAccount(
  user: UserEvent,
  label: string,
  typed: string,
  optionName: string | RegExp,
): Promise<void> {
  const input = screen.getByLabelText(label)
  await user.click(input)
  await user.type(input, typed)
  await user.click(await screen.findByRole('option', { name: optionName }))
}
