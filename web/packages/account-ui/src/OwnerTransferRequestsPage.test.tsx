/**
 * OwnerTransferRequestsPage checks the account-level list and detail and the two decisions.
 */

import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { OwnerTransferRequestsPage } from './OwnerTransferRequestsPage.js'

const LIST_PATH = '/api/v1/auth/me/owner-transfer-requests'
const DETAIL_PATH = `${LIST_PATH}/transfer-1`
const CONFIRM_PATH = `${DETAIL_PATH}/confirm`
const DECLINE_PATH = `${DETAIL_PATH}/decline`
const PROFILE_PATH = '/api/v1/auth/me'

const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

function requestFixture(
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    id: 'transfer-1',
    tenant: 'acme-inc',
    requested_at: '2026-11-01T00:00:00Z',
    expires_at: '2026-11-08T00:00:00Z',
    status: 'pending',
    ...overrides,
  }
}

function mountPage(respond: RealResponder, ui?: ReactElement) {
  const rig = makeRealClientRigWithoutSession(respond)
  rig.store.set('access-1')
  const element =
    ui ??
    (
      <OwnerTransferRequestsPage
        requestHref={(id) => `/transfer-requests/${id}`}
      />
    )
  const rendered = renderWithProviders(element)
  return { rig, ...rendered }
}

async function confirmTwice(
  user: ReturnType<typeof userEvent.setup>,
  confirmLabel: string,
): Promise<void> {
  const dialog = await screen.findByRole('dialog')
  await user.click(within(dialog).getByRole('button', { name: confirmLabel }))
  await act(async () => {
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
  })
  await user.click(
    within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
  )
}

function paths(calls: readonly RealCall[]): readonly string[] {
  return calls.map((call) => call.path)
}

