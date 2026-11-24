/**
 * Drives the account picker the way an operator does: type into the combobox,
 * wait for the option, click it, and re-click while the debounced search
 * replaces the option node, until the combobox takes the label or the popup closes.
 */

import { act, getConfig, screen } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'

const POLL_INTERVAL_MS = 25

/** The refusal message: the popup closed without the picker taking a choice. */
const REFUSED = 'the picker has not taken the chosen account'

/**
 * Whether the picker's popup is mounted. The popper marks an open popup; its
 * listbox exists only while there are options, so the popper separates a query
 * in flight from a refused choice.
 */
function popupIsOpen(): boolean {
  return document.querySelector('.MuiAutocomplete-popper') !== null
}

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

  const budget = getConfig().asyncUtilTimeout
  const deadline = Date.now() + budget

  /** The option once the popup offers it; a closed popup without one is a refusal. */
  const findOption = async (): Promise<Element> => {
    for (;;) {
      const option = screen.queryByRole('option', { name: optionName })
      if (option !== null) {
        return option
      }
      if (!popupIsOpen()) {
        throw new Error(REFUSED)
      }
      if (Date.now() >= deadline) {
        throw new Error(`${REFUSED} within ${budget}ms`)
      }
      // Sleep inside `act`, so a timer-driven update that brings the
      // listbox back is not reported as an unwrapped one.
      await act(
        () => new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS)),
      )
    }
  }

  for (;;) {
    // A re-render may have replaced the node the previous click targeted.
    const option = await findOption()
    const chosen = option.textContent ?? ''
    await user.click(option)
    // `user.click` flushes the React update it dispatches, so the
    // combobox's value reflects the choice when the click landed.
    if (combobox().value === chosen) {
      return
    }
    // The click was lost. Re-click only while the popup is open; a closed popup
    // without the choice is a refusal.
    if (!popupIsOpen() || Date.now() >= deadline) {
      throw new Error(REFUSED)
    }
  }
}
