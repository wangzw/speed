/**
 * AccountIdentity: one recorded account identifier rendered as its readable
 * identity.
 *
 * The account read's failures mean different things: a 404 says the
 * identifier names no account (deleted, or never one), while a 403 says the
 * viewer may not read accounts. The two markers must differ, so a deleted
 * actor is never read as a permission problem (or the reverse).
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { AccountIdentity } from './account-identity.js'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../../test-utils/operations.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const ACCOUNT = 'acc-9'
/** The two distinct markers an unresolved account read renders. */
const MISSING_LABEL = `${ACCOUNT} (${zhCN.common.accountMissing})`
const UNRESOLVED_LABEL = `${ACCOUNT} (${zhCN.common.accountUnresolved})`

describe('AccountIdentity', () => {
  it('shows the resolved display name when the account read answers', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<AccountIdentity account="acc-1" />, { operations })
    expect(await screen.findByText('Operator')).toBeInTheDocument()
  })

  it('marks a 404 as not-an-account, not as a permission problem', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getAccount: () =>
          Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    renderWithProviders(<AccountIdentity account={ACCOUNT} />, { operations })
    expect(await screen.findByLabelText(MISSING_LABEL)).toBeInTheDocument()
    expect(screen.queryByLabelText(UNRESOLVED_LABEL)).toBeNull()
  })

  it('marks a refused read as unresolved, not as a missing account', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getAccount: () =>
          Promise.reject({ status: 403, code: 'authn.forbidden' }),
      }),
    )
    renderWithProviders(<AccountIdentity account={ACCOUNT} />, { operations })
    expect(await screen.findByLabelText(UNRESOLVED_LABEL)).toBeInTheDocument()
    expect(screen.queryByLabelText(MISSING_LABEL)).toBeNull()
  })

  it('uses two distinct markers for the two failures', async () => {
    const missing = createConsoleOperations(
      makeConsoleBackend({
        getAccount: () =>
          Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    const refused = createConsoleOperations(
      makeConsoleBackend({
        getAccount: () =>
          Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    const first = renderWithProviders(<AccountIdentity account={ACCOUNT} />, {
      operations: missing,
    })
    const missingMarker = await screen.findByLabelText(MISSING_LABEL)
    first.unmount()
    renderWithProviders(<AccountIdentity account={ACCOUNT} />, {
      operations: refused,
    })
    const refusedMarker = await screen.findByLabelText(UNRESOLVED_LABEL)
    expect(missingMarker.getAttribute('title')).not.toBe(
      refusedMarker.getAttribute('title'),
    )
  })
})
