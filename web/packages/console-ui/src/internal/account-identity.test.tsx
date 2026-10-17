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
    renderWithProviders(<AccountIdentity account="acc-9" />, { operations })
    expect(
      await screen.findByLabelText('acc-9 (非账户或已删除)'),
    ).toBeInTheDocument()
    expect(screen.queryByLabelText(/身份未解析/)).toBeNull()
  })

  it('marks a refused read as unresolved, not as a missing account', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        getAccount: () =>
          Promise.reject({ status: 403, code: 'authn.forbidden' }),
      }),
    )
    renderWithProviders(<AccountIdentity account="acc-9" />, { operations })
    expect(
      await screen.findByLabelText(
        'acc-9 (身份未解析（需账户读取权限）)',
      ),
    ).toBeInTheDocument()
    expect(screen.queryByLabelText(/非账户或已删除/)).toBeNull()
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
    const first = renderWithProviders(<AccountIdentity account="acc-9" />, {
      operations: missing,
    })
    const missingMarker = await screen.findByLabelText('acc-9 (非账户或已删除)')
    first.unmount()
    renderWithProviders(<AccountIdentity account="acc-9" />, {
      operations: refused,
    })
    const refusedMarker = await screen.findByLabelText(
      'acc-9 (身份未解析（需账户读取权限）)',
    )
    expect(missingMarker.getAttribute('title')).not.toBe(
      refusedMarker.getAttribute('title'),
    )
  })
})
