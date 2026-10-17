/**
 * AccountSelector: the searchable account picker.
 *
 * It issues no account read until the operator types; a typed name is
 * searched as a display-name prefix and as the identifier kinds its shape
 * can name; a choice hands the caller the account's id, by pointer or by
 * keyboard; an empty answer is stated as "no matches"; and a failed search
 * is stated rather than rendered as "no matches".
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AccountSelector } from './account-selector.js'
import type { ConsoleAccount } from '../operations.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
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

  it('searches the display name and the identifier kinds the text can name', async () => {
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
      // An ASCII word is both a username and an email prefix.
      for (const kind of ['username', 'email']) {
        expect(listAccounts).toHaveBeenCalledWith({
          limit: 20,
          identifier_prefix: 'Ada',
          identifier_kind: kind,
        })
      }
    })
    // "Ada" is not the head of an E.164 number, so the listing would refuse
    // that read: it is never issued.
    expect(listAccounts).not.toHaveBeenCalledWith(
      expect.objectContaining({ identifier_kind: 'phone' }),
    )
  })

  it('does not read an identifier kind the text is too short to name', async () => {
    const user = userEvent.setup()
    const listAccounts = vi.fn(() =>
      Promise.resolve({ accounts: [account()], next_cursor: '' }),
    )
    const operations = createConsoleOperations(
      makeConsoleBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText('账户'), 'Ab')
    await vi.waitFor(() => {
      expect(listAccounts).toHaveBeenCalledWith({
        limit: 20,
        display_name_prefix: 'Ab',
      })
    })
    // Every identifier kind has a three-character floor on the listing.
    expect(listAccounts).toHaveBeenCalledTimes(1)
  })

  it('states an empty answer as no matches', async () => {
    const user = userEvent.setup()
    // The listing refuses every identifier kind for a text with a space in
    // it; the display-name prefix read is the only one it accepts and it
    // finds nothing. The popup must say so rather than claim a failure.
    const listAccounts = vi.fn(
      (params?: { readonly identifier_kind?: string }) =>
        params?.identifier_kind === undefined
          ? Promise.resolve({ accounts: [], next_cursor: '' })
          : Promise.reject({
              status: 400,
              code: 'authn.bad_request',
              message: 'refused',
              auth: false,
            }),
    )
    const operations = createConsoleOperations(
      makeConsoleBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText('账户'), 'Ada Lovelace')
    // The display-name read is the only one issued; wait for it to settle
    // before reading the popup, so the empty state is the settled one.
    await vi.waitFor(() => expect(listAccounts).toHaveBeenCalledTimes(1))
    await waitFor(() => {
      expect(screen.getByText('没有匹配的账户')).toBeInTheDocument()
    })
    expect(screen.queryByText('账户搜索失败')).toBeNull()
    // Nothing was paged out either, so no narrowing hint is due.
    expect(
      screen.queryByText('匹配的账户较多，输入更多字符以缩小搜索范围'),
    ).toBeNull()
  })

  it('reads a refused identifier kind as not applicable, not as a failure', async () => {
    const user = userEvent.setup()
    const listAccounts = vi.fn(
      (params?: { readonly identifier_kind?: string }) =>
        params?.identifier_kind === undefined
          ? Promise.resolve({ accounts: [], next_cursor: '' })
          : Promise.reject({
              status: 400,
              code: 'authn.bad_request',
              message: 'refused',
              auth: false,
            }),
    )
    const operations = createConsoleOperations(
      makeConsoleBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText('账户'), 'Ada')
    // The name read plus the two applicable identifier reads.
    await vi.waitFor(() => expect(listAccounts).toHaveBeenCalledTimes(3))
    // Re-query rather than hold the element: the popup re-renders as the
    // reads settle and a held node can be replaced between find and assert.
    await waitFor(() => {
      expect(screen.getByText('没有匹配的账户')).toBeInTheDocument()
    })
    expect(screen.queryByText('账户搜索失败')).toBeNull()
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

  it('asks the operator to narrow the search when a read leaves a next page', async () => {
    const user = userEvent.setup()
    // Every read answers a full page; the merged rows are only the first
    // page of each, so the control must not present them as the whole answer.
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccounts: () =>
          Promise.resolve({ accounts: [account()], next_cursor: 'page-2' }),
      }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText('账户'), 'Ada')
    expect(
      await screen.findByText('匹配的账户较多，输入更多字符以缩小搜索范围'),
    ).toBeInTheDocument()
  })

  it('names the in-flight progress and scans clean while searching', async () => {
    const user = userEvent.setup()
    // A read that never settles holds the searching state open, the state
    // the settled page scans never reach.
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccounts: () => new Promise<never>(() => {}),
      }),
    )
    renderWithProviders(
      <>
        <h1>权限</h1>
        <Harness onChoose={() => {}} />
      </>,
      { operations },
    )
    await user.type(screen.getByLabelText('账户'), 'Ada')
    const progress = await screen.findByRole('progressbar', {
      name: '正在搜索账户',
    })
    expect(progress).toBeInTheDocument()
    await expectNoAxeViolations()
  })
})
