/**
 * chooseAccount: the helper survives what a re-render does to the popup.
 *
 * The picker's search settles in several reads, so its option list can
 * re-render -- replacing the option nodes -- while a click's pointer
 * sequence is in flight; a click whose node was replaced never reaches
 * React, and the picker keeps the previous answer. While a new query is in
 * flight the popup stays open with no listbox at all, because MUI renders
 * the listbox only while there are options and the read keeps no previous
 * data. These cases drive the helper against such a picker: it must
 * re-read and click again, and it must wait out a popup that is open but
 * temporarily listless instead of reporting a refusal.
 */
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { chooseAccount } from './account-selector.js'

const LABEL = 'Account'
const OPTION = 'Ada (ada)'

/**
 * A picker whose first `dropFirst` option clicks are swallowed, as a
 * re-render that replaces the option node can do. It then either takes the
 * next click or, with `closeAfterDrop`, closes the popup without taking
 * it. With `reloadAfterDrop` the popup stays open but its listbox goes
 * away for a moment, the way an in-flight query leaves it.
 */
function Picker({
  dropFirst,
  closeAfterDrop = false,
  reloadAfterDrop = false,
}: {
  readonly dropFirst: number
  readonly closeAfterDrop?: boolean
  readonly reloadAfterDrop?: boolean
}) {
  const [value, setValue] = useState('')
  const [open, setOpen] = useState(true)
  const [listboxVisible, setListboxVisible] = useState(true)
  const [clicks, setClicks] = useState(0)
  return (
    <div>
      <input
        role="combobox"
        aria-label={LABEL}
        aria-expanded={open && listboxVisible}
        value={value}
        onChange={(event) => setValue(event.target.value)}
      />
      {open ? (
        <div className="MuiAutocomplete-popper" role="presentation">
          {listboxVisible ? (
            <ul role="listbox">
              <li
                role="option"
                aria-selected={false}
                onClick={() => {
                  const next = clicks + 1
                  setClicks(next)
                  if (next <= dropFirst) {
                    if (closeAfterDrop) {
                      setOpen(false)
                    } else if (reloadAfterDrop) {
                      setListboxVisible(false)
                      setTimeout(() => setListboxVisible(true), 100)
                    }
                    return
                  }
                  setValue(OPTION)
                  setOpen(false)
                }}
              >
                {OPTION}
              </li>
            </ul>
          ) : (
            <p role="status">Searching accounts</p>
          )}
        </div>
      ) : null}
    </div>
  )
}

describe('chooseAccount', () => {
  it('re-clicks the option when a re-render swallowed the first click', async () => {
    const user = userEvent.setup()
    render(<Picker dropFirst={1} />)
    await chooseAccount(user, LABEL, 'Ada', OPTION)
    expect(
      (screen.getByRole('combobox', { name: LABEL }) as HTMLInputElement).value,
    ).toBe(OPTION)
  })

  it('waits while an in-flight query leaves the popup open with no listbox', async () => {
    const user = userEvent.setup()
    render(<Picker dropFirst={1} reloadAfterDrop />)
    await chooseAccount(user, LABEL, 'Ada', OPTION)
    expect(
      (screen.getByRole('combobox', { name: LABEL }) as HTMLInputElement).value,
    ).toBe(OPTION)
  })

  it('reports a picker that closes its popup without taking the choice', async () => {
    const user = userEvent.setup()
    render(<Picker dropFirst={1} closeAfterDrop />)
    await expect(
      chooseAccount(user, LABEL, 'Ada', OPTION),
    ).rejects.toThrow(/has not taken the chosen account/)
  })
})
