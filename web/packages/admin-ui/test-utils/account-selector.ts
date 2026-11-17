/**
 * Drives the admin-ui account picker in the suites.
 *
 * The picker reads accounts only after the operator types, so a test
 * selects an account the way an operator does: type into the labelled
 * combobox, wait for the search's option, and click it. The option's text
 * is the account's display name with its login username ("Ada (ada)"),
 * either alone when only one resolves, and the neutral copy when neither
 * does -- never the opaque account id.
 *
 * The search is debounced (250ms) and settles in several reads, and the
 * list does not keep the previous answer while a new query is in flight,
 * so the popup can re-render -- replacing its option nodes -- while a
 * click's pointer sequence is in flight. A click whose node was replaced
 * in between never reaches React, and the picker keeps the previous
 * answer. The helper therefore re-reads the option and clicks it again
 * while the combobox has not taken the label and the popup is still open.
 *
 * MUI renders the listbox only while there are options, and `useAccounts`
 * keeps no previous data, so a query in flight leaves the popup open with
 * no listbox at all. That is not a refusal: the helper keeps waiting for
 * the listbox while the popup is mounted, and treats a closed popup as the
 * picker refusing the choice. The whole attempt is bounded by the suite's
 * shared async budget (`asyncUtilTimeout`, widened and scaled by the
 * caller's GATE_TIME_SCALE), so a loaded machine makes the wait longer,
 * not the click count; a picker that never takes the choice still fails.
 */

import { act, getConfig, screen } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'

/** How long the helper waits between reads while the popup offers no listbox. */
const POLL_INTERVAL_MS = 25

/** The refusal message: the popup closed without the picker taking a choice. */
const REFUSED = 'the picker has not taken the chosen account'

/**
 * Whether the picker's popup is mounted. MUI mounts the popper while the
 * popup is open and unmounts it when it closes; its listbox, unlike the
 * popper, exists only while there are options. The popper's utility class
 * is therefore the marker that separates "a query is in flight" from "the
 * picker refused the choice".
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

  /**
   * The option, once the popup offers it. While the popup is open with no
   * listbox the query is still in flight, so the wait continues; a popup
   * that has closed without an option is a picker that refused the choice.
   */
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
    // Re-read the option each attempt: a re-render may have replaced the
    // node the previous click targeted.
    const option = await findOption()
    const chosen = option.textContent ?? ''
    await user.click(option)
    // `user.click` flushes the React update it dispatches, so the
    // combobox's value reflects the choice when the click landed.
    if (combobox().value === chosen) {
      return
    }
    // The click was lost. Re-click only while the popup is still open:
    // one that closed without taking the choice is a picker that refused
    // the choice, not a lost click.
    if (!popupIsOpen() || Date.now() >= deadline) {
      throw new Error(REFUSED)
    }
  }
}
