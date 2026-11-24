import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AccountSelector } from './account-selector.js'
import type { AdminAccount } from '../operations.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import {
  createAdminOperations,
  makeAdminBackend,
} from '../../test-utils/operations.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const ACCOUNT_LABEL = 'Account'

function account(overrides: Partial<AdminAccount> = {}): AdminAccount {
  return {
    id: 'acc-2',
    kind: 'user',
    status: 'active',
    display_name: 'Ada',
    mfa_required: false,
    protected: false,
    password_set: true,
    identifiers: [
      { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
    ],
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function Harness({
  onChoose,
}: {
  readonly onChoose: (account: AdminAccount | null) => void
}) {
  const [value, setValue] = useState<AdminAccount | null>(null)
  return (
    <AccountSelector
      label={ACCOUNT_LABEL}
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
    const operations = createAdminOperations(
      makeAdminBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    expect(screen.getByLabelText(ACCOUNT_LABEL)).toBeInTheDocument()
    expect(listAccounts).not.toHaveBeenCalled()
  })

  it('searches the display name and the identifier kinds the text can name', async () => {
    const user = userEvent.setup()
    const listAccounts = vi.fn(() =>
      Promise.resolve({ accounts: [account()], next_cursor: '' }),
    )
    const operations = createAdminOperations(
      makeAdminBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText(ACCOUNT_LABEL), 'Ada')
    await waitForCondition(() => {
      expect(listAccounts).toHaveBeenCalledWith({
        limit: 20,
        display_name_prefix: 'Ada',
      })
      for (const kind of ['username', 'email']) {
        expect(listAccounts).toHaveBeenCalledWith({
          limit: 20,
          identifier_prefix: 'Ada',
          identifier_kind: kind,
        })
      }
    })
    expect(listAccounts).not.toHaveBeenCalledWith(
      expect.objectContaining({ identifier_kind: 'phone' }),
    )
  })

  it('does not read an identifier kind the text is too short to name', async () => {
    const user = userEvent.setup()
    const listAccounts = vi.fn(() =>
      Promise.resolve({ accounts: [account()], next_cursor: '' }),
    )
    const operations = createAdminOperations(
      makeAdminBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText(ACCOUNT_LABEL), 'Ab')
    await waitForCondition(() => {
      expect(listAccounts).toHaveBeenCalledWith({
        limit: 20,
        display_name_prefix: 'Ab',
      })
    })
    expect(listAccounts).toHaveBeenCalledTimes(1)
  })

  it('states an empty answer as no matches', async () => {
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
    const operations = createAdminOperations(
      makeAdminBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText(ACCOUNT_LABEL), 'Ada Lovelace')
    await waitForCondition(() => expect(listAccounts).toHaveBeenCalledTimes(1))
    // Wait for the display-name read to settle before reading the popup.
    await waitFor(() => {
      expect(screen.getByText(zhCN.accountSelector.empty)).toBeInTheDocument()
    })
    expect(screen.queryByText(zhCN.accountSelector.failed)).toBeNull()
    expect(
      screen.queryByText(zhCN.accountSelector.narrow),
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
    const operations = createAdminOperations(
      makeAdminBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText(ACCOUNT_LABEL), 'Ada')
    await waitForCondition(() => expect(listAccounts).toHaveBeenCalledTimes(3))
    await waitFor(() => {
      expect(screen.getByText(zhCN.accountSelector.empty)).toBeInTheDocument()
    })
    expect(screen.queryByText(zhCN.accountSelector.failed)).toBeNull()
  })

  it('reads a transport-synthesized 400 as not applicable, not as a failure', async () => {
    const user = userEvent.setup()
    const listAccounts = vi.fn(
      (params?: { readonly identifier_kind?: string }) =>
        params?.identifier_kind === undefined
          ? Promise.resolve({ accounts: [], next_cursor: '' })
          : Promise.reject({ code: 'client.http.400' }),
    )
    const operations = createAdminOperations(
      makeAdminBackend({ listAccounts }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText(ACCOUNT_LABEL), 'Ada')
    await waitForCondition(() => expect(listAccounts).toHaveBeenCalledTimes(3))
    await waitFor(() => {
      expect(screen.getByText(zhCN.accountSelector.empty)).toBeInTheDocument()
    })
    expect(screen.queryByText(zhCN.accountSelector.failed)).toBeNull()
  })

  it('hands the chosen account over when picked with the keyboard', async () => {
    const user = userEvent.setup()
    const chosen: (AdminAccount | null)[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({ accounts: [account()], next_cursor: '' }),
      }),
    )
    renderWithProviders(<Harness onChoose={(next) => chosen.push(next)} />, {
      operations,
    })
    await user.type(screen.getByLabelText(ACCOUNT_LABEL), 'Ada')
    await screen.findByRole('option', { name: 'Ada (ada)' })
    await user.keyboard('{ArrowDown}{Enter}')
    expect(chosen.at(-1)?.id).toBe('acc-2')
  })

  it('states a failed search instead of claiming no matches', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText(ACCOUNT_LABEL), 'Ada')
    expect(await screen.findByText(zhCN.accountSelector.failed)).toBeInTheDocument()
  })

  it('asks the operator to narrow the search when a read leaves a next page', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({ accounts: [account()], next_cursor: 'page-2' }),
      }),
    )
    renderWithProviders(<Harness onChoose={() => {}} />, { operations })
    await user.type(screen.getByLabelText(ACCOUNT_LABEL), 'Ada')
    expect(
      await screen.findByText(zhCN.accountSelector.narrow),
    ).toBeInTheDocument()
  })

  it('names the in-flight progress and scans clean while searching', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () => new Promise<never>(() => {}),
      }),
    )
    renderWithProviders(
      <>
        <h1>Permissions</h1>
        <Harness onChoose={() => {}} />
      </>,
      { operations },
    )
    await user.type(screen.getByLabelText(ACCOUNT_LABEL), 'Ada')
    const progress = await screen.findByRole('progressbar', {
      name: zhCN.accountSelector.searching,
    })
    expect(progress).toBeInTheDocument()
    await expectNoAxeViolations()
  })
})
