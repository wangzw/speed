/** AccountsPage suite: list read, search, filtering and paging cases. */

import { describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CONFIRM_ARM_LOCKOUT_MS } from '@speed/ui-kit'
import { AccountsPage } from './AccountsPage.js'
import { adminOperations } from './operations.js'
import type { AdminAccount } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { bindAdminRequest } from '../test-utils/request-rig.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
  type AdminBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import {
  fill,
  NEXT_PAGE_LABEL,
  PREVIOUS_PAGE_LABEL,
  withAccounts,
  search,
  openDetail,
  deleteThroughConfirm,
  CONFIRM_AGAIN_LABEL,
  UNKNOWN_TOTAL_LABEL,
  CONFIRM_LABEL,
} from '../test-utils/accounts-page.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

describe('AccountsPage', () => {
  it('renders the empty state when no account matches', async () => {
    const operations = withAccounts({
      listAccounts: () => Promise.resolve({ accounts: [], next_cursor: '' }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    expect(await screen.findByText(zhCN.accounts.empty.title)).toBeInTheDocument()
  })

  it('lists accounts and shows their status', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.getByText(zhCN.accounts.status.active)).toBeInTheDocument()
  })

  it('keeps its short-value and identity columns on one line', async () => {
    // The status cell must not split the active-status label across two
    // lines, and a display name with a space must not break at it. jsdom does
    // no layout, so the proof is the declared `white-space: nowrap` on the
    // rendered cells; the table's TableContainer scroll is what absorbs the
    // resulting width (asserted in ui-kit's DataTable suite). The account id
    // is not a column and is not copyable.
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    const statusCell = (await screen.findByText(zhCN.accounts.status.active)).closest('td')
    const nameCell = screen.getByText('Ada Lovelace').closest('td')
    for (const cell of [statusCell, nameCell]) {
      expect(cell).not.toBeNull()
      expect(cell).toHaveStyle({ whiteSpace: 'nowrap' })
    }
    expect(screen.queryByText('acc-1')).toBeNull()
  })

  it('sends the identifier prefix and its kind when the search is submitted', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/auth/accounts') {
        return { accounts: [], next_cursor: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    renderWithProviders(<AccountsPage />, { operations: adminOperations })
    await user.click(screen.getByLabelText(zhCN.accounts.identifierKindLabel))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.identifierKind.email }))
    await user.type(screen.getByLabelText(zhCN.accounts.identifierLabel), 'ada')
    await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
    await waitForCondition(() => {
      expect(calls).toContainEqual({
        method: 'GET',
        path: '/api/v1/system/auth/accounts',
        query: { identifier_prefix: 'ada', identifier_kind: 'email', limit: 100 },
      })
    })
  })

  it('sends the status and kind filters when the search is submitted', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/auth/accounts') {
        return { accounts: [], next_cursor: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    renderWithProviders(<AccountsPage />, { operations: adminOperations })
    await user.click(screen.getByLabelText(zhCN.accounts.filter.status))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.status.disabled }))
    await user.click(screen.getByLabelText(zhCN.accounts.filter.kind))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.create.kind.service }))
    await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
    await waitForCondition(() => {
      expect(calls).toContainEqual({
        method: 'GET',
        path: '/api/v1/system/auth/accounts',
        query: { status: 'disabled', kind: 'service', limit: 100 },
      })
    })
  })

  it('sends the display-name prefix when the search is submitted', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/auth/accounts') {
        return { accounts: [], next_cursor: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    renderWithProviders(<AccountsPage />, { operations: adminOperations })
    await user.type(screen.getByLabelText(zhCN.accounts.displayNamePrefixLabel), 'Ada')
    await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
    await waitForCondition(() => {
      expect(calls).toContainEqual({
        method: 'GET',
        path: '/api/v1/system/auth/accounts',
        query: { display_name_prefix: 'Ada', limit: 100 },
      })
    })
  })

  it('omits the status and kind filters when they are left at all', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/auth/accounts') {
        return { accounts: [], next_cursor: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    renderWithProviders(<AccountsPage />, { operations: adminOperations })
    await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
    await waitForCondition(() => {
      const call = calls.find((entry) => entry.path === '/api/v1/system/auth/accounts')
      expect(call).toBeDefined()
      // No filter is set, so the call carries the page size and nothing else.
      expect(call?.query ?? {}).toEqual({ limit: 100 })
    })
  })

  it('pages forward and back through the cursor read without repeating a page', async () => {
    const user = userEvent.setup()
    const pages: Record<
      string,
      { accounts: AdminAccount[]; next_cursor: string }
    > = {
      '': {
        accounts: [accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' })],
        next_cursor: 'cursor-1',
      },
      'cursor-1': {
        accounts: [accountFixture({ id: 'acc-2', display_name: 'Grace Hopper' })],
        next_cursor: 'cursor-2',
      },
      'cursor-2': {
        accounts: [
          accountFixture({ id: 'acc-3', display_name: 'Katherine Johnson' }),
        ],
        next_cursor: '',
      },
    }
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) =>
          Promise.resolve(
            pages[params?.after ?? ''] ?? { accounts: [], next_cursor: '' },
          ),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    expect(await screen.findByText('Grace Hopper')).toBeInTheDocument()
    expect(screen.queryByText('Ada Lovelace')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    expect(await screen.findByText('Katherine Johnson')).toBeInTheDocument()
    expect(screen.queryByText('Grace Hopper')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL }))
    expect(await screen.findByText('Grace Hopper')).toBeInTheDocument()
    expect(screen.queryByText('Katherine Johnson')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL }))
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.queryByText('Grace Hopper')).not.toBeInTheDocument()
  })

  it('drops the list result when the page changes', async () => {
    // A page move is a change of the list's context: the previous page's
    // refused row action does not describe the page now shown.
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) =>
          Promise.resolve(
            params?.after === undefined
              ? {
                  accounts: [
                    accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
                  ],
                  next_cursor: 'cursor-1',
                }
              : {
                  accounts: [
                    accountFixture({ id: 'acc-2', display_name: 'Grace Hopper' }),
                  ],
                  next_cursor: '',
                },
          ),
        deleteAccount: () =>
          Promise.reject({ code: 'authn.account_not_found' }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')
    await deleteThroughConfirm(user)
    expect(await screen.findByRole('alert')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    expect(await screen.findByText('Grace Hopper')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('drops the list result when a filter is submitted', async () => {
    // A submitted filter is a new list: the result written under the
    // previous filter does not describe it.
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [
              accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
            ],
            next_cursor: '',
          }),
        deleteAccount: () =>
          Promise.reject({ code: 'authn.account_not_found' }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')
    await deleteThroughConfirm(user)
    expect(await screen.findByRole('alert')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText(zhCN.accounts.identifierLabel), {
      target: { value: 'grace' },
    })
    await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
  })

  it('keeps the previous-page control on an empty later page', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) =>
          Promise.resolve(
            params?.after === undefined
              ? {
                  accounts: [
                    accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
                  ],
                  next_cursor: 'cursor-1',
                }
              : { accounts: [], next_cursor: '' },
          ),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))

    // The empty page keeps the table's footer: the read answered with no
    // rows, but the page before it is still reachable.
    const previous = await screen.findByRole('button', {
      name: PREVIOUS_PAGE_LABEL,
    })
    expect(previous).toBeEnabled()

    await user.click(previous)
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
  })

  it('keeps the previous-page control when a later page fails to read', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) =>
          params?.after === undefined
            ? Promise.resolve({
                accounts: [
                  accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
                ],
                next_cursor: 'cursor-1',
              })
            : Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    expect(
      await screen.findByText(zhCN.accounts.error.title),
    ).toBeInTheDocument()

    // The failed page renders its error placeholder, but the footer is not
    // part of that branch: the page before it stays reachable.
    const previous = screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL })
    expect(previous).toBeEnabled()
    await user.click(previous)
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
  })

  it('keeps focus inside the pagination controls after advancing', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) =>
          Promise.resolve(
            params?.after === undefined
              ? {
                  accounts: [
                    accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
                  ],
                  next_cursor: 'cursor-1',
                }
              : {
                  accounts: [
                    accountFixture({ id: 'acc-2', display_name: 'Grace Hopper' }),
                  ],
                  next_cursor: '',
                },
          ),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    expect(await screen.findByText('Grace Hopper')).toBeInTheDocument()

    // The page branch swapped under the footer; focus must not fall to the
    // document, or a keyboard operator loses their place.
    const previous = screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL })
    const next = screen.getByRole('button', { name: NEXT_PAGE_LABEL })
    expect(document.body).not.toBe(document.activeElement)
    expect([next, previous]).toContain(document.activeElement)
  })

  it('disables the next-page control on the last page', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
            next_cursor: '',
          }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: NEXT_PAGE_LABEL }),
    ).toBeDisabled()
  })

  it('does not advance while the next page is still loading', async () => {
    const user = userEvent.setup()
    let finishSecondPage: (() => void) | undefined
    const calls = bindAdminRequest((call) => {
      if (call.path !== '/api/v1/system/auth/accounts') {
        throw new Error(`unexpected request: ${call.path}`)
      }
      const query = call.query as { readonly after?: string } | undefined
      if (query?.after === undefined) {
        return {
          accounts: [
            accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
          ],
          next_cursor: 'cursor-1',
        }
      }
      return new Promise((resolve) => {
        finishSecondPage = () =>
          resolve({
            accounts: [
              accountFixture({ id: 'acc-2', display_name: 'Grace Hopper' }),
            ],
            next_cursor: '',
          })
      })
    })
    renderWithProviders(<AccountsPage />, { operations: adminOperations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    const next = screen.getByRole('button', { name: NEXT_PAGE_LABEL })
    // An unsettled page has no cursor to advance from, so the control is
    // disabled and a click cannot skip a page.
    expect(next).toBeDisabled()
    fireEvent.click(next)

    const forwardReads = calls.filter(
      (call) =>
        call.method === 'GET' &&
        call.path === '/api/v1/system/auth/accounts' &&
        (call.query as { readonly after?: string } | undefined)?.after ===
          'cursor-1',
    )
    expect(forwardReads).toHaveLength(1)

    await act(async () => {
      finishSecondPage?.()
    })
    expect(await screen.findByText('Grace Hopper')).toBeInTheDocument()
  })

  it('disables paging in both directions while a row write is in flight', async () => {
    const user = userEvent.setup()
    let finishDelete: (() => void) | undefined
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) =>
          Promise.resolve(
            params?.after === undefined
              ? {
                  accounts: [
                    accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
                  ],
                  next_cursor: 'cursor-1',
                }
              : {
                  accounts: [
                    accountFixture({ id: 'acc-2', display_name: 'Grace Hopper' }),
                  ],
                  next_cursor: 'cursor-2',
                },
          ),
        deleteAccount: () =>
          new Promise<void>((resolve) => {
            finishDelete = resolve
          }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    expect(await screen.findByText('Grace Hopper')).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', {
        name: fill(zhCN.accounts.deleteAria, { name: 'Grace Hopper' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    // The in-flight delete owns the list's context: paging away from it is
    // disabled in both directions until it settles.
    const previous = screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL })
    const next = screen.getByRole('button', { name: NEXT_PAGE_LABEL })
    expect(previous).toBeDisabled()
    expect(next).toBeDisabled()

    await act(async () => {
      finishDelete?.()
    })
    await waitFor(() => {
      expect(next).toBeEnabled()
    })
    expect(previous).toBeEnabled()
  })

  it('returns to the first page when a filter is submitted', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest((call) => {
      if (call.path !== '/api/v1/system/auth/accounts') {
        throw new Error(`unexpected request: ${call.path}`)
      }
      const query = call.query as
        | { readonly after?: string; readonly identifier_prefix?: string }
        | undefined
      if (query?.after !== undefined) {
        return {
          accounts: [
            accountFixture({ id: 'acc-9', display_name: 'Grace Hopper' }),
          ],
          next_cursor: '',
        }
      }
      return {
        accounts: [
          accountFixture({
            id: 'acc-1',
            display_name:
              query?.identifier_prefix === undefined
                ? 'Ada Lovelace'
                : 'Ada Filtered',
          }),
        ],
        next_cursor: 'cursor-1',
      }
    })
    renderWithProviders(<AccountsPage />, { operations: adminOperations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()

    await user.type(screen.getByLabelText(zhCN.accounts.identifierLabel), 'ada')
    await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
    expect(await screen.findByText('Ada Filtered')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    expect(await screen.findByText('Grace Hopper')).toBeInTheDocument()

    await user.type(
      screen.getByLabelText(zhCN.accounts.displayNamePrefixLabel),
      'Gr',
    )
    await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
    expect(await screen.findByText('Ada Filtered')).toBeInTheDocument()
    expect(screen.queryByText('Grace Hopper')).not.toBeInTheDocument()

    const reads = calls.filter(
      (call) =>
        call.method === 'GET' &&
        call.path === '/api/v1/system/auth/accounts',
    )
    // The filter submission reads its first page: the request carries the
    // new filters and no cursor.
    expect(reads[reads.length - 1]?.query).toEqual({
      identifier_prefix: 'ada',
      identifier_kind: 'username',
      display_name_prefix: 'Gr',
      limit: 100,
    })
  })

  it('shows the page range without a total count', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [
              accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
            ],
            next_cursor: 'cursor-1',
          }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Ada Lovelace')
    expect(
      screen.getByText(fill(UNKNOWN_TOTAL_LABEL, { from: '1', to: '100' })),
    ).toBeInTheDocument()
  })

  it('has no axe violations on a later page', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) =>
          Promise.resolve(
            params?.after === undefined
              ? {
                  accounts: [
                    accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
                  ],
                  next_cursor: 'cursor-1',
                }
              : {
                  accounts: [
                    accountFixture({ id: 'acc-2', display_name: 'Grace Hopper' }),
                  ],
                  next_cursor: '',
                },
          ),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Ada Lovelace')
    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    await screen.findByText('Grace Hopper')
    await expectNoAxeViolations()
  })

  it('renders the retryable error placeholder when the list read fails', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccounts: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    expect(
      await screen.findByText(zhCN.accounts.error.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.accounts.error.description),
    ).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the list read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccounts: () => Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.accounts.retry }),
    ).toBeNull()
  })

  it('renders the unavailable placeholder with a retry when the detail read answers 404', async () => {
    const user = userEvent.setup()
    let calls = 0
    const getAccount = () => {
      calls += 1
      return calls === 1
        ? Promise.reject({ status: 404, code: 'client.http.404' })
        : Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' }))
    }
    const operations = withAccounts({ getAccount })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: 'Ada Lovelace' })

    // A by-id 404 replaces the panel's content with the "not found or
    // unavailable" placeholder and keeps the retry that re-reads.
    expect(
      await within(panel).findByText(zhCN.detail.notFound.title),
    ).toBeInTheDocument()
    await user.click(
      within(panel).getByRole('button', { name: zhCN.accounts.detail.retry }),
    )
    expect(
      await within(panel).findByText(
        fill(zhCN.accounts.detail.displayNameLine, {
          value: 'Ada Lovelace',
        }),
      ),
    ).toBeInTheDocument()
    expect(calls).toBe(2)
  })

  it('withdraws the stale marker while the list retry is in flight', async () => {
    const user = userEvent.setup()
    let searchedReads = 0
    let releaseRetry: (() => void) | undefined
    const listAccounts = (
      params?: Parameters<AdminBackend['listAccounts']>[0],
    ): ReturnType<AdminBackend['listAccounts']> => {
      // The page's own opening read (no filter) is not the one under test.
      if (params?.identifier_prefix === undefined) {
        return Promise.resolve({
          accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
          next_cursor: '',
        })
      }
      searchedReads += 1
      if (searchedReads === 1) {
        return Promise.resolve({
          accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
          next_cursor: '',
        })
      }
      if (searchedReads === 2) {
        return Promise.reject(new Error('refresh failed'))
      }
      // The retry stays in flight: the marker must already be withdrawn.
      return new Promise((resolve) => {
        releaseRetry = () =>
          resolve({
            accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
            next_cursor: '',
          })
      })
    }
    const operations = withAccounts({ listAccounts })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')

    // A failed refresh keeps the settled list and marks it stale.
    await user.click(screen.getByRole('button', { name: zhCN.common.refresh }))
    const stale = await screen.findByRole('alert')
    expect(stale).toHaveTextContent(zhCN.stale.description)

    // The retry is in flight: the marker goes away rather than staying
    // beside content the page is already re-reading.
    await user.click(
      within(stale).getByRole('button', { name: zhCN.accounts.retry }),
    )
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(screen.getByText('Ada Lovelace')).toBeInTheDocument()

    await act(async () => {
      releaseRetry?.()
    })
  })

  it('re-reads the account list on refresh', async () => {
    const user = userEvent.setup()
    const listAccounts = vi.fn(() =>
      Promise.resolve({
        accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
        next_cursor: '',
      }),
    )
    const operations = withAccounts({ listAccounts })
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Ada Lovelace')

    const before = listAccounts.mock.calls.length
    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitFor(() =>
      expect(listAccounts.mock.calls.length).toBe(before + 1),
    )
  })
})
