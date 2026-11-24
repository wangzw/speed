import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { AccountIdentity } from './account-identity.js'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
} from '../../test-utils/operations.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const ACCOUNT = 'acc-9'
const MISSING_TITLE = zhCN.common.accountMissing
const UNRESOLVED_TITLE = zhCN.common.accountUnresolved

describe('AccountIdentity', () => {
  it('shows the resolved display name when the account read answers', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<AccountIdentity account="acc-1" />, { operations })
    expect(await screen.findByText('Operator')).toBeInTheDocument()
  })

  it('marks a 404 as not-an-account, not as a permission problem', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount: () =>
          Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    renderWithProviders(<AccountIdentity account={ACCOUNT} />, { operations })
    expect(await screen.findByTitle(MISSING_TITLE)).toBeInTheDocument()
    expect(screen.queryByTitle(UNRESOLVED_TITLE)).toBeNull()
  })

  it('marks a refused read as unresolved, not as a missing account', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount: () =>
          Promise.reject({ status: 403, code: 'authn.forbidden' }),
      }),
    )
    renderWithProviders(<AccountIdentity account={ACCOUNT} />, { operations })
    expect(await screen.findByTitle(UNRESOLVED_TITLE)).toBeInTheDocument()
    expect(screen.queryByTitle(MISSING_TITLE)).toBeNull()
  })

  it('issues no account read when authn.admin is absent and marks the identity unresolved', async () => {
    const getAccount = vi.fn(() => Promise.resolve(accountFixture()))
    const operations = createAdminOperations(makeAdminBackend({ getAccount }))
    renderWithProviders(<AccountIdentity account={ACCOUNT} />, {
      operations,
      hasFeature: (name) => name !== 'authn.admin',
    })
    expect(await screen.findByTitle(UNRESOLVED_TITLE)).toBeInTheDocument()
    expect(screen.queryByTitle(MISSING_TITLE)).toBeNull()
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('uses two distinct markers for the two failures', async () => {
    const missing = createAdminOperations(
      makeAdminBackend({
        getAccount: () =>
          Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    const refused = createAdminOperations(
      makeAdminBackend({
        getAccount: () =>
          Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    const first = renderWithProviders(<AccountIdentity account={ACCOUNT} />, {
      operations: missing,
    })
    const missingMarker = await screen.findByTitle(MISSING_TITLE)
    first.unmount()
    renderWithProviders(<AccountIdentity account={ACCOUNT} />, {
      operations: refused,
    })
    const refusedMarker = await screen.findByTitle(UNRESOLVED_TITLE)
    expect(missingMarker.getAttribute('title')).not.toBe(
      refusedMarker.getAttribute('title'),
    )
  })
})