function heldResponse(): {
  readonly promise: Promise<Response>
  readonly resolve: (response: Response) => void
} {
  let resolve!: (response: Response) => void
  const promise = new Promise<Response>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function describeActive(): string {
  const active = document.activeElement
  if (active === null) {
    return 'document.activeElement is null'
  }
  return `document.activeElement is <${active.tagName.toLowerCase()}> ${
    active.textContent ?? ''
  }`
}

describe('OwnerTransferRequestsPage', () => {
  it('lists the pending requests and links each to its detail', async () => {
    const { rig } = mountPage((call) => {
      if (call.path === PROFILE_PATH) {
        return jsonResponse(200, { time_zone: '' })
      }
      if (call.path === LIST_PATH) {
        return jsonResponse(200, { requests: [requestFixture()] })
      }
      return jsonResponse(404, null)
    })

    expect(await screen.findByText('acme-inc')).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: zhCN.transferRequests.open }),
    ).toHaveAttribute('href', '/transfer-requests/transfer-1')
    expect(screen.queryByText(/acc-/)).toBeNull()
    expect(rig.calls.every((call) => call.path.startsWith('/api/v1/auth/'))).toBe(
      true,
    )
    await expectNoAxeViolations()
  })

  it('confirms a pending request and re-reads the list, the detail and the profile', async () => {
    const user = userEvent.setup()
    let status = 'pending'
    const { rig } = mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, {
            request: requestFixture({ status }),
          })
        }
        if (call.method === 'POST' && call.path === CONFIRM_PATH) {
          status = 'confirmed'
          return new Response(null, { status: 204 })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    expect(
      await screen.findByRole('heading', { name: 'acme-inc' }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.transferRequests.status),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.transferRequests.confirm }),
    )
    await confirmTwice(user, zhCN.transferRequests.confirm)

    await waitFor(() =>
      expect(rig.calls).toContainEqual(
        expect.objectContaining({ method: 'POST', path: CONFIRM_PATH }),
      ),
    )
    expect(
      await screen.findByText(
        zhCN.transferRequests.confirmed.replace('{{tenant}}', 'acme-inc'),
      ),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByText(zhCN.transferRequests.terminal)).toBeInTheDocument(),
    )
    expect(screen.getByText(zhCN.transferRequests.statusValues.confirmed)).toBeInTheDocument()
    for (const path of paths(rig.calls)) {
      expect(path.startsWith('/api/v1/auth/')).toBe(true)
    }
  })

  it('declines a pending request without changing ownership', async () => {
    const user = userEvent.setup()
    let status = 'pending'
    const { rig } = mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, {
            request: requestFixture({ status }),
          })
        }
        if (call.method === 'POST' && call.path === DECLINE_PATH) {
          status = 'declined'
          return new Response(null, { status: 204 })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    await screen.findByRole('heading', { name: 'acme-inc' })
    await user.click(
      screen.getByRole('button', { name: zhCN.transferRequests.decline }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.transferRequests.declineTitle.replace(
        '{{tenant}}',
        'acme-inc',
      ),
    })
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.transferRequests.decline,
      }),
    )

    await waitFor(() =>
      expect(rig.calls).toContainEqual(
        expect.objectContaining({ method: 'POST', path: DECLINE_PATH }),
      ),
    )
    expect(
      await screen.findByText(zhCN.transferRequests.declined),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByText(zhCN.transferRequests.statusValues.declined)).toBeInTheDocument(),
    )
  })

  it('re-reads the detail after a conflict and names the expiry', async () => {
    const user = userEvent.setup()
    let status = 'pending'
    mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, {
            request: requestFixture({ status }),
          })
        }
        if (call.method === 'POST' && call.path === CONFIRM_PATH) {
          status = 'expired'
          return jsonResponse(409, {
            type: 'urn:speed:tenant:conflict',
            status: 409,
          })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    await screen.findByRole('heading', { name: 'acme-inc' })
    await user.click(
      screen.getByRole('button', { name: zhCN.transferRequests.confirm }),
    )
    await confirmTwice(user, zhCN.transferRequests.confirm)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.transferRequests.error.expired,
    )
  })

  it('marks only the confirming control busy and lands focus on the request card when its actions leave', async () => {
    const user = userEvent.setup()
    const held = heldResponse()
    let status = 'pending'
    const { rig } = mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, { request: requestFixture({ status }) })
        }
        if (call.method === 'POST' && call.path === CONFIRM_PATH) {
          return held.promise.then(() => {
            status = 'confirmed'
            return new Response(null, { status: 204 })
          })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    await screen.findByRole('heading', { name: 'acme-inc' })
    const confirmButton = screen.getByRole('button', {
      name: zhCN.transferRequests.confirm,
    })
    const declineButton = screen.getByRole('button', {
      name: zhCN.transferRequests.decline,
    })
    await user.click(confirmButton)
    await confirmTwice(user, zhCN.transferRequests.confirm)

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() =>
      expect(confirmButton).toHaveAttribute('aria-busy', 'true'),
    )
    expect(confirmButton).toHaveAttribute('aria-disabled', 'true')
    expect(confirmButton).toHaveFocus()

    expect(declineButton).toHaveAttribute('aria-disabled', 'true')
    expect(declineButton).not.toHaveAttribute('aria-busy')
    expect(declineButton).toHaveAccessibleDescription('')
    await user.click(declineButton)
    await user.keyboard('{Enter}')
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === DECLINE_PATH,
      ),
    ).toHaveLength(0)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(declineButton).toHaveFocus()

    held.resolve(new Response(null, { status: 204 }))
    await screen.findByText(zhCN.transferRequests.terminal)

    await waitFor(() => {
      expect(
        screen.getByRole('group', { name: 'acme-inc' }),
        describeActive(),
      ).toHaveFocus()
    })
  })

  it('marks only the declining control busy and lands focus on the request card when its actions leave', async () => {
    const user = userEvent.setup()
    const held = heldResponse()
    let status = 'pending'
    const { rig } = mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, { request: requestFixture({ status }) })
        }
        if (call.method === 'POST' && call.path === DECLINE_PATH) {
          return held.promise.then(() => {
            status = 'declined'
            return new Response(null, { status: 204 })
          })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    await screen.findByRole('heading', { name: 'acme-inc' })
    const confirmButton = screen.getByRole('button', {
      name: zhCN.transferRequests.confirm,
    })
    const declineButton = screen.getByRole('button', {
      name: zhCN.transferRequests.decline,
    })
    await user.click(declineButton)
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.transferRequests.declineTitle.replace(
        '{{tenant}}',
        'acme-inc',
      ),
    })
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.transferRequests.decline,
      }),
    )

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() =>
      expect(declineButton).toHaveAttribute('aria-busy', 'true'),
    )
    expect(declineButton).toHaveAttribute('aria-disabled', 'true')
    expect(declineButton).toHaveFocus()

    expect(confirmButton).toHaveAttribute('aria-disabled', 'true')
    expect(confirmButton).not.toHaveAttribute('aria-busy')
    expect(confirmButton).toHaveAccessibleDescription('')
    await user.click(confirmButton)
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === CONFIRM_PATH,
      ),
    ).toHaveLength(0)
    expect(screen.queryByRole('dialog')).toBeNull()

    held.resolve(new Response(null, { status: 204 }))
    await screen.findByText(zhCN.transferRequests.terminal)
    await waitFor(() => {
      expect(
        screen.getByRole('group', { name: 'acme-inc' }),
        describeActive(),
      ).toHaveFocus()
    })
  })

  it('leaves focus where the operator moved it while a decision is in flight', async () => {
    const user = userEvent.setup()
    const held = heldResponse()
    let status = 'pending'
    mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, { request: requestFixture({ status }) })
        }
        if (call.method === 'POST' && call.path === CONFIRM_PATH) {
          return held.promise.then(() => {
            status = 'confirmed'
            return new Response(null, { status: 204 })
          })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage
        request="transfer-1"
        listHref="/transfer-requests"
      />,
    )

    await screen.findByRole('heading', { name: 'acme-inc' })
    await user.click(
      screen.getByRole('button', { name: zhCN.transferRequests.confirm }),
    )
    await confirmTwice(user, zhCN.transferRequests.confirm)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    const back = screen.getByRole('link', {
      name: zhCN.transferRequests.back,
    })
    back.focus()

    held.resolve(new Response(null, { status: 204 }))
    await screen.findByText(zhCN.transferRequests.terminal)

    expect(back, describeActive()).toHaveFocus()
    expect(screen.getByRole('group', { name: 'acme-inc' })).not.toHaveFocus()
  })

  it('keeps the decision busy until the detail re-read that reports it settles', async () => {
    const user = userEvent.setup()
    const heldWrite = heldResponse()
    const heldReread = heldResponse()
    let rereadArmed = false
    const { rig } = mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          if (!rereadArmed) {
            return jsonResponse(200, { request: requestFixture() })
          }
          return heldReread.promise
        }
        if (call.method === 'POST' && call.path === CONFIRM_PATH) {
          return heldWrite.promise.then(() => {
            rereadArmed = true
            return new Response(null, { status: 204 })
          })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    await screen.findByRole('heading', { name: 'acme-inc' })
    const confirmButton = screen.getByRole('button', {
      name: zhCN.transferRequests.confirm,
    })
    const declineButton = screen.getByRole('button', {
      name: zhCN.transferRequests.decline,
    })
    await user.click(confirmButton)
    await confirmTwice(user, zhCN.transferRequests.confirm)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() =>
      expect(confirmButton).toHaveAttribute('aria-busy', 'true'),
    )

    await act(async () => {
      heldWrite.resolve(new Response(null, { status: 204 }))
    })
    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) => call.method === 'GET' && call.path === DETAIL_PATH,
        ),
      ).toHaveLength(2),
    )

    expect(confirmButton).toHaveAttribute('aria-busy', 'true')
    expect(confirmButton).toHaveAttribute('aria-disabled', 'true')
    expect(declineButton).toHaveAttribute('aria-disabled', 'true')
    await user.click(confirmButton)
    await user.click(declineButton)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(rig.calls.filter((call) => call.method === 'POST')).toHaveLength(1)

    await act(async () => {
      heldReread.resolve(
        jsonResponse(200, { request: requestFixture({ status: 'confirmed' }) }),
      )
    })
    await screen.findByText(zhCN.transferRequests.terminal)
  })

  it('does not land focus from a decision that settled without removing the request', async () => {
    const user = userEvent.setup()
    let status = 'pending'
    const { queryClient } = mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, { request: requestFixture({ status }) })
        }
        if (call.method === 'POST' && call.path === CONFIRM_PATH) {
          return errorResponse(403, 'authn.forbidden')
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    await screen.findByRole('heading', { name: 'acme-inc' })
    await user.click(
      screen.getByRole('button', { name: zhCN.transferRequests.confirm }),
    )
    await confirmTwice(user, zhCN.transferRequests.confirm)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.transferRequests.error.unknown,
    )

    ;(document.activeElement as HTMLElement | null)?.blur()
    status = 'expired'
    await act(async () => {
      await queryClient.invalidateQueries()
    })
    await screen.findByText(zhCN.transferRequests.terminal)

    expect(
      screen.getByRole('group', { name: 'acme-inc' }),
      describeActive(),
    ).not.toHaveFocus()
  })
})
