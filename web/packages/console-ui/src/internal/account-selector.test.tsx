/**
 * AccountSelector: the searchable account picker.
 *
 * It issues no account read until the operator types; a typed name is
 * searched as a display-name prefix and as each identifier kind's prefix;
 * a choice hands the caller the account's id, by pointer or by keyboard;
 * and a failed search is stated rather than rendered as "no matches".
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AccountSelector } from './account-selector.js'
import type { ConsoleAccount } from '../operations.js'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../../test-utils/operations.js'

function account(overrides: Partial<ConsoleAccount> = {}): ConsoleAccount {
  return {
    id: 'acc-2',
    kind: 'user',
    status: 'active',
    display_name: 'Ada',
    mfa_required: false,
    protected: false,
    password_set: true,
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

/** A controlled stand-in that records the account each choice hands over. */
function Harness({
  onChoose,
}: {
  readonly onChoose: (account: ConsoleAccount | null) => void
}) {
  const [value, setValue] = useState<ConsoleAccount | null>(null)
  return (
    <AccountSelector
      label="账户"
      value={value}
      onChange={(next) => {
        setValue(next)
        onChoose(next)
      }}
    />
  )
}

describe('AccountSelector', () => {
  it('reads nothing until the operator types', () => {
    const listAccounts = vi.fn(() =>
      Promise.resolve({ accounts: [account()], next_cursor: '' }),
    )
    const operations = createConsoleOperations(
      makeConsoleBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    expect(screen.getByLabelText('账户')).toBeInTheDocument()
    expect(listAccounts).not.toHaveBeenCalled()
  })

  it('searches the display name and every identifier kind', async () => {
    const user = userEvent.setup()
    const listAccounts = vi.fn(() =>
      Promise.resolve({ accounts: [account()], next_cursor: '' }),
    )
    const operations = createConsoleOperations(
      makeConsoleBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText('账户'), 'Ada')
    await vi.waitFor(() => {
      expect(listAccounts).toHaveBeenCalledWith({
        limit: 20,
        display_name_prefix: 'Ada',
      })
      for (const kind of ['username', 'email', 'phone']) {
        expect(listAccounts).toHaveBeenCalledWith({
          limit: 20,
          identifier_prefix: 'Ada',
          identifier_kind: kind,
        })
      }
    })
  })

  it('hands the chosen account over when picked with the keyboard', async () => {
    const user = userEvent.setup()
    const chosen: (ConsoleAccount | null)[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccounts: () =>
          Promise.resolve({ accounts: [account()], next_cursor: '' }),
      }),
    )
    renderWithProviders(<Harness onChoose={(next) => chosen.push(next)} />, {
      operations,
    })
    await user.type(screen.getByLabelText('账户'), 'Ada')
    await screen.findByRole('option', { name: 'Ada (acc-2)' })
    // The combobox is operable without a pointer: arrow keys move through
    // the options and Enter selects.
    await user.keyboard('{ArrowDown}{Enter}')
    expect(chosen.at(-1)?.id).toBe('acc-2')
  })

  it('states a failed search instead of claiming no matches', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccounts: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText('账户'), 'Ada')
    expect(await screen.findByText('账户搜索失败')).toBeInTheDocument()
  })
})
