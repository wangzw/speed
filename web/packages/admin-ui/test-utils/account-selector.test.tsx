import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { chooseAccount } from './account-selector.js'

const LABEL = 'Account'
const OPTION = 'Ada (ada)'

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
