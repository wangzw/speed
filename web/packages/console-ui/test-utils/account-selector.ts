/**
 * Drives the console-ui account picker in the suites.
 *
 * The picker reads accounts only after the operator types, so a test
 * selects an account the way an operator does: type into the labelled
 * combobox, wait for the search's options, and click the one that names
 * the account. The option's text is the account's display name beside its
 * id ("Ada (acc-2)").
 *
 * The search behind the popup is debounced and settles in several reads,
 * so the option list can re-render while a click's pointer sequence is in
 * flight; a click whose node was replaced in between never reaches React
 * and the picker keeps the previous answer. That is why the helper does
 * not stop at the click: it waits for the combobox to hold the option's
 * label and, while the popup is still open, clicks the option again. A
 * picker that never takes the choice still fails, and the helper's wait
 * stays far inside the suite's own budget.
 */

import { screen, waitFor } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'

/** How long the popup's first options may take to appear. */
const OPTION_TIMEOUT_MS = 5_000

/** How long one click has to move the combobox onto the chosen label. */
const SELECTION_TIMEOUT_MS = 1_000

/** How long the helper keeps re-clicking an option the picker keeps missing. */
const SELECTION_DEADLINE_MS = 5_000

export async function chooseAccount(
  user: UserEvent,
  label: string,
  typed: string,
  optionName: string | RegExp,
): Promise<void> {
  const combobox = () =>
    screen.getByRole('combobox', { name: label }) as HTMLInputElement

  const input = combobox()
  await user.click(input)
  await user.type(input, typed)

  const deadline = Date.now() + SELECTION_DEADLINE_MS
  for (;;) {
    const option = await screen.findByRole(
      'option',
      { name: optionName },
      { timeout: OPTION_TIMEOUT_MS },
    )
    const chosen = option.textContent ?? ''
    await user.click(option)
    try {
      await waitFor(
        () => {
          if (combobox().value !== chosen) {
            throw new Error('the picker has not taken the chosen account')
          }
        },
        { timeout: SELECTION_TIMEOUT_MS },
      )
      return
    } catch (error) {
      if (Date.now() > deadline) {
        throw error
      }
    }
  }
}
